// Package aiprovider is a tiny, dependency-free client for generating text from
// a hosted LLM using a caller-supplied API token (bring-your-own-key). Only the
// Anthropic Messages API is implemented for now; the Provider field leaves room
// for others.
package aiprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Request is a single-turn generation request.
type Request struct {
	Provider  string // "anthropic" (default)
	Model     string
	APIToken  string
	System    string // system / context prompt
	Prompt    string // the user message (the thread transcript)
	MaxTokens int
}

// Endpoint overrides for testing; empty uses the provider default.
var anthropicEndpoint = "https://api.anthropic.com/v1/messages"

var httpClient = &http.Client{Timeout: 60 * time.Second}

// Generate returns the model's text completion, or an error.
func Generate(ctx context.Context, req Request) (string, error) {
	switch strings.ToLower(strings.TrimSpace(req.Provider)) {
	case "", "anthropic":
		return generateAnthropic(ctx, req)
	default:
		return "", fmt.Errorf("unsupported AI provider %q", req.Provider)
	}
}

func generateAnthropic(ctx context.Context, req Request) (string, error) {
	if strings.TrimSpace(req.APIToken) == "" {
		return "", fmt.Errorf("missing API token")
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": maxTokens,
		"messages":   []map[string]any{{"role": "user", "content": req.Prompt}},
	}
	if strings.TrimSpace(req.System) != "" {
		body["system"] = req.System
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", req.APIToken)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic API status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("anthropic API: decode response: %w", err)
	}
	var sb strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", fmt.Errorf("anthropic API: empty completion")
	}
	return out, nil
}
