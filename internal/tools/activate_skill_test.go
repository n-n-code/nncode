package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"nncode/internal/skills"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeToolTestSkill(t *testing.T, root string, dirName string, frontmatter string, body string) {
	t.Helper()

	dir := filepath.Join(root, ".agents", "skills", dirName)
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+frontmatter+"---\n"+body), 0644))
}

func TestActivateSkillToolSuccessAndDuplicate(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeToolTestSkill(t, root, "go", "name: go\ndescription: go skill\n", "# Go")
	reg := skills.Discover(skills.DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	tool := ActivateSkill(skills.NewActivator(reg))

	args, _ := json.Marshal(map[string]string{"name": "go"})
	result, err := tool.Execute(context.Background(), args)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "<skill_content>")
	assert.Contains(t, result.Content, "# Go")

	result, err = tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "already active")
	assert.NotContains(t, result.Content, "<skill_content>")
}

func TestActivateSkillToolRejectsUnknownAndHidden(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeToolTestSkill(t, root, "manual", "name: manual\ndescription: manual\ndisable-model-invocation: true\n", "# Manual")
	reg := skills.Discover(skills.DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	tool := ActivateSkill(skills.NewActivator(reg))

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"missing"}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "unknown skill")

	result, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"manual"}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "manual-only")
}

func TestActivateSkillToolSchemaUsesVisibleEnum(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	writeToolTestSkill(t, root, "go", "name: go\ndescription: go\n", "# Go")
	writeToolTestSkill(t, root, "manual", "name: manual\ndescription: manual\ndisable-model-invocation: true\n", "# Manual")
	reg := skills.Discover(skills.DiscoverOptions{CWD: root, HomeDir: t.TempDir()})

	tool := ActivateSkill(skills.NewActivator(reg))

	assert.Contains(t, tool.Parameters, `"enum":["go"]`)
	assert.NotContains(t, tool.Parameters, "manual")
}

func TestActivateSkillToolSchemaUsesCappedCatalog(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))

	for i := range 65 {
		name := fmt.Sprintf("skill-%02d", i)
		writeToolTestSkill(t, root, name, fmt.Sprintf("name: %s\ndescription: skill %d\n", name, i), "# Skill")
	}

	reg := skills.Discover(skills.DiscoverOptions{CWD: root, HomeDir: t.TempDir()})
	tool := ActivateSkill(skills.NewActivator(reg))

	assert.Contains(t, tool.Parameters, "skill-63")
	assert.NotContains(t, tool.Parameters, "skill-64")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"skill-64"}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "not in the model activation catalog")
}
