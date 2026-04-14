package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// helper: write a file into a temp vault, returning vault root
func tempVault(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeVaultFile(t *testing.T, vaultRoot, relPath, content string) {
	t.Helper()
	full := filepath.Join(vaultRoot, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// --- obsidian_read ---

func TestObsidianRead_Success(t *testing.T) {
	vault := tempVault(t)
	writeVaultFile(t, vault, "Daily Notes/2026/04/2026-04-13.md", "# Hello\nWorld\n")

	handler := ObsidianReadHandler(vault)
	input, _ := json.Marshal(obsidianReadInput{Path: "Daily Notes/2026/04/2026-04-13.md"})

	result, err := handler(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, result, "Hello")
	require.Contains(t, result, "World")
}

func TestObsidianRead_NotFound(t *testing.T) {
	vault := tempVault(t)

	handler := ObsidianReadHandler(vault)
	input, _ := json.Marshal(obsidianReadInput{Path: "missing.md"})

	result, err := handler(context.Background(), input)
	require.NoError(t, err) // not a Go error
	require.Contains(t, result, "not found")
}

func TestObsidianRead_PathTraversal(t *testing.T) {
	vault := tempVault(t)

	handler := ObsidianReadHandler(vault)
	input, _ := json.Marshal(obsidianReadInput{Path: "../../etc/passwd"})

	_, err := handler(context.Background(), input)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside vault")
}

// --- obsidian_write ---

func TestObsidianWrite_CreatesFileAndDirs(t *testing.T) {
	vault := tempVault(t)

	handler := ObsidianWriteHandler(vault)
	input, _ := json.Marshal(obsidianWriteInput{
		Path:    "new/sub/note.md",
		Content: "# New Note\nCreated by test\n",
	})

	result, err := handler(context.Background(), input)
	require.NoError(t, err)
	require.NotEmpty(t, result)

	full := filepath.Join(vault, "new/sub/note.md")
	data, err := os.ReadFile(full)
	require.NoError(t, err)
	require.Contains(t, string(data), "New Note")
}

func TestObsidianWrite_PathTraversal(t *testing.T) {
	vault := tempVault(t)

	handler := ObsidianWriteHandler(vault)
	input, _ := json.Marshal(obsidianWriteInput{
		Path:    "../../evil.md",
		Content: "bad",
	})

	_, err := handler(context.Background(), input)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside vault")
}

// --- obsidian_patch ---

const mealsNote = `# Daily Log

#### Meals
Breakfast: oatmeal

#### Exercise
Running 5k

#### Sleep
8 hours
`

func TestObsidianPatch_AppendUnderHeading(t *testing.T) {
	vault := tempVault(t)
	writeVaultFile(t, vault, "log.md", mealsNote)

	handler := ObsidianPatchHandler(vault)
	input, _ := json.Marshal(obsidianPatchInput{
		Path:     "log.md",
		Heading:  "#### Meals",
		Content:  "Lunch: salad",
		Position: "append",
	})

	result, err := handler(context.Background(), input)
	require.NoError(t, err)
	require.NotEmpty(t, result)

	data, err := os.ReadFile(filepath.Join(vault, "log.md"))
	require.NoError(t, err)
	content := string(data)

	// New content should appear before Exercise heading
	mealsIdx := indexOf(content, "Lunch: salad")
	exerciseIdx := indexOf(content, "#### Exercise")
	require.Greater(t, mealsIdx, -1, "new content not found")
	require.Less(t, mealsIdx, exerciseIdx, "new content should be before Exercise section")
}

func TestObsidianPatch_ReplaceUnderHeading(t *testing.T) {
	vault := tempVault(t)
	writeVaultFile(t, vault, "log.md", mealsNote)

	handler := ObsidianPatchHandler(vault)
	input, _ := json.Marshal(obsidianPatchInput{
		Path:     "log.md",
		Heading:  "#### Meals",
		Content:  "Lunch: salad",
		Position: "replace",
	})

	result, err := handler(context.Background(), input)
	require.NoError(t, err)
	require.NotEmpty(t, result)

	data, err := os.ReadFile(filepath.Join(vault, "log.md"))
	require.NoError(t, err)
	content := string(data)

	require.Contains(t, content, "Lunch: salad")
	require.NotContains(t, content, "Breakfast: oatmeal")
}

func TestObsidianPatch_HeadingNotFound(t *testing.T) {
	vault := tempVault(t)
	writeVaultFile(t, vault, "log.md", mealsNote)

	handler := ObsidianPatchHandler(vault)
	input, _ := json.Marshal(obsidianPatchInput{
		Path:     "log.md",
		Heading:  "#### Shopping",
		Content:  "Milk",
		Position: "append",
	})

	result, err := handler(context.Background(), input)
	require.NoError(t, err) // not a Go error
	require.Contains(t, result, "not found")
}

// indexOf returns the byte index of substr in s, or -1 if not present.
func indexOf(s, substr string) int {
	idx := -1
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			idx = i
			break
		}
	}
	return idx
}
