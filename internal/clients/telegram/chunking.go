// Package telegram provides the Telegram long-poll client for obi-wan-core.
package telegram

import (
	"strings"
	"unicode/utf8"
)

// MaxChunk is Telegram's message length limit: 4096 bytes. Pure ASCII
// fits one-to-one. Multi-byte UTF-8 stays valid because hard splits
// back up to the nearest rune boundary via runeSafeSplit.
const MaxChunk = 4096

// Chunk splits s into pieces no longer than MaxChunk, preferring
// paragraph boundaries, then line boundaries, then hard splits that
// back up to the nearest rune boundary so each chunk is valid UTF-8.
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
			split = runeSafeSplit(remaining, MaxChunk)
		}
		out = append(out, remaining[:split])
		remaining = strings.TrimLeft(remaining[split:], "\n")
	}
	if remaining != "" {
		out = append(out, remaining)
	}
	return out
}

// runeSafeSplit returns a byte index at or below max that lands on a
// rune boundary, so both remaining[:index] and remaining[index:] are
// valid UTF-8. It walks backward from max until utf8.RuneStart reports
// a rune start; since UTF-8 runes are at most 4 bytes, the walk
// terminates within 3 steps for any valid input. Callers must pass a
// valid UTF-8 string — on invalid input the fallback return may
// reintroduce a byte-unsafe split.
func runeSafeSplit(remaining string, max int) int {
	for i := max; i > 0; i-- {
		if utf8.RuneStart(remaining[i]) {
			return i
		}
	}
	return max
}
