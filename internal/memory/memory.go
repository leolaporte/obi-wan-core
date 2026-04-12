// Package memory loads per-channel memory.md files.
//
// The memory system mirrors the existing convention at
// ~/.claude/channels/<channel>/memory.md, a manually-curated running
// context file updated after each session. The loader reads the current
// content at turn time so fresh edits are visible without restart.
package memory

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// MaxSize is the maximum memory file size we'll load. Larger files
// suggest a bug or runaway growth and should surface as an error.
const MaxSize = 64 * 1024

// Loader reads per-channel memory files from a root directory.
// Root is typically ~/.claude/channels.
type Loader struct {
	root string
}

// NewLoader constructs a Loader rooted at root.
func NewLoader(root string) *Loader {
	return &Loader{root: root}
}

// Load returns the content of <root>/<channel>/memory.md. If the file
// doesn't exist, returns ("", nil). If it exists but is larger than
// MaxSize, returns an error.
func (l *Loader) Load(channel string) (string, error) {
	path := filepath.Join(l.root, channel, "memory.md")
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stat memory file: %w", err)
	}
	if info.Size() > MaxSize {
		return "", fmt.Errorf("memory file too large: %d bytes (max %d)", info.Size(), MaxSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read memory file: %w", err)
	}
	return string(data), nil
}
