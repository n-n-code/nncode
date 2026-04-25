package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"nncode/internal/config"
	"nncode/internal/llm"
	"nncode/internal/skills"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockClient struct {
	events []llm.StreamEvent
	err    error
}

func (m *mockClient) Stream(ctx context.Context, req llm.Request) (<-chan llm.StreamEvent, error) {
	if m.err != nil {
		return nil, m.err
	}

	ch := make(chan llm.StreamEvent, len(m.events))

	go func() {
		defer close(ch)

		for _, ev := range m.events {
			ch <- ev
		}
	}()

	return ch, nil
}

func testConfig() *config.Config {
	return &config.Config{
		DefaultModel: "local",
		Models: map[string]config.Model{
			"local": {APIType: config.APITypeOpenAICompletions, Provider: "local", BaseURL: "http://127.0.0.1:8033/v1"},
			"cloud": {APIType: config.APITypeOpenAICompletions, Provider: "openai"},
		},
		Tools: config.ToolConfig{
			MaxReadBytes:       10,
			MaxWriteBytes:      20,
			MaxBashOutputBytes: 30,
			BashTimeoutSeconds: 4,
		},
	}
}

func find(checks []Check, name string) Check {
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}

	return Check{}
}

func TestRun_OKWithoutLive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	checks := Run(context.Background(), Options{Config: testConfig()})

	assert.Equal(t, StatusOK, find(checks, "config").Status)
	assert.Equal(t, StatusOK, find(checks, "model").Status)
	assert.Equal(t, StatusOK, find(checks, "tools").Status)
	assert.Equal(t, StatusOK, find(checks, "skills").Status)
	assert.Equal(t, StatusOK, find(checks, "sessions").Status)
	assert.Equal(t, StatusWarn, find(checks, "live request").Status)
	assert.False(t, HasFailures(checks))
}

func TestRun_WarnsOnSkillDiagnostics(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	skillDir := filepath.Join(root, ".agents", "skills", "bad")
	require.NoError(t, os.MkdirAll(skillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: bad\n---\n# Bad"), 0644))
	reg := skills.Discover(skills.DiscoverOptions{CWD: root, HomeDir: t.TempDir()})

	checks := Run(context.Background(), Options{Config: testConfig(), Skills: reg})

	skillCheck := find(checks, "skills")
	assert.Equal(t, StatusWarn, skillCheck.Status)
	assert.Contains(t, skillCheck.Detail, "diagnostics")
	assert.False(t, HasFailures(checks), "skill diagnostics should warn without failing doctor")
}

func TestRun_WarnsOnSkillCatalogTruncationWithoutPromptComposition(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))

	for i := range 65 {
		name := fmt.Sprintf("skill-%02d", i)
		skillDir := filepath.Join(root, ".agents", "skills", name)
		require.NoError(t, os.MkdirAll(skillDir, 0755))

		content := fmt.Sprintf("---\nname: %s\ndescription: skill %d\n---\n# Skill", name, i)
		require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644))
	}

	reg := skills.Discover(skills.DiscoverOptions{CWD: root, HomeDir: t.TempDir()})

	checks := Run(context.Background(), Options{Config: testConfig(), Skills: reg})

	skillCheck := find(checks, "skills")
	assert.Equal(t, StatusWarn, skillCheck.Status)
	assert.Contains(t, skillCheck.Detail, "omitted from prompt/tool activation catalog")
	assert.False(t, HasFailures(checks))
}

func TestRun_FailsInvalidConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := testConfig()
	cfg.DefaultModel = "missing"

	checks := Run(context.Background(), Options{Config: cfg})

	assert.Equal(t, StatusFail, find(checks, "config").Status)
	assert.True(t, HasFailures(checks))
}

func TestRun_FailsMissingCloudAPIKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	checks := Run(context.Background(), Options{Config: testConfig(), ModelName: "cloud"})

	assert.Equal(t, StatusFail, find(checks, "api key").Status)
	assert.True(t, HasFailures(checks))
}

func TestRun_LiveSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	client := &mockClient{events: []llm.StreamEvent{
		{Text: "ok"},
		{Done: &llm.Done{StopReason: "stop"}},
	}}

	checks := Run(context.Background(), Options{Config: testConfig(), Live: true, Client: client})

	assert.Equal(t, StatusOK, find(checks, "live request").Status)
	assert.False(t, HasFailures(checks))
}

func TestRun_LiveFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	client := &mockClient{err: errors.New("connection refused")}

	checks := Run(context.Background(), Options{Config: testConfig(), Live: true, Client: client})

	assert.Equal(t, StatusFail, find(checks, "live request").Status)
	assert.True(t, HasFailures(checks))
}

func TestWrite(t *testing.T) {
	var out bytes.Buffer

	Write(&out, []Check{{Name: "config", Status: StatusOK, Detail: "valid"}})

	require.Contains(t, out.String(), "[ok]")
	require.Contains(t, out.String(), "config")
}
