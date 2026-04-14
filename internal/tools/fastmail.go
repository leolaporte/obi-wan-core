package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
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
// Ensures the Authorization header carries a "Bearer " prefix; Fastmail API tokens
// come from the environment raw (e.g. "fmu1-..."), without the prefix.
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
	authHeader := token
	if !strings.HasPrefix(authHeader, "Bearer ") {
		authHeader = "Bearer " + authHeader
	}
	req.Header.Set("Authorization", authHeader)

	slog.Info("fastmail jmap request", "url", jmapURL)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("fastmail jmap transport error", "err", err)
		return fmt.Sprintf("error: JMAP request failed: %v", err), nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	slog.Info("fastmail jmap response", "status", resp.StatusCode, "bytes", len(respBody))
	if resp.StatusCode != http.StatusOK {
		slog.Warn("fastmail jmap non-200", "status", resp.StatusCode, "body", truncateStr(string(respBody), 200))
		return fmt.Sprintf("error: JMAP returned %d: %s", resp.StatusCode, truncateStr(string(respBody), 200)), nil
	}

	if successMsg != "" {
		return successMsg, nil
	}
	return string(respBody), nil
}

// --- Handlers ---

// resolveCalendarPath maps the user-facing calendar name to the path
// segment Fastmail expects. Fastmail's CalDAV uses internal identifiers
// (e.g. "Default", or a hex GUID) — not display names — in the URL path.
// If discovery ran at startup and found a matching display name
// (case-insensitive), we use the discovered path. Otherwise we pass
// through whatever Claude supplied, which lets users explicitly name a
// known path and also preserves the old behavior.
func resolveCalendarPath(requested string, discovered map[string]string) string {
	if discovered != nil {
		if path, ok := discovered[strings.ToLower(requested)]; ok {
			return path
		}
	}
	return requested
}

// FastmailCreateEventHandler returns a HandlerFunc that creates a calendar event via CalDAV PUT.
// calendarPaths is an optional display-name → path map (keys lowercased)
// discovered via PROPFIND at startup. Pass nil to disable lookup.
func FastmailCreateEventHandler(caldavURL, user, password string, calendarPaths map[string]string) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in fastmailCreateEventInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if in.Calendar == "" {
			in.Calendar = "Default"
		}

		start, err := time.Parse("2006-01-02T15:04:05", in.Start)
		if err != nil {
			return fmt.Sprintf("error: invalid start time %q: %v", in.Start, err), nil
		}

		uid := newUUID() + "@obi-wan-core"
		ical := buildVCalendar(uid, in.Title, in.Location, start, in.Duration)

		calPath := resolveCalendarPath(in.Calendar, calendarPaths)
		url := fmt.Sprintf("%s/dav/calendars/user/%s/%s/%s.ics", caldavURL, user, calPath, uid)

		slog.Info("fastmail caldav PUT", "calendar_requested", in.Calendar, "calendar_path", calPath, "title", in.Title, "start", in.Start)

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(ical))
		if err != nil {
			return "", fmt.Errorf("building CalDAV request: %w", err)
		}
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		req.SetBasicAuth(user, password)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Warn("fastmail caldav transport error", "err", err)
			return fmt.Sprintf("error: CalDAV request failed: %v", err), nil
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent {
			slog.Info("fastmail caldav created", "status", resp.StatusCode, "title", in.Title)
			return fmt.Sprintf("Event created: %s on %s", in.Title, start.Format("Jan 2, 2006 at 3:04 PM")), nil
		}

		body, _ := io.ReadAll(resp.Body)
		slog.Warn("fastmail caldav non-2xx", "status", resp.StatusCode, "calendar_path", calPath, "body", truncateStr(string(body), 200))
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
// calendarPaths is an optional display-name → path map (lowercased keys)
// discovered via DiscoverCalendars; pass nil to skip the lookup layer.
func RegisterFastmailTools(r *Registry, caldavURL, user, password, jmapURL, token string, calendarPaths map[string]string) {
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
		Handler: FastmailCreateEventHandler(caldavURL, user, password, calendarPaths),
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

// --- CalDAV calendar discovery ---

// caldavPROPFIND represents the subset of the CalDAV PROPFIND multistatus
// response we need: for each <response>, we want the <href> (path) and
// the <displayname> property. Fastmail's CalDAV collection listing under
// /dav/calendars/user/<user>/ returns one response per calendar.
type caldavPROPFIND struct {
	XMLName   xml.Name            `xml:"multistatus"`
	Responses []caldavPROPFINDRsp `xml:"response"`
}

type caldavPROPFINDRsp struct {
	Href     string `xml:"href"`
	Propstat []struct {
		Prop struct {
			DisplayName string `xml:"displayname"`
		} `xml:"prop"`
		Status string `xml:"status"`
	} `xml:"propstat"`
}

const caldavPropfindBody = `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:">
  <D:prop><D:displayname/></D:prop>
</D:propfind>`

// DiscoverCalendars issues a depth-1 PROPFIND against Fastmail's calendar
// collection for the given user and returns a map of lowercased display
// name to the path segment (e.g. "personal" → "Default" or a hex GUID).
// Call this once at startup; pass the result to RegisterFastmailTools so
// FastmailCreateEventHandler can translate Claude's calendar names into
// paths Fastmail actually recognizes.
func DiscoverCalendars(ctx context.Context, caldavURL, user, password string) (map[string]string, error) {
	baseURL := fmt.Sprintf("%s/dav/calendars/user/%s/", caldavURL, user)
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", baseURL, strings.NewReader(caldavPropfindBody))
	if err != nil {
		return nil, fmt.Errorf("build PROPFIND: %w", err)
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.SetBasicAuth(user, password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PROPFIND transport: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// CalDAV returns 207 Multi-Status on success.
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("PROPFIND returned %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}

	var parsed caldavPROPFIND
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse PROPFIND: %w", err)
	}

	out := make(map[string]string)
	userPrefix := fmt.Sprintf("/dav/calendars/user/%s/", user)
	for _, r := range parsed.Responses {
		// Skip the parent collection itself (href == userPrefix) and any
		// response without a displayname property populated (e.g. the
		// outbox/inbox collections that Fastmail doesn't name).
		href := r.Href
		if !strings.HasPrefix(href, userPrefix) || href == userPrefix {
			continue
		}
		pathSegment := strings.TrimSuffix(strings.TrimPrefix(href, userPrefix), "/")
		if pathSegment == "" {
			continue
		}
		var name string
		for _, ps := range r.Propstat {
			if ps.Prop.DisplayName != "" {
				name = ps.Prop.DisplayName
				break
			}
		}
		if name == "" {
			continue
		}
		out[strings.ToLower(name)] = pathSegment
	}
	return out, nil
}
