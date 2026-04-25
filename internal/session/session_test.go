package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nncode/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_SessionHasID(t *testing.T) {
	s := New()
	assert.NotEmpty(t, s.ID)
	assert.Empty(t, s.FilePath)
	assert.Empty(t, s.Messages)
}

func TestAddMessage(t *testing.T) {
	s := New()
	msg := llm.Message{Role: llm.RoleUser, Content: "hello"}
	s.AddMessage(msg)
	assert.Len(t, s.Messages, 1)
	assert.Equal(t, "hello", s.Messages[0].Content)
	assert.NotZero(t, s.Messages[0].Timestamp)
}

func TestSaveAndLoad_Roundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	s := New()
	s.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	s.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "hi there"})

	err := s.Save(tmpDir)
	require.NoError(t, err)

	loaded, err := Load(s.FilePath)
	require.NoError(t, err)
	assert.Equal(t, s.ID, loaded.ID)
	assert.Len(t, loaded.Messages, 2)
	assert.Equal(t, "hello", loaded.Messages[0].Content)
	assert.Equal(t, "hi there", loaded.Messages[1].Content)
}

func TestSaveAndLoad_EmptySession(t *testing.T) {
	tmpDir := t.TempDir()
	s := New()

	err := s.Save(tmpDir)
	require.NoError(t, err)

	loaded, err := Load(s.FilePath)
	require.NoError(t, err)
	assert.Empty(t, loaded.Messages)
}

func TestSave_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "subdir", "sessions")

	s := New()
	err := s.Save(sessionDir)
	require.NoError(t, err)

	_, err = os.Stat(s.FilePath)
	assert.NoError(t, err)
}

func TestLoad_NonExistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/session.jsonl")
	assert.Error(t, err)
}

func TestList_NoSessions(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	files, err := List()
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestList_WithSessions(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sessionsDir := filepath.Join(tmpDir, ".nncode", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(sessionsDir, "123.jsonl"), []byte(`{"role":"user","content":"hi"}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(sessionsDir, "456.jsonl"), []byte(`{"role":"user","content":"hello"}`), 0644))

	files, err := List()
	require.NoError(t, err)
	assert.Len(t, files, 2)
	joined := strings.Join(files, " ")
	assert.Contains(t, joined, "123.jsonl")
	assert.Contains(t, joined, "456.jsonl")
}

func TestList_SkipsNonJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sessionsDir := filepath.Join(tmpDir, ".nncode", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(sessionsDir, "123.txt"), []byte("not a session"), 0644))

	files, err := List()
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestDefaultDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir, err := DefaultDir()

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpDir, ".nncode", "sessions"), dir)
}

func TestResolve_ID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	path, err := Resolve("123")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpDir, ".nncode", "sessions", "123.jsonl"), path)
}

func TestResolve_FileName(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	path, err := Resolve("123.jsonl")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpDir, ".nncode", "sessions", "123.jsonl"), path)
}

func TestResolve_Path(t *testing.T) {
	path, err := Resolve(filepath.Join("some", "session.jsonl"))

	require.NoError(t, err)
	assert.Equal(t, filepath.Join("some", "session.jsonl"), path)
}

func TestResolve_Empty(t *testing.T) {
	_, err := Resolve(" ")
	assert.Error(t, err)
}
