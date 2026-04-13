package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFastmailCreateEvent_Success(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	handler := FastmailCreateEventHandler(srv.URL, "testuser@fastmail.com", "testpassword")

	input := fastmailCreateEventInput{
		Title:    "Dentist",
		Start:    "2026-04-15T14:00:00",
		Duration: "PT1H",
		Location: "123 Main St",
		Calendar: "Personal",
	}
	raw, _ := json.Marshal(input)

	result, err := handler(context.Background(), raw)

	require.NoError(t, err)
	require.NotNil(t, capturedReq)

	// Verify Basic auth
	user, pass, ok := capturedReq.BasicAuth()
	require.True(t, ok, "expected Basic auth header")
	require.Equal(t, "testuser@fastmail.com", user)
	require.Equal(t, "testpassword", pass)

	// Verify iCal body contains expected fields
	body := string(capturedBody)
	require.Contains(t, body, "SUMMARY:Dentist")
	require.Contains(t, body, "DTSTART:")

	// Verify result message
	require.Contains(t, result, "Dentist")
	require.Contains(t, strings.ToLower(result), "created")
}

func TestFastmailCreateEvent_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	handler := FastmailCreateEventHandler(srv.URL, "user@fastmail.com", "pass")

	input := fastmailCreateEventInput{
		Title:    "Test Event",
		Start:    "2026-04-15T10:00:00",
		Duration: "PT30M",
		Calendar: "Personal",
	}
	raw, _ := json.Marshal(input)

	result, err := handler(context.Background(), raw)

	// Must be a tool result (string), not a Go error
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(result), "error")
}

func TestFastmailCreateContact_Success(t *testing.T) {
	var capturedReq *http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"methodResponses":[["ContactCard/set",{"created":{"c1":{"id":"abc123"}}},""]]}`))
	}))
	defer srv.Close()

	handler := FastmailCreateContactHandler(srv.URL, "Bearer testtoken123")

	input := fastmailContactInput{
		Name:    "Alice Example",
		Email:   "alice@example.com",
		Phone:   "555-1234",
		Company: "Example Corp",
		Notes:   "Met at conference",
	}
	raw, _ := json.Marshal(input)

	result, err := handler(context.Background(), raw)

	require.NoError(t, err)
	require.NotNil(t, capturedReq)

	// Verify Bearer token
	authHeader := capturedReq.Header.Get("Authorization")
	require.Contains(t, authHeader, "Bearer")
	require.Contains(t, authHeader, "testtoken123")

	// Verify result contains contact name
	require.Contains(t, result, "Alice Example")
}

func TestFastmailSearchContacts_ReturnsBody(t *testing.T) {
	responseBody := `{"methodResponses":[["ContactCard/get",{"list":[{"id":"1","fullName":"Jeff Goldblum","emails":[{"value":"jeff@example.com"}]}]},""]]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(responseBody))
	}))
	defer srv.Close()

	handler := FastmailSearchContactsHandler(srv.URL, "Bearer searchtoken")

	input := fastmailSearchInput{Query: "Jeff"}
	raw, _ := json.Marshal(input)

	result, err := handler(context.Background(), raw)

	require.NoError(t, err)
	require.Contains(t, result, "Jeff")
}
