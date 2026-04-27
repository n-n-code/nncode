package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRead_Success(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := "hello world"
	require.NoError(t, os.WriteFile(testFile, []byte(testContent), 0644))

	args, _ := json.Marshal(map[string]string{"path": testFile})
	result, err := Read().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, testContent, result.Content)
}

func TestRead_FileNotFound(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"path": "/nonexistent/file.txt"})
	result, err := Read().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "Failed to read file")
}

func TestRead_InvalidArgs(t *testing.T) {
	args := json.RawMessage(`{"bad": "json` + "\x00")
	result, err := Read().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestWrite_CreateNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "new.txt")
	testContent := "new content"

	args, _ := json.Marshal(map[string]string{
		"path":    testFile,
		"content": testContent,
	})
	result, err := Write().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "Successfully wrote")

	data, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, string(data))
}

func TestWrite_CreateNestedDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "a", "b", "c", "file.txt")
	testContent := "nested"

	args, _ := json.Marshal(map[string]string{
		"path":    testFile,
		"content": testContent,
	})
	result, err := Write().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)

	data, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, string(data))
}

func TestWrite_InvalidArgs(t *testing.T) {
	args := json.RawMessage(`{`)
	result, err := Write().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestEdit_Success(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "edit.txt")
	original := "hello world foo"
	require.NoError(t, os.WriteFile(testFile, []byte(original), 0644))

	args, _ := json.Marshal(map[string]string{
		"path":       testFile,
		"old_string": "world",
		"new_string": "universe",
	})
	result, err := Edit().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)

	data, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, "hello universe foo", string(data))
}

func TestEdit_StringNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "edit.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("hello world"), 0644))

	args, _ := json.Marshal(map[string]string{
		"path":       testFile,
		"old_string": "nonexistent",
		"new_string": "replaced",
	})
	result, err := Edit().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "String not found")
}

func TestEdit_ExactMatchRequired(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "edit.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("hello  world"), 0644))

	args, _ := json.Marshal(map[string]string{
		"path":       testFile,
		"old_string": "hello world",
		"new_string": "hi there",
	})
	result, err := Edit().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestEdit_FileNotFound(t *testing.T) {
	args, _ := json.Marshal(map[string]string{
		"path":       "/nonexistent/file.txt",
		"old_string": "foo",
		"new_string": "bar",
	})
	result, err := Edit().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestBash_Success(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"command": "echo hello"})
	result, err := Bash().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "hello", result.Content)
}

func TestBash_FailingCommand(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"command": "exit 1"})
	result, err := Bash().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestBash_InvalidArgs(t *testing.T) {
	args := json.RawMessage(`{`)
	result, err := Bash().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestRead_LargeFileTruncation(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "large.txt")

	largeContent := make([]byte, 60000)
	for i := range largeContent {
		largeContent[i] = 'a'
	}

	require.NoError(t, os.WriteFile(testFile, largeContent, 0644))

	args, _ := json.Marshal(map[string]string{"path": testFile})
	result, err := Read().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Len(t, result.Content, 50000+len("\n... (truncated, file too large)"))
	assert.Contains(t, result.Content, "truncated")
}

func TestRead_CustomLimit(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "limited.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("abcdef"), 0644))

	args, _ := json.Marshal(map[string]string{"path": testFile})
	result, err := Read(Options{MaxReadBytes: 3}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "abc\n... (truncated, file too large)", result.Content)
}

func TestWrite_CustomLimit(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "too-large.txt")

	args, _ := json.Marshal(map[string]string{"path": testFile, "content": "abcdef"})
	result, err := Write(Options{MaxWriteBytes: 3}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "write limit")
}

func TestEdit_RejectsEmptyOldString(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "edit.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("hello"), 0644))

	args, _ := json.Marshal(map[string]string{
		"path":       testFile,
		"old_string": "",
		"new_string": "x",
	})
	result, err := Edit().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "old_string")
}

func TestTools_WorkspaceRootAllowsRelativePathInsideRoot(t *testing.T) {
	tmpDir := t.TempDir()
	opts := Options{RootDir: tmpDir}

	args, _ := json.Marshal(map[string]string{"path": "inside.txt", "content": "ok"})
	result, err := Write(opts).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)

	data, err := os.ReadFile(filepath.Join(tmpDir, "inside.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ok", string(data))
}

func TestTools_WorkspaceRootRejectsPathOutsideRoot(t *testing.T) {
	tmpDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")

	args, _ := json.Marshal(map[string]string{"path": outside, "content": "no"})
	result, err := Write(Options{RootDir: tmpDir}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "outside workspace root")
}

