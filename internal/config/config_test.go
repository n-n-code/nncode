package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "gpt-4o", cfg.DefaultModel)
	assert.Contains(t, cfg.Models, "gpt-4o")
	assert.Contains(t, cfg.Models, "llama3")
	assert.Equal(t, APITypeOpenAICompletions, cfg.Models["gpt-4o"].APIType)
	assert.Equal(t, APITypeOpenAICompletions, cfg.Models["llama3"].APIType)
	assert.Equal(t, "http://127.0.0.1:8033/v1", cfg.Models["llama3"].BaseURL)
	assert.Equal(t, 50000, cfg.Tools.MaxReadBytes)
	assert.Equal(t, 1000000, cfg.Tools.MaxWriteBytes)
	assert.Equal(t, 10000, cfg.Tools.MaxBashOutputBytes)
	assert.Equal(t, 120, cfg.Tools.BashTimeoutSeconds)
	assert.NoError(t, cfg.Validate())
}

func TestLoad_NoConfigFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Temporarily change the home directory to a temp dir with no config
	t.Setenv("HOME", tmpDir)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", cfg.DefaultModel)
}

func TestLoad_WithConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfgDir := filepath.Join(tmpDir, ".nncode")
	require.NoError(t, os.MkdirAll(cfgDir, 0755))

	cfgData := `{
		"default_model": "gpt-4o-mini",
		"models": {
			"gpt-4o-mini": { "api_type": "openai-completions", "provider": "openai" }
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfgData), 0644))

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o-mini", cfg.DefaultModel)
	assert.Equal(t, APITypeOpenAICompletions, cfg.Models["gpt-4o-mini"].APIType)
}

func TestLoad_PartialConfigPreservesDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfgDir := filepath.Join(tmpDir, ".nncode")
	require.NoError(t, os.MkdirAll(cfgDir, 0755))

	// Only override default_model; baked-in model entries should survive.
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"default_model": "llama3"}`), 0644))

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "llama3", cfg.DefaultModel)
	assert.Contains(t, cfg.Models, "llama3", "default llama3 entry must survive a partial overlay")
	assert.NotEmpty(t, cfg.Models["llama3"].BaseURL)
}

func TestSaveGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg := &Config{
		DefaultModel: "gpt-4o",
		Models: map[string]Model{
			"gpt-4o": {APIType: APITypeOpenAICompletions, Provider: "openai"},
		},
	}

	err := SaveGlobal(cfg)
	require.NoError(t, err)

	configPath := filepath.Join(tmpDir, ".nncode", "config.json")
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var loaded Config

	err = json.Unmarshal(data, &loaded)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", loaded.DefaultModel)
	assert.Equal(t, APITypeOpenAICompletions, loaded.Models["gpt-4o"].APIType)
}

func TestResolveModel_Found(t *testing.T) {
	cfg := defaultConfig()
	m, ok := cfg.ResolveModel("gpt-4o")
	require.True(t, ok)
	assert.Equal(t, APITypeOpenAICompletions, m.APIType)
}

func TestResolveModel_NotFound(t *testing.T) {
	cfg := defaultConfig()
	_, ok := cfg.ResolveModel("unknown-model")
	assert.False(t, ok)
}

func TestResolveModel_EmptyConfig(t *testing.T) {
	cfg := &Config{Models: map[string]Model{}}
	_, ok := cfg.ResolveModel("anything")
	assert.False(t, ok)
}

func TestAutoVendModel_AddsEntryFromTemplate(t *testing.T) {
	cfg := defaultConfig()
	vended := cfg.AutoVendModel("my-local-model")

	require.True(t, vended)
	m, ok := cfg.ResolveModel("my-local-model")
	require.True(t, ok)
	assert.Equal(t, APITypeOpenAICompletions, m.APIType)
	assert.Equal(t, "http://127.0.0.1:8033/v1", m.BaseURL)
	assert.Equal(t, "local", m.Provider)
	assert.Empty(t, m.ID, "ID must be empty so RequestID falls back to the map key")
	assert.NoError(t, cfg.Validate())
}

func TestAutoVendModel_NoOpWhenModelExists(t *testing.T) {
	cfg := defaultConfig()
	vended := cfg.AutoVendModel("llama3")

	assert.False(t, vended)
	assert.Len(t, cfg.Models, 4, "no new entries should be added")
}

func TestAutoVendModel_NoOpWhenNoTemplate(t *testing.T) {
	cfg := &Config{
		DefaultModel: "gpt-4o",
		Models: map[string]Model{
			"gpt-4o": {APIType: APITypeOpenAICompletions, Provider: "openai"},
		},
	}
	vended := cfg.AutoVendModel("my-local-model")

	assert.False(t, vended)
	assert.Len(t, cfg.Models, 1)
}

func TestAutoVendModel_NoOpWhenMultipleTemplates(t *testing.T) {
	cfg := &Config{
		DefaultModel: "gpt-4o",
		Models: map[string]Model{
			"gpt-4o":  {APIType: APITypeOpenAICompletions, Provider: "openai"},
			"local-a": {APIType: APITypeOpenAICompletions, Provider: "local", BaseURL: "http://127.0.0.1:8033/v1"},
			"local-b": {APIType: APITypeOpenAICompletions, Provider: "local", BaseURL: "http://localhost:1234/v1"},
		},
	}
	vended := cfg.AutoVendModel("my-local-model")

	assert.False(t, vended)
	assert.Len(t, cfg.Models, 3)
}

