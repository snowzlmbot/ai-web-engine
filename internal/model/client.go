package model

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	} else {
		transport = transport.Clone()
	}
	pool := androidCertPool()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	transport.DialContext = androidDialContext
	return &Client{HTTP: &http.Client{Transport: transport, Timeout: 0}}
}

type Delta struct {
	Content string
	Done    bool
}

func (c *Client) Stream(cfg config.Config, modelID, reasoning, system string, messages []session.Message, emit func(Delta) error) error {
	if cfg.APIKey == "" {
		return errors.New("API key is not configured")
	}
	protocol := effectiveProtocol(cfg)
	body, headers, err := request(cfg, modelID, reasoning, system, messages)
	if err != nil {
		return err
	}
	endpoint, err := endpointURL(cfg.Endpoint, protocol)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent())
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Android's native curl uses bionic/netd for name resolution. A
		// CGO-disabled Go binary cannot always reach that resolver directly,
		// so retry the same HTTPS request through the system curl without
		// putting the API key in argv, logs, or persistent configuration.
		if fallbackErr := streamWithAndroidCurl(endpoint, body, headers, protocol, emit); fallbackErr == nil {
			return nil
		} else {
			return fmt.Errorf("provider request: %w; Android system curl fallback: %v", err, fallbackErr)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		return fmt.Errorf("provider HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return parseSSE(protocol, resp.Body, emit)
}

func endpointURL(raw, protocol string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("endpoint must be an HTTPS URL")
	}
	if protocol == config.ProtocolAnthropic {
		if strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/messages") {
			return u.String(), nil
		}
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/messages"
		return u.String(), nil
	}
	if protocol == config.ProtocolOpenAIResponses {
		clean := strings.TrimRight(u.Path, "/")
		if strings.HasSuffix(clean, "/responses") {
			u.Path = clean
			return u.String(), nil
		}
		if strings.HasSuffix(clean, "/chat/completions") {
			return "", errors.New("Responses 协议端点不能使用 chat/completions 路径")
		}
		if clean == "" {
			u.Path = "/v1/responses"
		} else if clean == "/v1" {
			u.Path = "/v1/responses"
		} else {
			u.Path = clean + "/v1/responses"
		}
		return u.String(), nil
	}
	if strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/chat/completions") {
		return u.String(), nil
	}
	clean := strings.TrimRight(u.Path, "/")
	if clean == "" {
		u.Path = "/v1/chat/completions"
	} else if clean == "/v1" {
		u.Path = "/v1/chat/completions"
	} else {
		u.Path = clean + "/v1/chat/completions"
	}
	return u.String(), nil
}

func effectiveProtocol(cfg config.Config) string {
	if cfg.Protocol == config.ProtocolOpenAI {
		if u, err := url.Parse(strings.TrimSpace(cfg.Endpoint)); err == nil && strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/responses") {
			return config.ProtocolOpenAIResponses
		}
	}
	return cfg.Protocol
}

func request(cfg config.Config, modelID, reasoning, system string, messages []session.Message) ([]byte, map[string]string, error) {
	if modelID == "" {
		return nil, nil, errors.New("model id is required")
	}
	if system == "" {
		return nil, nil, errors.New("skill system prompt is empty")
	}
	all := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		all = append(all, map[string]string{"role": message.Role, "content": message.Content})
	}
	payload := map[string]any{"model": modelID, "stream": true}
	protocol := effectiveProtocol(cfg)
	if reasoning != "off" && (protocol == config.ProtocolOpenAI || protocol == config.ProtocolOpenAIResponses) {
		if protocol == config.ProtocolOpenAIResponses {
			payload["reasoning"] = map[string]string{"effort": reasoning}
		} else {
			payload["reasoning_effort"] = reasoning
		}
	}
	if protocol == config.ProtocolAnthropic {
		payload["system"] = system
		payload["messages"] = all
		payload["max_tokens"] = 4096
		data, err := json.Marshal(payload)
		return data, map[string]string{
			"Content-Type":      "application/json",
			"x-api-key":         cfg.APIKey,
			"anthropic-version": "2023-06-01",
		}, err
	}
	if protocol == config.ProtocolOpenAIResponses {
		type responseContent struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		type responseMessage struct {
			Role    string            `json:"role"`
			Content []responseContent `json:"content"`
		}
		input := make([]responseMessage, 0, len(messages))
		for _, message := range messages {
			role := message.Role
			contentType := "input_text"
			if role == "assistant" {
				contentType = "output_text"
			}
			input = append(input, responseMessage{
				Role:    role,
				Content: []responseContent{{Type: contentType, Text: message.Content}},
			})
		}
		payload["instructions"] = system
		payload["input"] = input
		data, err := json.Marshal(payload)
		return data, map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + cfg.APIKey,
		}, err
	}
	payload["messages"] = append([]map[string]string{{"role": "system", "content": system}}, all...)
	data, err := json.Marshal(payload)
	return data, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + cfg.APIKey,
	}, err
}

