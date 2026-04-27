package projectctx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGather_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	assert.Empty(t, Gather(dir))
}

func TestGather_GoMod(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.22\n"), 0o644))

	ctx := Gather(dir)
	assert.Contains(t, ctx, "example.com/foo")
	assert.Contains(t, ctx, "go 1.22")
	assert.Contains(t, ctx, "<project_context>")
	assert.Contains(t, ctx, "</project_context>")
}

func TestGather_PackageJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"my-app","description":"A test app"}`), 0o644))

	ctx := Gather(dir)
	assert.Contains(t, ctx, "my-app")
	assert.Contains(t, ctx, "A test app")
}

func TestGather_CargoToml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"my-crate\"\nversion = \"0.1.0\"\n"), 0o644))

	ctx := Gather(dir)
	assert.Contains(t, ctx, "my-crate")
}

func TestGather_PyProject(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"my-lib\"\n"), 0o644))

	ctx := Gather(dir)
	assert.Contains(t, ctx, "my-lib")
}

func TestGather_Makefile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\techo build\ntest:\n\techo test\n"), 0o644))

	ctx := Gather(dir)
	assert.Contains(t, ctx, "Makefile")
	assert.Contains(t, ctx, "build")
	assert.Contains(t, ctx, "test")
}

func TestGather_Readme(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# My Project\n"), 0o644))

	ctx := Gather(dir)
	assert.Contains(t, ctx, "My Project")
}

func TestGather_ReadmeBadgeSkipped(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("[![Build](https://example.com)]\n# Real Title\n"), 0o644))

	ctx := Gather(dir)
	assert.Contains(t, ctx, "- README.md")
	assert.NotContains(t, ctx, "Real Title")
}

func TestGather_GitRepo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))

	ctx := Gather(dir)
	assert.Contains(t, ctx, ".git repository")
}

func TestAppendToPrompt(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module foo\n"), 0o644))

	result := AppendToPrompt("base prompt", dir)
	assert.Contains(t, result, "base prompt")
	assert.Contains(t, result, "foo")
}

func TestAppendToPrompt_NoContext(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "base", AppendToPrompt("base", dir))
}

func TestGather_Truncation(t *testing.T) {
	dir := t.TempDir()
	// Create many files to exceed the byte limit.
	for i := range 200 {
		name := filepath.Join(dir, filepath.Base(dir)+"_"+string(rune('a'+i%26))+".txt")
		require.NoError(t, os.WriteFile(name, []byte("x"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# X\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n"), 0o644))

	ctx := Gather(dir)
	assert.LessOrEqual(t, len(ctx), maxContextBytes+50, "context should be roughly capped")
}