func TestMerge_Overlay(t *testing.T) {
	base := &Config{
		DefaultModel: "gpt-4o",
		Models: map[string]Model{
			"gpt-4o": {APIType: APITypeOpenAICompletions, Provider: "openai"},
			"shared": {APIType: APITypeOpenAICompletions, Provider: "openai"},
		},
	}
	overlay := &Config{
		DefaultModel: "llama3",
		Models: map[string]Model{
			"llama3": {APIType: APITypeOpenAICompletions, Provider: "local", BaseURL: "http://127.0.0.1:8033/v1"},
			"shared": {APIType: APITypeOpenAICompletions, Provider: "lmstudio", BaseURL: "http://localhost:1234/v1"},
		},
	}

	base.Merge(overlay)

	assert.Equal(t, "llama3", base.DefaultModel)
	assert.Equal(t, "http://127.0.0.1:8033/v1", base.Models["llama3"].BaseURL)
	assert.Equal(t, "lmstudio", base.Models["shared"].Provider, "overlay should replace the shared entry")
	assert.Equal(t, "openai", base.Models["gpt-4o"].Provider, "untouched entries survive")
}

func TestMerge_NilOverlay(t *testing.T) {
	cfg := defaultConfig()
	cfg.Merge(nil)
	assert.Equal(t, "gpt-4o", cfg.DefaultModel)
}

func TestMerge_EmptyDefaultModelKeeps(t *testing.T) {
	base := &Config{DefaultModel: "gpt-4o", Models: map[string]Model{"gpt-4o": {Provider: "openai"}}}
	overlay := &Config{Models: map[string]Model{"llama3": {Provider: "local"}}}

	base.Merge(overlay)

	assert.Equal(t, "gpt-4o", base.DefaultModel, "empty overlay DefaultModel must not clobber base")
	assert.Contains(t, base.Models, "llama3")
	assert.Contains(t, base.Models, "gpt-4o")
}

func TestMerge_ToolConfigOverlay(t *testing.T) {
	base := defaultConfig()
	overlay := &Config{Tools: ToolConfig{
		Disabled:           []string{"bash"},
		WorkspaceRoot:      "/tmp/workspace",
		MaxReadBytes:       123,
		MaxWriteBytes:      456,
		MaxBashOutputBytes: 789,
		BashTimeoutSeconds: 3,
	}}

	base.Merge(overlay)

	assert.Equal(t, []string{"bash"}, base.Tools.Disabled)
	assert.Equal(t, "/tmp/workspace", base.Tools.WorkspaceRoot)
	assert.Equal(t, 123, base.Tools.MaxReadBytes)
	assert.Equal(t, 456, base.Tools.MaxWriteBytes)
	assert.Equal(t, 789, base.Tools.MaxBashOutputBytes)
	assert.Equal(t, 3, base.Tools.BashTimeoutSeconds)
}

func TestValidate_DefaultModelMustExist(t *testing.T) {
	cfg := defaultConfig()
	cfg.DefaultModel = "missing"

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "default_model")
}

func TestModelValidate_RejectsMissingProvider(t *testing.T) {
	m := Model{APIType: APITypeOpenAICompletions}
	err := m.Validate("gpt-test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider")
}

func TestModelValidate_RejectsLocalProviderWithoutBaseURL(t *testing.T) {
	m := Model{APIType: APITypeOpenAICompletions, Provider: "local"}
	err := m.Validate("local")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
}

func TestModelValidate_RejectsInvalidBaseURL(t *testing.T) {
	m := Model{APIType: APITypeOpenAICompletions, Provider: "local", BaseURL: "127.0.0.1:8033/v1"}
	err := m.Validate("local")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
}

func TestModelValidate_AllowsEmptyAPIType(t *testing.T) {
	m := Model{Provider: "openai"}
	assert.NoError(t, m.Validate("gpt-test"))
}

func TestModelValidate_AllowsOpenAICompletions(t *testing.T) {
	m := Model{APIType: APITypeOpenAICompletions, Provider: "openai"}
	assert.NoError(t, m.Validate("gpt-test"))
}

func TestModelRequestID(t *testing.T) {
	assert.Equal(t, "alias", Model{}.RequestID("alias"))
	assert.Equal(t, "provider-id", Model{ID: "provider-id"}.RequestID("alias"))
}

func TestModelValidate_RejectsUnsupportedAPIType(t *testing.T) {
	m := Model{APIType: "openai-responses", Provider: "openai"}
	err := m.Validate("gpt-test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported api_type")
	assert.Contains(t, err.Error(), "gpt-test")
}

func TestToolConfigValidate_RejectsUnknownDisabledTool(t *testing.T) {
	err := (&ToolConfig{Disabled: []string{"shell"}}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool")
}

func TestToolConfigValidate_RejectsDuplicateDisabledTool(t *testing.T) {
	err := (&ToolConfig{Disabled: []string{"bash", "bash"}}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestToolConfigValidate_RejectsNegativeLimits(t *testing.T) {
	err := (&ToolConfig{MaxReadBytes: -1}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_read_bytes")
}

func TestToolConfigIsDisabled(t *testing.T) {
	cfg := ToolConfig{Disabled: []string{"bash"}}
	assert.True(t, cfg.IsDisabled("bash"))
	assert.False(t, cfg.IsDisabled("read"))
}
