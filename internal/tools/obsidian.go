package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// --- input types ---

type obsidianReadInput struct {
	Path string `json:"path"`
}

type obsidianWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type obsidianPatchInput struct {
	Path     string `json:"path"`
	Heading  string `json:"heading"`
	Content  string `json:"content"`
	Position string `json:"position"` // "append" | "replace"
}

// safePath resolves relPath under vaultRoot and rejects any path that escapes
// the vault via traversal sequences.
func safePath(vaultRoot, relPath string) (string, error) {
	abs, err := filepath.Abs(filepath.Join(vaultRoot, relPath))
	if err != nil {
		return "", fmt.Errorf("resolving path: %w", err)
	}
	root, err := filepath.Abs(vaultRoot)
	if err != nil {
		return "", fmt.Errorf("resolving vault root: %w", err)
	}
	// Ensure abs is root itself or a child of root.
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside vault", relPath)
	}
	return abs, nil
}

// countLeadingHashes returns the number of leading '#' characters in s.
func countLeadingHashes(s string) int {
	for i, ch := range s {
		if ch != '#' {
			return i
		}
	}
	return len(s)
}

// ObsidianReadHandler returns a HandlerFunc that reads a markdown file from
// the vault. A missing file is returned as a tool result, not a Go error.
func ObsidianReadHandler(vaultRoot string) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in obsidianReadInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		abs, err := safePath(vaultRoot, in.Path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Sprintf("File not found: %s", in.Path), nil
			}
			return "", fmt.Errorf("reading file: %w", err)
		}
		return string(data), nil
	}
}

// ObsidianWriteHandler returns a HandlerFunc that creates parent directories
// and writes content to a file in the vault.
func ObsidianWriteHandler(vaultRoot string) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in obsidianWriteInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		abs, err := safePath(vaultRoot, in.Path)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", fmt.Errorf("creating directories: %w", err)
		}
		if err := os.WriteFile(abs, []byte(in.Content), 0o644); err != nil {
			return "", fmt.Errorf("writing file: %w", err)
		}
		return fmt.Sprintf("Written: %s", in.Path), nil
	}
}

// ObsidianPatchHandler returns a HandlerFunc that finds a heading in a
// markdown file and either appends content before the next heading of equal
// or higher level ("append") or replaces the section content ("replace").
// A missing heading is returned as a tool result, not a Go error.
func ObsidianPatchHandler(vaultRoot string) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in obsidianPatchInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		abs, err := safePath(vaultRoot, in.Path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Sprintf("File not found: %s", in.Path), nil
			}
			return "", fmt.Errorf("reading file: %w", err)
		}

		lines := strings.Split(string(data), "\n")
		headingLevel := countLeadingHashes(strings.TrimSpace(in.Heading))

		// Find the target heading line.
		headingIdx := -1
		for i, line := range lines {
			if strings.TrimSpace(line) == strings.TrimSpace(in.Heading) {
				headingIdx = i
				break
			}
		}
		if headingIdx == -1 {
			return fmt.Sprintf("Heading not found: %s", in.Heading), nil
		}

		// Find the end of the section: next heading of same or higher level (fewer hashes), or EOF.
		sectionEnd := len(lines) // exclusive index of first line after the section
		for i := headingIdx + 1; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.HasPrefix(trimmed, "#") {
				level := countLeadingHashes(trimmed)
				if level <= headingLevel {
					sectionEnd = i
					break
				}
			}
		}

		// Build the new content line(s), preserving trailing newline behaviour.
		newLines := strings.Split(strings.TrimRight(in.Content, "\n"), "\n")

		var result []string
		switch in.Position {
		case "replace":
			// Keep heading, replace everything between heading+1 and sectionEnd.
			result = append(result, lines[:headingIdx+1]...)
			result = append(result, newLines...)
			result = append(result, lines[sectionEnd:]...)
		default: // "append"
			// Insert new lines before sectionEnd.
			result = append(result, lines[:sectionEnd]...)
			result = append(result, newLines...)
			result = append(result, lines[sectionEnd:]...)
		}

		out := strings.Join(result, "\n")
		if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
			return "", fmt.Errorf("writing file: %w", err)
		}
		return fmt.Sprintf("Patched %s under %s", in.Path, in.Heading), nil
	}
}

// RegisterObsidianTools registers the three Obsidian tools with the registry.
func RegisterObsidianTools(r *Registry, vaultRoot string) {
	r.Register(Tool{
		Name:        "obsidian_read_note",
		Description: "Read a markdown file from the Obsidian vault. Path is relative to the vault root, e.g. \"Daily Notes/2026/04/2026-04-13.md\".",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Relative path within the vault, e.g. \"Daily Notes/2026/04/2026-04-13.md\""
				}
			},
			"required": ["path"]
		}`),
		Handler: ObsidianReadHandler(vaultRoot),
	})

	r.Register(Tool{
		Name:        "obsidian_write_note",
		Description: "Write (create or overwrite) a markdown file in the Obsidian vault. Parent directories are created automatically. Path is relative to the vault root, e.g. \"AI/Research/topic.md\".",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Relative path within the vault, e.g. \"AI/Research/topic.md\""
				},
				"content": {
					"type": "string",
					"description": "Full markdown content to write to the file"
				}
			},
			"required": ["path", "content"]
		}`),
		Handler: ObsidianWriteHandler(vaultRoot),
	})

	r.Register(Tool{
		Name:        "obsidian_patch_note",
		Description: "Insert or replace content under a specific heading in a markdown file. Finds the heading, then either appends before the next section (position=append) or replaces the section body (position=replace). Path is relative to the vault root, e.g. \"Daily Notes/2026/04/2026-04-13.md\".",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Relative path within the vault, e.g. \"Daily Notes/2026/04/2026-04-13.md\""
				},
				"heading": {
					"type": "string",
					"description": "The exact heading line to find, e.g. \"#### Meals\""
				},
				"content": {
					"type": "string",
					"description": "Content to insert or use as replacement"
				},
				"position": {
					"type": "string",
					"enum": ["append", "replace"],
					"description": "\"append\" inserts content before the next heading; \"replace\" replaces the section body"
				}
			},
			"required": ["path", "heading", "content", "position"]
		}`),
		Handler: ObsidianPatchHandler(vaultRoot),
	})
}
