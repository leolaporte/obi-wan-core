// Package telegram provides the Telegram long-poll client for obi-wan-core.
package telegram

import "strings"

// MaxChunk is Telegram's message length limit: 4096 bytes. Pure ASCII
// fits one-to-one. Multi-byte UTF-8 may be cut mid-rune on a hard
// split, but each chunk remains valid UTF-8 (the cut falls between
// complete runes on whichever side), so Telegram accepts both.
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
