package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxToolIterations = 10

// ToolExecutor executes a tool call and returns the result text.
type ToolExecutor func(ctx context.Context, name string, input json.RawMessage) (string, error)

// APIClient is a stateless HTTP client for the Anthropic Messages API.
type APIClient struct {
	baseURL      string
	apiKey       string
	model        string
	http         *http.Client
	toolExecutor ToolExecutor
	toolSchemas  []json.RawMessage
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

// SetToolExecutor configures the function used to execute tool calls.
func (c *APIClient) SetToolExecutor(fn ToolExecutor) { c.toolExecutor = fn }

// SetToolSchemas configures the tool definitions sent with each request.
func (c *APIClient) SetToolSchemas(schemas []json.RawMessage) { c.toolSchemas = schemas }

// contentBlock represents a single block in the API response content array.
type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type apiResponse struct {
	StopReason string         `json:"stop_reason"`
	Content    []contentBlock `json:"content"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Send POSTs to <baseURL>/v1/messages and returns the first text content block.
// If tools are configured and the model requests tool use, Send loops: executing
// tools and feeding results back until the model returns a text response or the
// iteration limit is reached.
func (c *APIClient) Send(ctx context.Context, args SendArgs) (string, error) {
	model := c.model
	if args.Model != "" {
		model = args.Model
	}

	// Build the messages array as []any so we can append tool_result objects
	// that have a different shape than regular Message structs.
	messages := make([]any, len(args.Messages))
	for i, m := range args.Messages {
		messages[i] = m
	}

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		reqMap := map[string]any{
			"model":      model,
			"messages":   messages,
			"max_tokens": 4096,
		}
		if args.System != "" {
			reqMap["system"] = args.System
		}
		if len(c.toolSchemas) > 0 {
			reqMap["tools"] = c.toolSchemas
		}

		data, err := json.Marshal(reqMap)
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

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
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

		if parsed.StopReason != "tool_use" {
			// No tool use — extract first text block and return.
			for _, block := range parsed.Content {
				if block.Type == "text" {
					return block.Text, nil
				}
			}
			return "", fmt.Errorf("api: no text content in response")
		}

		// Tool use requested — append the assistant's response and execute tools.
		messages = append(messages, map[string]any{
			"role":    "assistant",
			"content": parsed.Content,
		})

		var toolResults []map[string]any
		for _, block := range parsed.Content {
			if block.Type != "tool_use" {
				continue
			}
			if c.toolExecutor == nil {
				return "", fmt.Errorf("api: tool_use requested but no executor configured")
			}
			result, execErr := c.toolExecutor(ctx, block.Name, block.Input)
			if execErr != nil {
				result = fmt.Sprintf("error: %s", execErr.Error())
			}
			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": block.ID,
				"content":     result,
			})
		}

		messages = append(messages, map[string]any{
			"role":    "user",
			"content": toolResults,
		})
	}

	return "", fmt.Errorf("api: max tool iterations (%d) exceeded", maxToolIterations)
}
