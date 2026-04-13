package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// APIClient is a stateless HTTP client for the Anthropic Messages API.
type APIClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// SendArgs bundles per-call parameters for Send.
type SendArgs struct {
	System   string    // system prompt (optional)
	Messages []Message // conversation history including the new user message
	Model    string    // override model (empty = use client default)
}

// NewAPIClient constructs an APIClient.
func NewAPIClient(baseURL, apiKey, model string) *APIClient {
	return &APIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{},
	}
}

type apiRequest struct {
	Model     string    `json:"model"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
}

type apiResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Send POSTs to <baseURL>/v1/messages and returns the first text content block.
func (c *APIClient) Send(ctx context.Context, args SendArgs) (string, error) {
	model := c.model
	if args.Model != "" {
		model = args.Model
	}

	reqBody := apiRequest{
		Model:     model,
		System:    args.System,
		Messages:  args.Messages,
		MaxTokens: 4096,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("api: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("api: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("api: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("api: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api: status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed apiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("api: unmarshal response: %w", err)
	}

	for _, block := range parsed.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("api: no text content in response")
}