func parseSSE(protocol string, reader io.Reader, emit func(Delta) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		if payload == "[DONE]" {
			return emit(Delta{Done: true})
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(payload), &object); err != nil {
			return nil
		}
		if protocol == config.ProtocolOpenAIResponses {
			typeName, _ := object["type"].(string)
			switch typeName {
			case "response.completed", "response.done":
				return emit(Delta{Done: true})
			case "response.failed", "response.error":
				return fmt.Errorf("Responses API returned %s", typeName)
			}
		}
		token := extract(protocol, object)
		if token != "" {
			return emit(Delta{Content: token})
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func extract(protocol string, object map[string]any) string {
	if protocol == config.ProtocolOpenAIResponses {
		if typeName, _ := object["type"].(string); typeName == "response.output_text.delta" {
			text, _ := object["delta"].(string)
			return text
		}
		if text, ok := object["delta"].(string); ok {
			return text
		}
		return ""
	}
	if protocol == config.ProtocolAnthropic {
		if delta, ok := object["delta"].(map[string]any); ok {
			if text, ok := delta["text"].(string); ok {
				return text
			}
		}
		return ""
	}
	if text, ok := object["delta"].(string); ok {
		return text
	}
	if choices, ok := object["choices"].([]any); ok && len(choices) > 0 {
		if first, ok := choices[0].(map[string]any); ok {
			if delta, ok := first["delta"].(map[string]any); ok {
				text, _ := delta["content"].(string)
				return text
			}
		}
	}
	return ""
}

func UserAgent() string { return "ai-web-engine/1.0" }

func androidCertPool() *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	for _, path := range []string{
		"/system/etc/security/cacerts",
		"/apex/com.android.conscrypt/cacerts",
		"/system/etc/security/cacerts/ca-certificates.crt",
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/ssl/cert.pem",
	} {
		appendCertPath(pool, path)
	}
	return pool
}

func appendCertPath(pool *x509.CertPool, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if !info.IsDir() {
		if data, readErr := os.ReadFile(path); readErr == nil {
			pool.AppendCertsFromPEM(data)
		}
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(path, entry.Name()))
		if readErr == nil {
			pool.AppendCertsFromPEM(data)
		}
	}
}

// androidDialContext keeps TLS verification on the original hostname while
// resolving through Android's system DNS properties. Pure-Go networking may
// otherwise read a broken /etc/resolv.conf such as nameserver ::1.
func androidDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) != nil {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	ips, err := androidLookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{}
	var lastErr error
	for _, ip := range ips {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("no address returned")
	}
	return nil, fmt.Errorf("connect %s: %w", host, lastErr)
}

func androidLookupHost(ctx context.Context, host string) ([]string, error) {
	if ips := systemResolverLookup(ctx, host); len(ips) > 0 {
		return ips, nil
	}
	servers := androidDNSServers()
	if len(servers) == 0 {
		return nil, fmt.Errorf("DNS resolution for %s failed: Android system resolver and DNS properties are unavailable", host)
	}
	var lastErr error
	for _, server := range servers {
		resolver := &net.Resolver{
			PreferGo: true,
			Dial: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: 3 * time.Second}
				return dialer.DialContext(dialCtx, "udp", net.JoinHostPort(server, "53"))
			},
		}
		ips, err := resolver.LookupHost(ctx, host)
		if err == nil && len(ips) > 0 {
			return ips, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no DNS response")
	}
	return nil, fmt.Errorf("DNS resolution for %s failed using Android system resolver: %w", host, lastErr)
}

