package model

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

func TestEndpointURL(t *testing.T) {
	cases := []struct {
		name, endpoint, protocol, want string
	}{
		{"openai-base", "https://api.example.test", config.ProtocolOpenAI, "https://api.example.test/v1/chat/completions"},
		{"openai-full", "https://api.example.test/v1/chat/completions", config.ProtocolOpenAI, "https://api.example.test/v1/chat/completions"},
		{"responses-base", "https://api.example.test", config.ProtocolOpenAIResponses, "https://api.example.test/v1/responses"},
		{"responses-full", "https://api.example.test/v1/responses", config.ProtocolOpenAIResponses, "https://api.example.test/v1/responses"},
		{"responses-trailing-slash", "https://api.example.test/v1/responses/", config.ProtocolOpenAIResponses, "https://api.example.test/v1/responses"},
		{"anthropic-base", "https://api.example.test", config.ProtocolAnthropic, "https://api.example.test/v1/messages"},
		{"anthropic-full", "https://api.example.test/v1/messages", config.ProtocolAnthropic, "https://api.example.test/v1/messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := endpointURL(tc.endpoint, tc.protocol)
			if err != nil || got != tc.want {
				t.Fatalf("endpointURL() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestRequestShapes(t *testing.T) {
	cfg := config.Config{Protocol: config.ProtocolOpenAI, APIKey: "test-key"}
	body, headers, err := request(cfg, "demo", "low", "skill", []session.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if headers["Authorization"] != "Bearer test-key" {
		t.Fatalf("authorization header = %q", headers["Authorization"])
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["stream"] != true || payload["reasoning_effort"] != "low" {
		t.Fatalf("unexpected OpenAI payload: %#v", payload)
	}
	messages := payload["messages"].([]any)
	if messages[0].(map[string]any)["role"] != "system" {
		t.Fatal("system message was not first")
	}

	cfg.Protocol = config.ProtocolAnthropic
	body, headers, err = request(cfg, "claude", "off", "skill", nil)
	if err != nil {
		t.Fatal(err)
	}
	if headers["x-api-key"] != "test-key" || headers["anthropic-version"] != "2023-06-01" {
		t.Fatalf("unexpected Anthropic headers: %#v", headers)
	}
	payload = map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["reasoning_effort"]; ok {
		t.Fatal("off reasoning level should not be sent")
	}
}

func TestParseSSE(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: [DONE]\n\n"
	var got string
	var done bool
	if err := parseSSE(config.ProtocolOpenAI, strings.NewReader(input), func(delta Delta) error {
		got += delta.Content
		done = done || delta.Done
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != "hi" || !done {
		t.Fatalf("parsed SSE = %q, done=%v", got, done)
	}
}

func TestClientStreamOverHTTPS(t *testing.T) {
	var gotAuth string
	var gotPath string
	var gotModel string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotModel, _ = payload["model"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ShortX \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Rule\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer provider.Close()

	cfg := config.Config{Endpoint: provider.URL, Protocol: config.ProtocolOpenAI, APIKey: "test-key"}
	client := &Client{HTTP: provider.Client()}
	var text string
	var done bool
	if err := client.Stream(cfg, "demo-model", "low", "skill", []session.Message{{Role: "user", Content: "hello"}}, func(delta Delta) error {
		text += delta.Content
		done = done || delta.Done
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" || gotAuth != "Bearer test-key" || gotModel != "demo-model" {
		t.Fatalf("request path/auth/model = %q/%q/%q", gotPath, gotAuth, gotModel)
	}
	if text != "ShortX Rule" || !done {
		t.Fatalf("stream = %q, done=%v", text, done)
	}
}

func TestResponsesRequestAndSSE(t *testing.T) {
	cfg := config.Config{Endpoint: "https://cc-vibe.com/v1/responses", Protocol: config.ProtocolOpenAIResponses, APIKey: "test-key"}
	body, headers, err := request(cfg, "gpt-5.6-sol", "medium", "skill instructions", []session.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if headers["Authorization"] != "Bearer test-key" {
		t.Fatalf("authorization header = %q", headers["Authorization"])
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "gpt-5.6-sol" || payload["stream"] != true || payload["instructions"] != "skill instructions" {
		t.Fatalf("unexpected Responses payload: %#v", payload)
	}
	if _, ok := payload["messages"]; ok {
		t.Fatal("Responses payload must not contain messages")
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("unexpected Responses input: %#v", payload["input"])
	}
	message, ok := input[0].(map[string]any)
	if !ok || message["role"] != "user" {
		t.Fatalf("unexpected Responses message: %#v", input[0])
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("unexpected Responses content: %#v", message["content"])
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "input_text" || block["text"] != "hello" {
		t.Fatalf("unexpected Responses content block: %#v", content[0])
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" {
		t.Fatalf("unexpected Responses reasoning: %#v", payload["reasoning"])
	}

	stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n" +
		"data: {\"type\":\"response.completed\"}\n\n"
	var text string
	var done bool
	if err := parseSSE(config.ProtocolOpenAIResponses, strings.NewReader(stream), func(delta Delta) error {
		text += delta.Content
		done = done || delta.Done
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if text != "hello world" || !done {
		t.Fatalf("Responses SSE = %q, done=%v", text, done)
	}
}

func TestOpenAIEndpointAutomaticallySelectsResponsesProtocol(t *testing.T) {
	cfg := config.Config{Endpoint: "https://cc-vibe.com/v1/responses", Protocol: config.ProtocolOpenAI, APIKey: "test-key"}
	body, headers, err := request(cfg, "gpt-5.6-sol", "off", "skill", []session.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := endpointURL(cfg.Endpoint, effectiveProtocol(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://cc-vibe.com/v1/responses" {
		t.Fatalf("auto-detected endpoint = %q", endpoint)
	}
	if headers["Authorization"] != "Bearer test-key" {
		t.Fatalf("authorization header = %q", headers["Authorization"])
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["input"]; !ok {
		t.Fatalf("auto-detected request did not use Responses input: %#v", payload)
	}
	if _, ok := payload["messages"]; ok {
		t.Fatalf("auto-detected request still used Chat messages: %#v", payload)
	}
}

func TestResponsesClientStreamUsesResponsesEndpoint(t *testing.T) {
	var gotPath string
	var gotPayload map[string]any
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer provider.Close()

	cfg := config.Config{Endpoint: provider.URL + "/v1/responses", Protocol: config.ProtocolOpenAIResponses, APIKey: "test-key"}
	client := &Client{HTTP: provider.Client()}
	var text string
	var done bool
	if err := client.Stream(cfg, "gpt-5.6-sol", "medium", "skill", []session.Message{{Role: "user", Content: "hello"}}, func(delta Delta) error {
		text += delta.Content
		done = done || delta.Done
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/responses" || text != "ok" || !done {
		t.Fatalf("path/text/done = %q/%q/%v", gotPath, text, done)
	}
	if gotPayload["instructions"] != "skill" || gotPayload["messages"] != nil {
		t.Fatalf("unexpected Responses payload: %#v", gotPayload)
	}
}

func TestDNSAddressFiltering(t *testing.T) {
	servers := []string{}
	for _, value := range []string{"::1", "127.0.0.1", "8.8.8.8", "8.8.8.8", "2001:4860:4860::8888", "not-an-ip"} {
		servers = appendUniqueDNS(servers, value)
	}
	want := []string{"8.8.8.8", "2001:4860:4860::8888"}
	if strings.Join(servers, ",") != strings.Join(want, ",") {
		t.Fatalf("DNS servers = %v, want %v", servers, want)
	}
}

func TestNewClientUsesAndroidAwareTransport(t *testing.T) {
	client := NewClient()
	transport, ok := client.HTTP.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil {
		t.Fatal("NewClient did not install an Android-aware DialContext")
	}
}

func TestParseResolverOutput(t *testing.T) {
	output := "cc-vibe.com has address 45.77.158.239\nName: cc-vibe.com\nAddress: 45.77.158.239\nAddress: ::1\n"
	got := parseResolverOutput(output)
	if strings.Join(got, ",") != "45.77.158.239" {
		t.Fatalf("resolver output = %v", got)
	}
}

func TestSystemResolverDoesNotRequireNetDNSProperties(t *testing.T) {
	t.Setenv("AI_WEB_ENGINE_DNS", "")
	if got := parseResolverOutput("Name: example\nAddress: 192.0.2.10\n"); len(got) != 1 || got[0] != "192.0.2.10" {
		t.Fatalf("system resolver parser = %v", got)
	}
}

func TestClientStreamReportsProviderHTTPError(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider rejected request", http.StatusUnauthorized)
	}))
	defer provider.Close()

	cfg := config.Config{Endpoint: provider.URL, Protocol: config.ProtocolOpenAI, APIKey: "test-key"}
	client := &Client{HTTP: provider.Client()}
	err := client.Stream(cfg, "demo-model", "off", "skill", nil, func(Delta) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "provider HTTP 401") || !strings.Contains(err.Error(), "provider rejected request") {
		t.Fatalf("unexpected provider error: %v", err)
	}
}

func TestAndroidCurlFallbackUsesStdinConfigWithoutSecretInArgv(t *testing.T) {
	curlPath := "/tmp/fake-android-curl-v2"
	if _, err := os.Stat(curlPath); err != nil {
		t.Skip("fake curl fixture is supplied by the integration runner")
	}
	_ = os.Chmod(curlPath, 0o755)
	t.Setenv("AI_WEB_ENGINE_CURL", curlPath)
	failingTransport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("simulated pure-Go DNS failure")
	})
	client := &Client{HTTP: &http.Client{Transport: failingTransport}}
	cfg := config.Config{Endpoint: "https://provider.example/v1/chat/completions", Protocol: config.ProtocolOpenAI, APIKey: "test-fallback-key"}
	var text string
	var done bool
	if err := client.Stream(cfg, "demo", "off", "skill", nil, func(delta Delta) error {
		text += delta.Content
		done = done || delta.Done
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if text != "fallback-ok" || !done {
		t.Fatalf("fallback stream = %q, done=%v", text, done)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
