package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// HandlerFunc is the function signature for tool execution.
type HandlerFunc func(ctx context.Context, input json.RawMessage) (string, error)

// Tool defines a single tool with its API schema and local handler.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Handler     HandlerFunc     `json:"-"`
}

// ToolSchema is the API-facing schema (no handler) sent in the request.
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Registry maps tool names to handlers and provides schemas for the API.
type Registry struct {
	tools []Tool
	index map[string]int
}

func NewRegistry() *Registry {
	return &Registry{index: make(map[string]int)}
}

func (r *Registry) Register(t Tool) {
	if _, exists := r.index[t.Name]; exists {
		panic(fmt.Sprintf("duplicate tool: %s", t.Name))
	}
	r.index[t.Name] = len(r.tools)
	r.tools = append(r.tools, t)
}

func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	idx, ok := r.index[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return r.tools[idx].Handler(ctx, input)
}

func (r *Registry) Schemas() []ToolSchema {
	schemas := make([]ToolSchema, len(r.tools))
	for i, t := range r.tools {
		schemas[i] = ToolSchema{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return schemas
}
