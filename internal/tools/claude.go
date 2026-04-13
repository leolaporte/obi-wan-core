package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
)

type spawnClaudeInput struct {
	Task  string `json:"task"`
	Skill string `json:"skill,omitempty"`
}

func SpawnClaudeCodeHandler(claudeBinary string) HandlerFunc {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		var args spawnClaudeInput
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("parse input: %w", err)
		}

		prompt := args.Task
		if args.Skill != "" {
			prompt = fmt.Sprintf("/%s %s", args.Skill, args.Task)
		}

		cmd := exec.Command(claudeBinary, "-p", "--model", "opus", prompt)
		if err := cmd.Start(); err != nil {
			return fmt.Sprintf("Failed to spawn: %s", err), nil
		}

		go func() {
			err := cmd.Wait()
			if err != nil {
				slog.Error("spawned claude process failed", "task", args.Task, "skill", args.Skill, "error", err)
			} else {
				slog.Info("spawned claude process completed", "task", args.Task, "skill", args.Skill)
			}
		}()

		return fmt.Sprintf("Task spawned: %s", args.Task), nil
	}
}

func RegisterClaudeTools(r *Registry, claudeBinary string) {
	r.Register(Tool{
		Name:        "spawn_claude_code",
		Description: "Spawn a background Claude Code process for heavy tasks like research, showprep, council, wiki ingest, or code review. The process runs asynchronously with full Claude Code capabilities. Results are written to Obsidian or other destinations by the invoked skill.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task": {"type": "string", "description": "Description of the task to perform"},
				"skill": {"type": "string", "description": "Optional skill to invoke, e.g. showprep, council, research"}
			},
			"required": ["task"]
		}`),
		Handler: SpawnClaudeCodeHandler(claudeBinary),
	})
}
