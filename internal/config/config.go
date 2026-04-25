package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const APITypeOpenAICompletions = "openai-completions"

var knownToolNames = []string{"bash", "edit", "patch", "read", "write"}

// Config holds the application configuration.
type Config struct {
	DefaultModel string           `json:"default_model"`
	Models       map[string]Model `json:"models"`
	Tools        ToolConfig       `json:"tools,omitempty"`
}

// Model holds configuration for a single model.
type Model struct {
	ID        string `json:"id,omitempty"`
	APIType   string `json:"api_type"` // "openai-completions" or empty for the default
	Provider  string `json:"provider"`
	BaseURL   string `json:"base_url,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// ToolConfig controls built-in tool availability and basic resource limits.
type ToolConfig struct {
	Disabled           []string `json:"disabled,omitempty"`
	WorkspaceRoot      string   `json:"workspace_root,omitempty"`
	MaxReadBytes       int      `json:"max_read_bytes,omitempty"`
	MaxWriteBytes      int      `json:"max_write_bytes,omitempty"`
	MaxBashOutputBytes int      `json:"max_bash_output_bytes,omitempty"`
	BashTimeoutSeconds int      `json:"bash_timeout_seconds,omitempty"`
}

// Load loads configuration from the global config file, merged on top of the
// built-in defaults. A missing file is not an error.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(home, ".nncode", "config.json")
	cfg := defaultConfig()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	var overlay Config
	if err := json.Unmarshal(data, &overlay); err != nil {
		return nil, err
	}
	cfg.Merge(&overlay)
	return cfg, nil
}

// LoadProject loads project-local configuration if it exists.
func LoadProject() (*Config, error) {
	configPath := ".nncode/config.json"

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// SaveGlobal saves the configuration to the global config file.
func SaveGlobal(cfg *Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, ".nncode")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}

func defaultConfig() *Config {
	return &Config{
		DefaultModel: "gpt-4o",
		Models: map[string]Model{
			"gpt-4o": {
				APIType:  APITypeOpenAICompletions,
				Provider: "openai",
			},
			"gpt-4o-mini": {
				APIType:  APITypeOpenAICompletions,
				Provider: "openai",
			},
			"o3": {
				APIType:  APITypeOpenAICompletions,
				Provider: "openai",
			},
			"llama3": {
				APIType:  APITypeOpenAICompletions,
				Provider: "ollama",
				BaseURL:  "http://127.0.0.1:8033/v1",
			},
		},
		Tools: ToolConfig{
			MaxReadBytes:       50000,
			MaxWriteBytes:      1000000,
			MaxBashOutputBytes: 10000,
			BashTimeoutSeconds: 120,
		},
	}
}

// Merge overlays other on top of c. A non-empty DefaultModel in other wins,
// and model entries in other replace entries with the same key in c.
func (c *Config) Merge(other *Config) {
	if other == nil {
		return
	}
	if other.DefaultModel != "" {
		c.DefaultModel = other.DefaultModel
	}
	if c.Models == nil {
		c.Models = make(map[string]Model)
	}
	for k, v := range other.Models {
		c.Models[k] = v
	}
	c.Tools.Merge(other.Tools)
}

// ResolveModel resolves a configured model by name.
func (c *Config) ResolveModel(name string) (Model, bool) {
	if m, ok := c.Models[name]; ok {
		return m, true
	}
	return Model{}, false
}

// Validate reports invalid configuration after all overlays have been merged.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.DefaultModel) == "" {
		return fmt.Errorf("default_model is required")
	}
	if len(c.Models) == 0 {
		return fmt.Errorf("at least one model is required")
	}
	if _, ok := c.Models[c.DefaultModel]; !ok {
		return fmt.Errorf("default_model %q is not configured", c.DefaultModel)
	}
	for name, model := range c.Models {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("model name cannot be empty")
		}
		if err := model.Validate(name); err != nil {
			return err
		}
	}
	return c.Tools.Validate()
}

// Validate reports unsupported model configuration values.
func (m Model) Validate(name string) error {
	switch m.APIType {
	case "", APITypeOpenAICompletions:
	default:
		return fmt.Errorf("model %q uses unsupported api_type %q (supported: %q)", name, m.APIType, APITypeOpenAICompletions)
	}
	if strings.TrimSpace(m.Provider) == "" {
		return fmt.Errorf("model %q provider is required", name)
	}
	if m.MaxTokens < 0 {
		return fmt.Errorf("model %q max_tokens cannot be negative", name)
	}
	if m.BaseURL == "" {
		if m.Provider != "openai" {
			return fmt.Errorf("model %q provider %q requires base_url", name, m.Provider)
		}
		return nil
	}
	u, err := url.Parse(m.BaseURL)
	if err != nil {
		return fmt.Errorf("model %q base_url is invalid: %w", name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("model %q base_url must use http or https", name)
	}
	if u.Host == "" {
		return fmt.Errorf("model %q base_url must include a host", name)
	}
	return nil
}

// RequestID returns the model identifier to send to the provider.
func (m Model) RequestID(name string) string {
	if m.ID != "" {
		return m.ID
	}
	return name
}

// Merge overlays non-zero tool settings from other on top of t.
func (t *ToolConfig) Merge(other ToolConfig) {
	if other.Disabled != nil {
		t.Disabled = append([]string(nil), other.Disabled...)
	}
	if other.WorkspaceRoot != "" {
		t.WorkspaceRoot = other.WorkspaceRoot
	}
	if other.MaxReadBytes != 0 {
		t.MaxReadBytes = other.MaxReadBytes
	}
	if other.MaxWriteBytes != 0 {
		t.MaxWriteBytes = other.MaxWriteBytes
	}
	if other.MaxBashOutputBytes != 0 {
		t.MaxBashOutputBytes = other.MaxBashOutputBytes
	}
	if other.BashTimeoutSeconds != 0 {
		t.BashTimeoutSeconds = other.BashTimeoutSeconds
	}
}

// Validate reports invalid built-in tool configuration.
func (t ToolConfig) Validate() error {
	seen := make(map[string]struct{}, len(t.Disabled))
	for _, name := range t.Disabled {
		if !slices.Contains(knownToolNames, name) {
			return fmt.Errorf("tools.disabled contains unknown tool %q", name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("tools.disabled contains duplicate tool %q", name)
		}
		seen[name] = struct{}{}
	}
	if t.MaxReadBytes < 0 {
		return fmt.Errorf("tools.max_read_bytes cannot be negative")
	}
	if t.MaxWriteBytes < 0 {
		return fmt.Errorf("tools.max_write_bytes cannot be negative")
	}
	if t.MaxBashOutputBytes < 0 {
		return fmt.Errorf("tools.max_bash_output_bytes cannot be negative")
	}
	if t.BashTimeoutSeconds < 0 {
		return fmt.Errorf("tools.bash_timeout_seconds cannot be negative")
	}
	if t.WorkspaceRoot != "" {
		if _, err := filepath.Abs(t.WorkspaceRoot); err != nil {
			return fmt.Errorf("tools.workspace_root is invalid: %w", err)
		}
	}
	return nil
}

// IsDisabled reports whether a named built-in tool is disabled.
func (t ToolConfig) IsDisabled(name string) bool {
	return slices.Contains(t.Disabled, name)
}