func TestBash_CustomOutputLimit(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"command": "printf abcdef"})
	result, err := Bash(Options{MaxBashOutputBytes: 3}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "abc\n... (truncated)", result.Content)
}

func TestBash_Timeout(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"command": "sleep 1"})
	result, err := Bash(Options{BashTimeout: 20 * time.Millisecond}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "timed out")
}

func TestBash_WorkspaceRootSetsWorkingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	args, _ := json.Marshal(map[string]string{"command": "pwd"})
	result, err := Bash(Options{RootDir: tmpDir}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, tmpDir, strings.TrimSpace(result.Content))
}

func TestPatch_UpdatesExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "main.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package main\n\nfunc main() {}\n"), 0644))

	patch := `--- a/main.go
+++ b/main.go
@@ -1,3 +1,5 @@
 package main
 
-func main() {}
+func main() {
+	println("hi")
+}
`
	args, _ := json.Marshal(map[string]string{"patch": patch})

	result, err := Patch(Options{RootDir: tmpDir}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 1, result.Metadata["files_changed"])

	data, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n", string(data))
}

func TestPatch_CreatesNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	patch := `--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
`
	args, _ := json.Marshal(map[string]string{"patch": patch})

	result, err := Patch(Options{RootDir: tmpDir}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)

	data, err := os.ReadFile(filepath.Join(tmpDir, "new.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\nworld\n", string(data))
}

func TestPatch_RejectsMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("actual\n"), 0644))

	patch := `--- a/file.txt
+++ b/file.txt
@@ -1,1 +1,1 @@
-expected
+new
`
	args, _ := json.Marshal(map[string]string{"patch": patch})

	result, err := Patch(Options{RootDir: tmpDir}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "mismatch")
}

func TestPatch_RejectsOutsideWorkspaceRoot(t *testing.T) {
	tmpDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	patch := `--- /dev/null
+++ ` + outside + `
@@ -0,0 +1,1 @@
+no
`
	args, _ := json.Marshal(map[string]string{"patch": patch})

	result, err := Patch(Options{RootDir: tmpDir}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "outside workspace root")
}

func TestGrep_ContentOutput(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package main\nfunc foo() {}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte("package main\nfunc bar() {}\n"), 0644))

	args, _ := json.Marshal(map[string]string{"pattern": "func .*\\(", "path": tmpDir, "output": "content"})
	result, err := Grep().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "a.go:2:func foo() {}")
	assert.Contains(t, result.Content, "b.go:2:func bar() {}")
}

func TestGrep_CountOutput(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("hello\nhello\n"), 0644))

	args, _ := json.Marshal(map[string]string{"pattern": "hello", "path": tmpDir, "output": "count"})
	result, err := Grep().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "2 matches", result.Content)
}

func TestGrep_FilesOutput(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package main\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("text\n"), 0644))

	args, _ := json.Marshal(map[string]string{"pattern": "package", "path": tmpDir, "output": "files"})
	result, err := Grep().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "a.go")
	assert.NotContains(t, result.Content, "b.txt")
}

func TestGrep_InvalidPattern(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"pattern": "[invalid", "path": "."})
	result, err := Grep().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "Invalid pattern")
}

func TestGrep_NoMatches(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package main\n"), 0644))

	args, _ := json.Marshal(map[string]string{"pattern": "nonexistent", "path": tmpDir})
	result, err := Grep().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "No matches found", result.Content)
}

func TestGrep_WorkspaceRootRestriction(t *testing.T) {
	tmpDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret\n"), 0644))

	args, _ := json.Marshal(map[string]string{"pattern": "secret", "path": outside})
	result, err := Grep(Options{RootDir: tmpDir}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "outside workspace root")
}

func TestFind_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "c.txt"), []byte("x"), 0644))

	args, _ := json.Marshal(map[string]string{"pattern": "*.go", "path": tmpDir})
	result, err := Find().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "a.go")
	assert.Contains(t, result.Content, "b.go")
	assert.NotContains(t, result.Content, "c.txt")
}

func TestFind_DefaultPattern(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("x"), 0644))

	args, _ := json.Marshal(map[string]string{"path": tmpDir})
	result, err := Find().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "a.go")
	assert.Contains(t, result.Content, "b.txt")
}

func TestFind_NoMatches(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("x"), 0644))

	args, _ := json.Marshal(map[string]string{"pattern": "*.go", "path": tmpDir})
	result, err := Find().Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "No files found", result.Content)
}

func TestFind_WorkspaceRootRestriction(t *testing.T) {
	tmpDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")

	args, _ := json.Marshal(map[string]string{"pattern": "*.txt", "path": outside})
	result, err := Find(Options{RootDir: tmpDir}).Execute(context.Background(), args)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "outside workspace root")
}
