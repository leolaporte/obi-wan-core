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

	handler := FastmailCreateEventHandler(srv.URL, "testuser@fastmail.com", "testpassword", nil)

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

	handler := FastmailCreateEventHandler(srv.URL, "user@fastmail.com", "pass", nil)

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

// TestFastmailJMAP_AddsBearerPrefix verifies that JMAP handlers prepend
// "Bearer " to tokens that come in raw from the env (Fastmail API tokens
// don't carry the prefix). Without this, Fastmail returns 401.
func TestFastmailJMAP_AddsBearerPrefix(t *testing.T) {
	var authSeen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"methodResponses":[]}`))
	}))
	defer srv.Close()

	// Token comes in raw, as it would from FASTMAIL_API_TOKEN env var.
	handler := FastmailCreateContactHandler(srv.URL, "fmu1-rawtoken")
	raw, _ := json.Marshal(fastmailContactInput{Name: "Test"})
	_, err := handler(context.Background(), raw)

	require.NoError(t, err)
	require.Equal(t, "Bearer fmu1-rawtoken", authSeen)
}

// TestFastmailJMAP_DoesNotDoubleBearer verifies that a token already
// carrying a "Bearer " prefix isn't double-prefixed.
func TestFastmailJMAP_DoesNotDoubleBearer(t *testing.T) {
	var authSeen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"methodResponses":[]}`))
	}))
	defer srv.Close()

	handler := FastmailSearchContactsHandler(srv.URL, "Bearer fmu1-alreadyprefixed")
	raw, _ := json.Marshal(fastmailSearchInput{Query: "x"})
	_, err := handler(context.Background(), raw)

	require.NoError(t, err)
	require.Equal(t, "Bearer fmu1-alreadyprefixed", authSeen)
}

// TestFastmailCreateEvent_UsesDiscoveredCalendarPath verifies that when
// a calendar-path map is supplied, the display name supplied by Claude
// is translated into the Fastmail path identifier (e.g. "personal" →
// "Default") before the CalDAV PUT.
func TestFastmailCreateEvent_UsesDiscoveredCalendarPath(t *testing.T) {
	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	calendarPaths := map[string]string{"personal": "Default"}
	handler := FastmailCreateEventHandler(srv.URL, "u@fastmail.com", "p", calendarPaths)

	raw, _ := json.Marshal(fastmailCreateEventInput{
		Title: "x", Start: "2026-04-15T10:00:00", Duration: "PT1H", Calendar: "Personal",
	})
	_, err := handler(context.Background(), raw)
	require.NoError(t, err)
	require.Contains(t, requestedPath, "/dav/calendars/user/u@fastmail.com/Default/")
}

func TestDiscoverCalendars_ParsesPROPFIND(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/dav/calendars/user/u@fastmail.com/</D:href>
    <D:propstat><D:prop><D:displayname></D:displayname></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
  <D:response>
    <D:href>/dav/calendars/user/u@fastmail.com/Default/</D:href>
    <D:propstat><D:prop><D:displayname>Personal</D:displayname></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
  <D:response>
    <D:href>/dav/calendars/user/u@fastmail.com/abc123/</D:href>
    <D:propstat><D:prop><D:displayname>Work</D:displayname></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
</D:multistatus>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PROPFIND", r.Method)
		require.Equal(t, "1", r.Header.Get("Depth"))
		user, pass, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "u@fastmail.com", user)
		require.Equal(t, "pw", pass)
		w.WriteHeader(http.StatusMultiStatus)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := DiscoverCalendars(context.Background(), srv.URL, "u@fastmail.com", "pw")
	require.NoError(t, err)
	require.Equal(t, "Default", got["personal"])
	require.Equal(t, "abc123", got["work"])
	require.Len(t, got, 2)
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
