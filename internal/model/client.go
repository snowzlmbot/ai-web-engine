package model

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 0}}
}

type Delta struct {
	Content string
	Done    bool
}

func (c *Client) Stream(cfg config.Config, modelID, reasoning, system string, messages []session.Message, emit func(Delta) error) error {
	if cfg.APIKey == "" {
		return errors.New("API key is not configured")
	}
	body, headers, err := request(cfg, modelID, reasoning, system, messages)
	if err != nil {
		return err
	}
	endpoint, err := endpointURL(cfg.Endpoint, cfg.Protocol)
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
		return fmt.Errorf("provider request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		return fmt.Errorf("provider HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return parseSSE(cfg.Protocol, resp.Body, emit)
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
	if strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/chat/completions") {
		return u.String(), nil
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/chat/completions"
	return u.String(), nil
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
	if reasoning != "off" && cfg.Protocol == config.ProtocolOpenAI {
		payload["reasoning_effort"] = reasoning
	}
	if cfg.Protocol == config.ProtocolAnthropic {
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
