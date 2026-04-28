package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nncode/internal/config"
	"nncode/internal/skills"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildToolsIncludesPatch(t *testing.T) {
	tools := buildTools(config.ToolConfig{}, nil)

	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}

	assert.Contains(t, names, "read")
	assert.Contains(t, names, "write")
	assert.Contains(t, names, "edit")
	assert.Contains(t, names, "patch")
	assert.Contains(t, names, "bash")
}

func TestBuildToolsHonorsDisabledTools(t *testing.T) {
	tools := buildTools(config.ToolConfig{Disabled: []string{"bash", "patch"}}, nil)

	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}

	assert.NotContains(t, names, "bash")
	assert.NotContains(t, names, "patch")
	assert.Contains(t, names, "read")
}

func TestBuildToolsAddsActivateSkillOnlyWhenVisibleSkillsExist(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(tmpDir, ".git"), 0755))
	skillsDir := filepath.Join(tmpDir, ".agents", "skills", "go")
	require.NoError(t, os.MkdirAll(skillsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte("---\nname: go\ndescription: go skill\n---\n# Go"), 0644))
	reg := skills.Discover(skills.DiscoverOptions{CWD: tmpDir, HomeDir: t.TempDir()})

	tools := buildTools(config.ToolConfig{}, skills.NewActivator(reg))

	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}

	assert.Contains(t, names, "activate_skill")
}

func TestBuildToolsOmitsActivateSkillWhenOnlyHiddenSkillsExist(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(tmpDir, ".git"), 0755))
	skillsDir := filepath.Join(tmpDir, ".agents", "skills", "manual")
	require.NoError(t, os.MkdirAll(skillsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte("---\nname: manual\ndescription: manual\ndisable-model-invocation: true\n---\n# Manual"), 0644))
	reg := skills.Discover(skills.DiscoverOptions{CWD: tmpDir, HomeDir: t.TempDir()})

	tools := buildTools(config.ToolConfig{}, skills.NewActivator(reg))

	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}

	assert.NotContains(t, names, "activate_skill")
}

func TestRunWithArgsDoctor(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Chdir(tmpDir)
	require.NoError(t, os.MkdirAll(".nncode", 0755))

	configJSON := `{
		"default_model": "local",
		"models": {
			"local": {
				"api_type": "openai-completions",
				"provider": "local",
				"base_url": "http://127.0.0.1:8033/v1"
			}
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(".nncode", "config.json"), []byte(configJSON), 0644))

	err := runWithArgs([]string{"doctor", "-model", "local"})

	require.NoError(t, err)
}

func TestRunWithArgsDoctor_AutoVendsUnknownModel(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Chdir(tmpDir)

	// No custom config — default config has llama3 with a BaseURL,
	// so auto-vending should create an entry for the unknown model name.
	err := runWithArgs([]string{"doctor", "-model", "my-custom-model"})

	require.NoError(t, err)
}

func TestRunWithArgsLoopCheck(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(tmpDir)
	writeMainTestLoop(t, tmpDir, "check-me")

	err := runWithArgs([]string{"-loop-check", "check-me"})

	require.NoError(t, err)
}

func TestRunWithArgsLoopSubcommands(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(tmpDir)
	writeMainTestLoop(t, tmpDir, "check-me")

	require.NoError(t, runWithArgs([]string{"loop", "list"}))
	require.NoError(t, runWithArgs([]string{"loop", "check", "check-me"}))
}

func TestBuildModelUsesConfiguredModelID(t *testing.T) {
	model := buildModel("alias", config.Model{ID: "provider-id", BaseURL: "http://127.0.0.1:8033/v1"})

	assert.Equal(t, "provider-id", model.ID)
	assert.Equal(t, "http://127.0.0.1:8033/v1", model.BaseURL)
}

func TestComposeSystemPrompt_InjectsCWD(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	result := composeSystemPrompt("base prompt")

	assert.Contains(t, result, "base prompt")
	assert.Contains(t, result, cwd)
	assert.Contains(t, result, "Prefer relative paths")
	assert.Contains(t, result, "\n\nThe current working directory is ")
}

func TestComposeSystemPrompt_TrimsTrailingNewlines(t *testing.T) {
	result := composeSystemPrompt("base\n\n")

	assert.True(t, strings.HasPrefix(result, "base\n\nThe current working directory is "), "should trim trailing newlines then add exactly two before cwd")
}

func writeMainTestLoop(t *testing.T, root string, name string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".nncode", "loops"), 0755))
	loopJSON := `{
		"schema_version": 1,
		"name": "` + name + `",
		"nodes": [
			{"id":"entry","type":"entry_prompt","content":"entry"},
			{"id":"prompt","type":"prompt","content":"prompt"},
			{"id":"exit","type":"exit_criteria","content":"done"}
		]
	}`
	require.NoError(t, os.WriteFile(filepath.Join(root, ".nncode", "loops", name+".json"), []byte(loopJSON), 0644))
}
