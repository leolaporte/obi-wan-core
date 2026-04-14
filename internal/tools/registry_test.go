package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistry_Execute_CallsHandler(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name:        "test_echo",
		Description: "echoes input",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "echo: " + string(input), nil
		},
	})

	result, err := r.Execute(context.Background(), "test_echo", json.RawMessage(`"hello"`))
	require.NoError(t, err)
	require.Equal(t, `echo: "hello"`, result)
}

func TestRegistry_Execute_UnknownToolReturnsError(t *testing.T) {
	r := NewRegistry()

	_, err := r.Execute(context.Background(), "nonexistent", json.RawMessage(`{}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent")
}

func TestRegistry_Schemas_ReturnsAllRegistered(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{
		Name:        "tool_a",
		Description: "first tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{}}}`),
		Handler:     func(ctx context.Context, input json.RawMessage) (string, error) { return "", nil },
	})
	r.Register(Tool{
		Name:        "tool_b",
		Description: "second tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"y":{}}}`),
		Handler:     func(ctx context.Context, input json.RawMessage) (string, error) { return "", nil },
	})

	schemas := r.Schemas()
	require.Len(t, schemas, 2)
	require.Equal(t, "tool_a", schemas[0].Name)
	require.Equal(t, "tool_b", schemas[1].Name)
	require.Equal(t, "first tool", schemas[0].Description)
	require.Equal(t, "second tool", schemas[1].Description)
}
