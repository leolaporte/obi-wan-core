package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// ClaudeRunner wraps the `claude -p` binary. Each Run call spawns a
// subprocess with --permission-mode auto and parses its JSON output.
type ClaudeRunner struct {
	binary   string
	model    string
	extraEnv []string
}

// NewClaudeRunner constructs a runner.
//
// binary: path to the claude executable (e.g. /home/leo/.local/bin/claude)
// model: "sonnet" | "opus" | "haiku"
func NewClaudeRunner(binary, model string) *ClaudeRunner {
	return &ClaudeRunner{binary: binary, model: model}
}

// NewClaudeRunnerWithEnv creates a runner that injects extra environment
// variables into the subprocess. Used by FallbackRunner to point at alternate
// API endpoints.
func NewClaudeRunnerWithEnv(binary, model string, extraEnv []string) *ClaudeRunner {
	return &ClaudeRunner{binary: binary, model: model, extraEnv: extraEnv}
}

// RunArgs bundles per-call parameters.
type RunArgs struct {
	Message      string
	Channel      string // "telegram" | "watch" | "r1"
	SessionID    string
	IsNewSession bool   // true → --session-id, false → --resume
	SystemPrompt string // optional --append-system-prompt content
}

// RunResult is the parsed result of one claude -p invocation.
type RunResult struct {
	Text         string
	SessionError bool // true if the failure looked like a stale session
}

// Run invokes claude -p once. Returns a Go error only for failures that
// prevent the subprocess from running (e.g. binary not found). Normal
// claude-side failures (non-zero exit, session errors) are surfaced via
// RunResult fields, not Go errors.
func (r *ClaudeRunner) Run(ctx context.Context, args RunArgs) (*RunResult, error) {
	sessionFlag := "--resume"
	if args.IsNewSession {
		sessionFlag = "--session-id"
	}

	// Inject current date/time and message source so resumed sessions
	// don't reuse stale dates and Claude can tailor responses by channel.
	now := time.Now().In(mustLoadLA())
	dated := fmt.Sprintf("[Current time: %s | Source: %s]\n\n%s",
		now.Format("Monday, January 2, 2006 3:04 PM"), args.Channel, args.Message)

	cmdArgs := []string{
		"-p",
		"--model", r.model,
		sessionFlag, args.SessionID,
		"--permission-mode", "auto",
		"--output-format", "json",
		"--no-chrome",
	}
	if args.SystemPrompt != "" {
		cmdArgs = append(cmdArgs, "--append-system-prompt", args.SystemPrompt)
	}
	cmdArgs = append(cmdArgs, dated)

	cmd := exec.CommandContext(ctx, r.binary, cmdArgs...)
	env := append(cmd.Environ(), "CI=1")
	env = append(env, r.extraEnv...)
	cmd.Env = env

	stdout, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}

		if isSessionError(stderr) {
			return &RunResult{SessionError: true}, nil
		}

		slog.Error("claude exited non-zero", "stderr", truncate(stderr, 500))
		return &RunResult{
			Text: fmt.Sprintf("Error running claude: %s", truncate(stderr, 200)),
		}, nil
	}

	var parsed struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(stdout, &parsed); err != nil {
		text := strings.TrimSpace(string(stdout))
		if text == "" {
			text = "(no output)"
		}
		return &RunResult{Text: text}, nil
	}

	text := strings.TrimSpace(parsed.Result)
	if text == "" {
		text = "(no output)"
	}
	return &RunResult{Text: text}, nil
}

func isSessionError(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "session") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "enoent")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// mustLoadLA returns America/Los_Angeles, falling back to UTC if tzdata
// is missing.
func mustLoadLA() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.UTC
	}
	return loc
}
