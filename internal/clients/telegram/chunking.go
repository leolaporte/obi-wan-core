// Package telegram provides the Telegram long-poll client for obi-wan-core.
package telegram

import "strings"

// MaxChunk is Telegram's message length limit in UTF-16 code units.
// 4096 characters is a safe under-approximation for pure ASCII.
const MaxChunk = 4096

// Chunk splits s into pieces no longer than MaxChunk, preferring
// paragraph boundaries, then line boundaries, then hard splits.
// An empty string returns a nil slice.
func Chunk(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	remaining := s
	for len(remaining) > MaxChunk {
		split := strings.LastIndex(remaining[:MaxChunk], "\n\n")
		if split < MaxChunk/2 {
			split = strings.LastIndex(remaining[:MaxChunk], "\n")
		}
		if split < MaxChunk/2 {
			split = MaxChunk
		}
		out = append(out, remaining[:split])
		remaining = strings.TrimLeft(remaining[split:], "\n")
	}
	if remaining != "" {
		out = append(out, remaining)
	}
	return out
}