// systemResolverLookup asks Android's own resolver through commands that are
// available on common system/root shells. This is important on newer Android
// versions where net.dns1..4 are empty because netd owns private DNS state.
// The returned IP is dialed directly while net/http still performs TLS SNI
// and certificate verification against the original hostname.
func systemResolverLookup(ctx context.Context, host string) []string {
	commands := [][]string{
		{"/system/bin/getent", "ahostsv4", host},
		{"getent", "ahostsv4", host},
		{"/system/bin/nslookup", host},
		{"nslookup", host},
		{"toybox", "nslookup", host},
		{"/system/bin/toybox", "nslookup", host},
		{"/system/bin/ping", "-c", "1", "-W", "1", host},
		{"ping", "-c", "1", "-W", "1", host},
		{"toybox", "ping", "-c", "1", "-W", "1", host},
		{"/system/bin/toybox", "ping", "-c", "1", "-W", "1", host},
	}
	for _, command := range commands {
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		output, _ := exec.CommandContext(lookupCtx, command[0], command[1:]...).CombinedOutput()
		cancel()
		// ping can resolve successfully and still exit non-zero when ICMP is
		// blocked. Trust parsed addresses, not the command exit status.
		if ips := parseResolverOutput(string(output)); len(ips) > 0 {
			return ips
		}
	}
	return nil
}

func streamWithAndroidCurl(endpoint string, body []byte, headers map[string]string, protocol string, emit func(Delta) error) error {
	curl, err := androidCurlBinary()
	if err != nil {
		return err
	}
	bodyFile, err := os.CreateTemp("", ".ai-web-engine-request-*")
	if err != nil {
		return fmt.Errorf("create temporary request body: %w", err)
	}
	bodyPath := bodyFile.Name()
	defer os.Remove(bodyPath)
	if err := bodyFile.Chmod(0o600); err != nil {
		_ = bodyFile.Close()
		return fmt.Errorf("protect temporary request body: %w", err)
	}
	if _, err := bodyFile.Write(body); err != nil {
		_ = bodyFile.Close()
		return fmt.Errorf("write temporary request body: %w", err)
	}
	if err := bodyFile.Close(); err != nil {
		return fmt.Errorf("close temporary request body: %w", err)
	}

	headerFile, err := os.CreateTemp("", ".ai-web-engine-response-*")
	if err != nil {
		return fmt.Errorf("create temporary response headers: %w", err)
	}
	headerPath := headerFile.Name()
	_ = headerFile.Close()
	defer os.Remove(headerPath)
	_ = os.Chmod(headerPath, 0o600)

	var config strings.Builder
	config.WriteString("request = \"POST\"\n")
	config.WriteString("url = ")
	config.WriteString(curlConfigValue(endpoint))
	config.WriteByte('\n')
	config.WriteString("no-buffer\nshow-error\nsilent\n")
	config.WriteString("connect-timeout = \"15\"\n")
	config.WriteString("dump-header = ")
	config.WriteString(curlConfigValue(headerPath))
	config.WriteByte('\n')
	config.WriteString("data-binary = ")
	config.WriteString(curlConfigValue("@" + bodyPath))
	config.WriteByte('\n')
	for key, value := range headers {
		config.WriteString("header = ")
		config.WriteString(curlConfigValue(key + ": " + value))
		config.WriteByte('\n')
	}

	cmd := exec.Command(curl, "--config", "-")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open curl stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open curl stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Android curl: %w", err)
	}
	if _, err := io.WriteString(stdin, config.String()); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return fmt.Errorf("send curl request configuration: %w", err)
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Wait()
		return fmt.Errorf("close curl configuration: %w", err)
	}

	var captured limitedBuffer
	parseErr := parseSSE(protocol, io.TeeReader(stdout, &captured), emit)
	waitErr := cmd.Wait()
	status := curlResponseStatus(headerPath)
	if status >= 400 {
		return fmt.Errorf("provider HTTP %d: %s", status, strings.TrimSpace(captured.String()))
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(captured.String())
		}
		return fmt.Errorf("curl failed: %s", message)
	}
	if parseErr != nil {
		return parseErr
	}
	return nil
}

