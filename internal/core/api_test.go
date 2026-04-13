package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIClient_Send_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method and path
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/messages", r.URL.Path)

		// Verify headers
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))

		// Verify body
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "claude-test", req["model"])
		assert.Equal(t, "You are helpful.", req["system"])
		assert.Equal(t, float64(4096), req["max_tokens"])
		msgs := req["messages"].([]any)
		require.Len(t, msgs, 1)
		msg := msgs[0].(map[string]any)
		assert.Equal(t, "user", msg["role"])
		assert.Equal(t, "Hello", msg["content"])

		// Return a valid response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apiResponse{
			StopReason: "end_turn",
			Content: []contentBlock{
				{Type: "text", Text: "Hi there!"},
			},
		})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	result, err := client.Send(context.Background(), SendArgs{
		System: "You are helpful.",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "Hi there!", result)
}

func TestAPIClient_Send_ModelOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))

		// Should use the override model, not the client default
		assert.Equal(t, "claude-override", req["model"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apiResponse{
			StopReason: "end_turn",
			Content: []contentBlock{
				{Type: "text", Text: "Overridden"},
			},
		})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", "claude-default")
	result, err := client.Send(context.Background(), SendArgs{
		Model:    "claude-override",
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "Overridden", result)
}

func TestAPIClient_Send_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"rate limited"}}`))
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	_, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "429"), "error should contain status code 429, got: %s", err.Error())
}

func TestAPIClient_Send_HistoryPassedThrough(t *testing.T) {
	history := []Message{
		{Role: "user", Content: "first message"},
		{Role: "assistant", Content: "first reply"},
		{Role: "user", Content: "second message"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))

		// All history messages must arrive intact
		msgs := req["messages"].([]any)
		require.Len(t, msgs, 3)
		m0 := msgs[0].(map[string]any)
		assert.Equal(t, "user", m0["role"])
		assert.Equal(t, "first message", m0["content"])
		m1 := msgs[1].(map[string]any)
		assert.Equal(t, "assistant", m1["role"])
		assert.Equal(t, "first reply", m1["content"])
		m2 := msgs[2].(map[string]any)
		assert.Equal(t, "user", m2["role"])
		assert.Equal(t, "second message", m2["content"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apiResponse{
			StopReason: "end_turn",
			Content: []contentBlock{
				{Type: "text", Text: "got it"},
			},
		})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	result, err := client.Send(context.Background(), SendArgs{
		Messages: history,
	})
	require.NoError(t, err)
	assert.Equal(t, "got it", result)
}

func TestAPIClient_Send_ToolLoop(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if n == 1 {
			// First call: request tool use
			_ = json.NewEncoder(w).Encode(apiResponse{
				StopReason: "tool_use",
				Content: []contentBlock{
					{Type: "text", Text: "Let me call the tool."},
					{
						Type:  "tool_use",
						ID:    "call_1",
						Name:  "test_tool",
						Input: json.RawMessage(`{"key":"val"}`),
					},
				},
			})
		} else {
			// Second call: verify tool result was sent, return final text
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			msgs := req["messages"].([]any)
			// Last message should be the tool_result
			lastMsg := msgs[len(msgs)-1].(map[string]any)
			assert.Equal(t, "user", lastMsg["role"])
			content := lastMsg["content"].([]any)
			toolResult := content[0].(map[string]any)
			assert.Equal(t, "tool_result", toolResult["type"])
			assert.Equal(t, "call_1", toolResult["tool_use_id"])
			assert.Equal(t, "mock_result", toolResult["content"])

			_ = json.NewEncoder(w).Encode(apiResponse{
				StopReason: "end_turn",
				Content: []contentBlock{
					{Type: "text", Text: "Done! The result was: mock_result"},
				},
			})
		}
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	client.SetToolSchemas([]json.RawMessage{
		json.RawMessage(`{"name":"test_tool","description":"A test tool","input_schema":{"type":"object"}}`),
	})
	client.SetToolExecutor(func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		assert.Equal(t, "test_tool", name)
		assert.JSONEq(t, `{"key":"val"}`, string(input))
		return "mock_result", nil
	})

	result, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "use the tool"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "Done! The result was: mock_result", result)
	assert.Equal(t, int32(2), callCount.Load(), "expected exactly 2 API calls")
}

func TestAPIClient_Send_NoTools_StillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no tools field in request
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		_, hasTools := req["tools"]
		assert.False(t, hasTools, "request should not contain tools field")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apiResponse{
			StopReason: "end_turn",
			Content: []contentBlock{
				{Type: "text", Text: "No tools needed"},
			},
		})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	// No tools configured
	result, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "No tools needed", result)
}

func TestAPIClient_Send_ToolLoopMaxIterations(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Always return tool_use — never a final text response
		_ = json.NewEncoder(w).Encode(apiResponse{
			StopReason: "tool_use",
			Content: []contentBlock{
				{
					Type:  "tool_use",
					ID:    "call_loop",
					Name:  "infinite_tool",
					Input: json.RawMessage(`{}`),
				},
			},
		})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", "claude-test")
	client.SetToolSchemas([]json.RawMessage{
		json.RawMessage(`{"name":"infinite_tool","description":"Never stops","input_schema":{"type":"object"}}`),
	})
	client.SetToolExecutor(func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		return "ok", nil
	})

	_, err := client.Send(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "loop forever"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max tool iterations")
	assert.Equal(t, int32(maxToolIterations), callCount.Load(), "should have made exactly maxToolIterations API calls")
}
