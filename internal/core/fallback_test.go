package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func mockAPI(t *testing.T, text string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			w.Write([]byte(`{"error":{"type":"api_error","message":"fail"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
		})
	}))
}

func TestFallbackRunner_PrimarySuccess(t *testing.T) {
	primary := mockAPI(t, "primary reply", http.StatusOK)
	defer primary.Close()
	fallback := mockAPI(t, "fallback reply", http.StatusOK)
	defer fallback.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "model"),
		[]FallbackTier{
			{Client: NewAPIClient(fallback.URL, "key", "glm"), Label: "GLM"},
		},
	)

	text, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "primary reply", text, "should return primary result without prefix")
}

func TestFallbackRunner_PrimaryFailsFallsBack(t *testing.T) {
	primary := mockAPI(t, "", http.StatusInternalServerError)
	defer primary.Close()
	fallback := mockAPI(t, "fallback reply", http.StatusOK)
	defer fallback.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "model"),
		[]FallbackTier{
			{Client: NewAPIClient(fallback.URL, "key", "glm"), Label: "GLM"},
		},
	)

	text, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "[GLM] fallback reply", text, "should prefix fallback result")
}

func TestFallbackRunner_AllFail(t *testing.T) {
	primary := mockAPI(t, "", http.StatusInternalServerError)
	defer primary.Close()
	fallback := mockAPI(t, "", http.StatusInternalServerError)
	defer fallback.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "model"),
		[]FallbackTier{
			{Client: NewAPIClient(fallback.URL, "key", "glm"), Label: "GLM"},
		},
	)

	_, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "[GLM]", "error should contain fallback tier label")
}

func TestFallbackRunner_NoFallbacksReturnsError(t *testing.T) {
	primary := mockAPI(t, "", http.StatusInternalServerError)
	defer primary.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "model"),
		nil,
	)

	_, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.Error(t, err, "should return primary error when no fallbacks")
}

func TestFallbackRunner_ThreeTierChain(t *testing.T) {
	primary := mockAPI(t, "", http.StatusInternalServerError)
	defer primary.Close()
	glm := mockAPI(t, "", http.StatusInternalServerError)
	defer glm.Close()
	ollama := mockAPI(t, "local reply", http.StatusOK)
	defer ollama.Close()

	fr := NewFallbackRunner(
		NewAPIClient(primary.URL, "key", "model"),
		[]FallbackTier{
			{Client: NewAPIClient(glm.URL, "key", "glm"), Label: "GLM"},
			{Client: NewAPIClient(ollama.URL, "key", "ollama"), Label: "Ollama"},
		},
	)

	text, err := fr.Run(context.Background(), SendArgs{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "[Ollama] local reply", text, "should use second fallback and prefix")
}
