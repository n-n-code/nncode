package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nncode/internal/llm"
)

func TestExtractChanges(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "write", Args: []byte(`{"path":"foo.go","content":"package main"}`)},
				{ID: "c2", Name: "edit", Args: []byte(`{"path":"bar.go","old_string":"x","new_string":"y"}`)},
				{ID: "c3", Name: "patch", Args: []byte(`{"path":"baz.go","diff":"---"}`)},
				{ID: "c4", Name: "bash", Args: []byte(`{"command":"go test ./..."}`)},
				{ID: "c5", Name: "read", Args: []byte(`{"path":"README.md"}`)},
			},
		},
	}

	changes := ExtractChanges(msgs)
	require.Len(t, changes, 4)

	assert.Equal(t, "write", changes[0].Tool)
	assert.Equal(t, "foo.go", changes[0].File)

	assert.Equal(t, "edit", changes[1].Tool)
	assert.Equal(t, "bar.go", changes[1].File)
	assert.Contains(t, changes[1].Detail, "replaced 1 chars with 1 chars")

	assert.Equal(t, "patch", changes[2].Tool)
	assert.Equal(t, "baz.go", changes[2].File)

	assert.Equal(t, "bash", changes[3].Tool)
	assert.Equal(t, "go test ./...", changes[3].Detail)
}

func TestExtractChangesEmpty(t *testing.T) {
	changes := ExtractChanges([]llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	})
	assert.Empty(t, changes)
}

func TestFormatChanges(t *testing.T) {
	changes := []Change{
		{Tool: "write", File: "main.go", Detail: "wrote file"},
		{Tool: "bash", Detail: "go build"},
	}

	out := FormatChanges(changes)
	assert.Contains(t, out, "Changes (2):")
	assert.Contains(t, out, "**write** `main.go`")
	assert.Contains(t, out, "**bash** — go build")
}

func TestFormatChangesEmpty(t *testing.T) {
	out := FormatChanges(nil)
	assert.Equal(t, "No file changes recorded in this session.", out)
}

func TestExtractChanges_AnnotatesFailures(t *testing.T) {
	msgs := []llm.Message{
		{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "write", Args: []byte(`{"path":"ok.go","content":"x"}`)},
				{ID: "c2", Name: "write", Args: []byte(`{"path":"fail.go","content":"y"}`)},
			},
		},
		{Role: llm.RoleTool, Content: "done", ToolCallID: "c1", ToolName: "write"},
		{Role: llm.RoleTool, Content: "error", ToolCallID: "c2", ToolName: "write", IsError: true},
	}

	changes := ExtractChanges(msgs)
	require.Len(t, changes, 2)

	assert.Equal(t, "write", changes[0].Tool)
	assert.Equal(t, "ok.go", changes[0].File)
	assert.NotContains(t, changes[0].Detail, "failed")

	assert.Equal(t, "write", changes[1].Tool)
	assert.Equal(t, "fail.go", changes[1].File)
	assert.Contains(t, changes[1].Detail, "failed")
}
