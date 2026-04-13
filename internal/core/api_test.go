package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		var req apiRequest
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "claude-test", req.Model)
		assert.Equal(t, "You are helpful.", req.System)
		assert.Equal(t, 4096, req.MaxTokens)
		require.Len(t, req.Messages, 1)
		assert.Equal(t, "user", req.Messages[0].Role)
		assert.Equal(t, "Hello", req.Messages[0].Content)

		// Return a valid response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apiResponse{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{
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
		var req apiRequest
		require.NoError(t, json.Unmarshal(body, &req))

		// Should use the override model, not the client default
		assert.Equal(t, "claude-override", req.Model)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apiResponse{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{
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
		var req apiRequest
		require.NoError(t, json.Unmarshal(body, &req))

		// All history messages must arrive intact
		require.Len(t, req.Messages, 3)
		assert.Equal(t, "user", req.Messages[0].Role)
		assert.Equal(t, "first message", req.Messages[0].Content)
		assert.Equal(t, "assistant", req.Messages[1].Role)
		assert.Equal(t, "first reply", req.Messages[1].Content)
		assert.Equal(t, "user", req.Messages[2].Role)
		assert.Equal(t, "second message", req.Messages[2].Content)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(apiResponse{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{
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