func androidCurlBinary() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AI_WEB_ENGINE_CURL")); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return override, nil
		}
	}
	for _, candidate := range []string{"/system/bin/curl", "/system/xbin/curl", "curl"} {
		if strings.Contains(candidate, "/") {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("Android system curl is unavailable")
}

func curlConfigValue(value string) string {
	value = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\r", "\\r", "\n", "\\n").Replace(value)
	return "\"" + value + "\""
}

type limitedBuffer struct {
	data []byte
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	originalLen := len(value)
	if len(b.data) < 32<<10 {
		remaining := (32 << 10) - len(b.data)
		if len(value) > remaining {
			value = value[:remaining]
		}
		b.data = append(b.data, value...)
	}
	return originalLen, nil
}

func (b *limitedBuffer) String() string { return string(b.data) }

func curlResponseStatus(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	status := 0
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[0], "HTTP/") {
			if value, err := strconv.Atoi(fields[1]); err == nil {
				status = value
			}
		}
	}
	return status
}

func parseResolverOutput(output string) []string {
	ips := make([]string, 0, 4)
	for _, line := range strings.Split(output, "\n") {
		for _, field := range strings.Fields(line) {
			value := strings.Trim(field, "()[],:;")
			ip := net.ParseIP(value)
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			canonical := ip.String()
			found := false
			for _, existing := range ips {
				if existing == canonical {
					found = true
					break
				}
			}
			if !found {
				ips = append(ips, canonical)
			}
		}
	}
	return ips
}

func androidDNSServers() []string {
	servers := make([]string, 0, 6)
	if override := os.Getenv("AI_WEB_ENGINE_DNS"); override != "" {
		for _, value := range strings.Split(override, ",") {
			servers = appendUniqueDNS(servers, value)
		}
	}
	for _, property := range []string{"net.dns1", "net.dns2", "net.dns3", "net.dns4"} {
		if value := getprop(property); value != "" {
			servers = appendUniqueDNS(servers, value)
		}
	}
	for _, value := range androidNetdDNSServers() {
		servers = appendUniqueDNS(servers, value)
	}
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "nameserver" {
				servers = appendUniqueDNS(servers, fields[1])
			}
		}
	}
	return servers
}

// androidNetdDNSServers reads resolver state owned by Android's netd. On
// recent releases net.dns1..4 may be empty while netd still has the active
// network DNS servers. The output format differs across Android versions, so
// only syntactically valid non-loopback IPs are retained.
func androidNetdDNSServers() []string {
	servers := make([]string, 0, 4)
	commands := [][]string{
		{"/system/bin/cmd", "netd", "resolver", "getnetdns"},
		{"cmd", "netd", "resolver", "getnetdns"},
		{"/system/bin/cmd", "netd", "resolver", "getnetdns", "0"},
		{"cmd", "netd", "resolver", "getnetdns", "0"},
	}
	for _, command := range commands {
		if !commandAvailable(command[0]) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		output, _ := exec.CommandContext(ctx, command[0], command[1:]...).CombinedOutput()
		cancel()
		for _, value := range parseResolverOutput(string(output)) {
			servers = appendUniqueDNS(servers, value)
		}
		if len(servers) > 0 {
			return servers
		}
	}
	return servers
}

func commandAvailable(command string) bool {
	if strings.Contains(command, "/") {
		info, err := os.Stat(command)
		return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
	}
	_, err := exec.LookPath(command)
	return err == nil
}

func getprop(property string) string {
	for _, command := range []string{"/system/bin/getprop", "getprop"} {
		output, err := exec.Command(command, property).Output()
		if err == nil {
			value := strings.TrimSpace(string(output))
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func appendUniqueDNS(servers []string, value string) []string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return servers
	}
	canonical := ip.String()
	for _, existing := range servers {
		if existing == canonical {
			return servers
		}
	}
	return append(servers, canonical)
}
