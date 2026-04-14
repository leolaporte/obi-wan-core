package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// --- input types ---

type fastmailCreateEventInput struct {
	Title    string `json:"title"`
	Start    string `json:"start"`    // ISO 8601 e.g. "2026-04-15T14:00:00"
	Duration string `json:"duration"` // ISO 8601 e.g. "PT1H"
	Location string `json:"location"`
	Calendar string `json:"calendar"` // defaults to "Personal"
}

type fastmailContactInput struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	Company string `json:"company"`
	Notes   string `json:"notes"`
}

type fastmailSearchInput struct {
	Query string `json:"query"`
}

// --- UUID helper ---

// newUUID generates a random UUID v4 string.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// --- iCal helper ---

// buildVCalendar builds a minimal VCALENDAR/VEVENT string with CRLF line endings.
func buildVCalendar(uid, summary, location string, start time.Time, duration string) string {
	dtstart := start.UTC().Format("20060102T150405Z")
	lines := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//obi-wan-core//EN",
		"BEGIN:VEVENT",
		"UID:" + uid,
		"DTSTART:" + dtstart,
		"DURATION:" + duration,
		"SUMMARY:" + summary,
		"LOCATION:" + location,
		"END:VEVENT",
		"END:VCALENDAR",
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// truncateStr truncates s to at most n characters (for error messages).
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- JMAP helper ---

// doJMAP posts a JMAP request and returns successMsg on HTTP 200, or an error string.
func doJMAP(ctx context.Context, jmapURL, token string, body any, successMsg string) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encoding JMAP request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, jmapURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("building JMAP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("error: JMAP request failed: %v", err), nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("error: JMAP returned %d: %s", resp.StatusCode, truncateStr(string(respBody), 200)), nil
	}

	if successMsg != "" {
		return successMsg, nil
	}
	return string(respBody), nil
}

// --- Handlers ---

// FastmailCreateEventHandler returns a HandlerFunc that creates a calendar event via CalDAV PUT.
func FastmailCreateEventHandler(caldavURL, user, password string) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in fastmailCreateEventInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if in.Calendar == "" {
			in.Calendar = "Personal"
		}

		start, err := time.Parse("2006-01-02T15:04:05", in.Start)
		if err != nil {
			return fmt.Sprintf("error: invalid start time %q: %v", in.Start, err), nil
		}

		uid := newUUID() + "@obi-wan-core"
		ical := buildVCalendar(uid, in.Title, in.Location, start, in.Duration)

		url := fmt.Sprintf("%s/dav/calendars/user/%s/%s/%s.ics", caldavURL, user, in.Calendar, uid)

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(ical))
		if err != nil {
			return "", fmt.Errorf("building CalDAV request: %w", err)
		}
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		req.SetBasicAuth(user, password)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Sprintf("error: CalDAV request failed: %v", err), nil
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent {
			return fmt.Sprintf("Event created: %s on %s", in.Title, start.Format("Jan 2, 2006 at 3:04 PM")), nil
		}

		body, _ := io.ReadAll(resp.Body)
		return fmt.Sprintf("error: CalDAV returned %d: %s", resp.StatusCode, truncateStr(string(body), 200)), nil
	}
}

// FastmailCreateContactHandler returns a HandlerFunc that creates a contact via JMAP.
func FastmailCreateContactHandler(jmapURL, token string) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in fastmailContactInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		// Build the card — split name into firstName/lastName for JMAP
		nameParts := strings.SplitN(in.Name, " ", 2)
		firstName := nameParts[0]
		lastName := ""
		if len(nameParts) > 1 {
			lastName = nameParts[1]
		}

		card := map[string]any{
			"@type":     "Card",
			"version":   "1.0",
			"fullName":  in.Name,
			"firstName": firstName,
			"lastName":  lastName,
		}
		if in.Email != "" {
			card["emails"] = []map[string]any{{"value": in.Email}}
		}
		if in.Phone != "" {
			card["phones"] = []map[string]any{{"value": in.Phone}}
		}
		if in.Company != "" {
			card["company"] = in.Company
		}
		if in.Notes != "" {
			card["notes"] = in.Notes
		}

		jmapReq := map[string]any{
			"using": []string{
				"urn:ietf:params:jmap:contacts",
				"https://www.fastmail.com/dev/contacts",
			},
			"methodCalls": []any{
				[]any{
					"ContactCard/set",
					map[string]any{
						"accountId": "primary",
						"create": map[string]any{
							"c1": card,
						},
					},
					"0",
				},
			},
		}

		return doJMAP(ctx, jmapURL, token, jmapReq, fmt.Sprintf("Contact created: %s", in.Name))
	}
}

// FastmailSearchContactsHandler returns a HandlerFunc that searches contacts via JMAP.
// Returns the raw JMAP response body for Claude to format.
func FastmailSearchContactsHandler(jmapURL, token string) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in fastmailSearchInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		jmapReq := map[string]any{
			"using": []string{
				"urn:ietf:params:jmap:contacts",
				"https://www.fastmail.com/dev/contacts",
			},
			"methodCalls": []any{
				[]any{
					"ContactCard/query",
					map[string]any{
						"accountId": "primary",
						"filter": map[string]any{
							"text": in.Query,
						},
						"limit": 10,
					},
					"0",
				},
				[]any{
					"ContactCard/get",
					map[string]any{
						"accountId": "primary",
						"#ids": map[string]any{
							"resultOf": "0",
							"name":     "ContactCard/query",
							"path":     "/ids",
						},
					},
					"1",
				},
			},
		}

		// successMsg="" means return raw body
		return doJMAP(ctx, jmapURL, token, jmapReq, "")
	}
}

// RegisterFastmailTools registers all three Fastmail tools with the registry.
func RegisterFastmailTools(r *Registry, caldavURL, user, password, jmapURL, token string) {
	r.Register(Tool{
		Name:        "fastmail_create_event",
		Description: "Create a calendar event in Fastmail via CalDAV. Provide a title, ISO 8601 start time (e.g. \"2026-04-15T14:00:00\"), ISO 8601 duration (e.g. \"PT1H\"), optional location, and optional calendar name (defaults to \"Personal\").",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"title": {
					"type": "string",
					"description": "Event title/summary"
				},
				"start": {
					"type": "string",
					"description": "ISO 8601 start time, e.g. \"2026-04-15T14:00:00\""
				},
				"duration": {
					"type": "string",
					"description": "ISO 8601 duration, e.g. \"PT1H\" for one hour, \"PT30M\" for 30 minutes"
				},
				"location": {
					"type": "string",
					"description": "Optional event location"
				},
				"calendar": {
					"type": "string",
					"description": "Calendar name, defaults to \"Personal\""
				}
			},
			"required": ["title", "start", "duration"]
		}`),
		Handler: FastmailCreateEventHandler(caldavURL, user, password),
	})

	r.Register(Tool{
		Name:        "fastmail_create_contact",
		Description: "Create a new contact in Fastmail via JMAP. Provide name (required) and optional email, phone, company, and notes.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Full name of the contact"
				},
				"email": {
					"type": "string",
					"description": "Email address"
				},
				"phone": {
					"type": "string",
					"description": "Phone number"
				},
				"company": {
					"type": "string",
					"description": "Company or organization"
				},
				"notes": {
					"type": "string",
					"description": "Free-form notes"
				}
			},
			"required": ["name"]
		}`),
		Handler: FastmailCreateContactHandler(jmapURL, token),
	})

	r.Register(Tool{
		Name:        "fastmail_search_contacts",
		Description: "Search contacts in Fastmail via JMAP. Returns up to 10 matching contacts. Claude will format the raw JMAP response.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {
					"type": "string",
					"description": "Search query — matches against name, email, phone, company, notes"
				}
			},
			"required": ["query"]
		}`),
		Handler: FastmailSearchContactsHandler(jmapURL, token),
	})
}
