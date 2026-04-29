package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const APITypeOpenAICompletions = "openai-completions"

const (
	ContextProbeAuto     = "auto"
	ContextProbeOff      = "off"
	ContextProbeLlamaCPP = "llamacpp"
)

const (
	defaultMaxReadBytes       = 50000
	defaultMaxWriteBytes      = 1000000
	defaultMaxBashOutputBytes = 10000
	defaultBashTimeoutSeconds = 120

	defaultContextWindowGPT4o     = 128000
	defaultContextWindowGPT4oMini = 128000
	defaultContextWindowO3        = 200000
	defaultContextWindowLlama3    = 128000
)

var (
	errDefaultModelRequired       = errors.New("default_model is required")
	errAtLeastOneModelRequired    = errors.New("at least one model is required")
	errDefaultModelNotConfigured  = errors.New("default_model is not configured")
	errModelNameEmpty             = errors.New("model name cannot be empty")
	errUnsupportedAPIType         = errors.New("unsupported api_type")
	errProviderRequired           = errors.New("provider is required")
	errMaxTokensNegative          = errors.New("max_tokens cannot be negative")
	errContextWindowNegative      = errors.New("context_window cannot be negative")
	errContextProbeUnsupported    = errors.New("unsupported context_probe")
	errBaseURLRequired            = errors.New("base_url is required")
	errBaseURLScheme              = errors.New("base_url must use http or https")
	errBaseURLHost                = errors.New("base_url must include a host")
	errUnknownToolDisabled        = errors.New("tools.disabled contains unknown tool")
	errDuplicateToolDisabled      = errors.New("tools.disabled contains duplicate tool")
	errMaxReadBytesNegative       = errors.New("tools.max_read_bytes cannot be negative")
	errMaxWriteBytesNegative      = errors.New("tools.max_write_bytes cannot be negative")
	errMaxBashOutputBytesNegative = errors.New("tools.max_bash_output_bytes cannot be negative")
	errBashTimeoutNegative        = errors.New("tools.bash_timeout_seconds cannot be negative")
)

func knownToolNames() []string {
	return []string{"bash", "edit", "find", "grep", "patch", "read", "write"}
}

// Config holds the application configuration.
type Config struct {
	DefaultModel string           `json:"default_model"`
	Models       map[string]Model `json:"models"`
	Tools        ToolConfig       `json:"tools,omitzero"`
}

// Model holds configuration for a single model.
type Model struct {
	ID            string `json:"id,omitempty"`
	APIType       string `json:"api_type"` // "openai-completions" or empty for the default
	Provider      string `json:"provider"`
	BaseURL       string `json:"base_url,omitempty"`
	MaxTokens     int    `json:"max_tokens,omitempty"`
	ContextWindow int    `json:"context_window,omitempty"`
	ContextProbe  string `json:"context_probe,omitempty"`
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
		return nil, fmt.Errorf("cannot get user home directory: %w", err)
	}

	configPath := filepath.Join(home, ".nncode", "config.json")
	cfg := defaultConfig()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}

		return nil, fmt.Errorf("cannot read global config file: %w", err)
	}

	var overlay Config
	err = json.Unmarshal(data, &overlay)
	if err != nil {
		return nil, fmt.Errorf("cannot parse global config file: %w", err)
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
			return &Config{}, nil //nolint:exhaustruct // empty config is a valid zero value
		}

		return nil, fmt.Errorf("cannot read project config file: %w", err)
	}

	var cfg Config
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot parse project config file: %w", err)
	}

	return &cfg, nil
}

// SaveGlobal saves the configuration to the global config file.
func SaveGlobal(cfg *Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get user home directory: %w", err)
	}

	dir := filepath.Join(home, ".nncode")
	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return fmt.Errorf("cannot create config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal config: %w", err)
	}

	err = os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
	if err != nil {
		return fmt.Errorf("cannot write config file: %w", err)
	}

	return nil
}

func defaultConfig() *Config {
	return &Config{
		DefaultModel: "gpt-4o",
		Models: map[string]Model{
			"gpt-4o": {
				APIType:       APITypeOpenAICompletions,
				Provider:      "openai",
				ContextWindow: defaultContextWindowGPT4o,
			},
			"gpt-4o-mini": {
				APIType:       APITypeOpenAICompletions,
				Provider:      "openai",
				ContextWindow: defaultContextWindowGPT4o,
			},
			"o3": {
				APIType:       APITypeOpenAICompletions,
				Provider:      "openai",
				ContextWindow: defaultContextWindowO3,
			},
			"llama3": {
				APIType:       APITypeOpenAICompletions,
				Provider:      "local",
				BaseURL:       "http://127.0.0.1:8033/v1",
				ContextWindow: defaultContextWindowGPT4o,
			},
		},
		Tools: ToolConfig{
			MaxReadBytes:       defaultMaxReadBytes,
			MaxWriteBytes:      defaultMaxWriteBytes,
			MaxBashOutputBytes: defaultMaxBashOutputBytes,
			BashTimeoutSeconds: defaultBashTimeoutSeconds,
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

	maps.Copy(c.Models, other.Models)

	c.Tools.Merge(other.Tools)
}

// ResolveModel resolves a configured model by name.
func (c *Config) ResolveModel(name string) (Model, bool) {
	if m, ok := c.Models[name]; ok {
		return m, true
	}

	return Model{}, false
}

// AutoVendModel adds a model entry for name by cloning the first configured
// model that has a non-empty BaseURL. It returns true if a new entry was
// added. This makes it convenient to use arbitrary model names with local
// OpenAI-compatible endpoints without pre-declaring every name in config.
func (c *Config) AutoVendModel(name string) bool {
	if _, ok := c.Models[name]; ok {
		return false
	}

	templateName := c.findModelTemplateName()
	if templateName == "" {
		return false
	}

	template := c.Models[templateName]
	c.Models[name] = Model{
		APIType:       template.APIType,
		Provider:      template.Provider,
		BaseURL:       template.BaseURL,
		MaxTokens:     template.MaxTokens,
		ContextWindow: template.ContextWindow,
		ContextProbe:  template.ContextProbe,
	}

	return true
}

// Validate reports invalid configuration after all overlays have been merged.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.DefaultModel) == "" {
		return errDefaultModelRequired
	}

	if len(c.Models) == 0 {
		return errAtLeastOneModelRequired
	}

	if _, ok := c.Models[c.DefaultModel]; !ok {
		return fmt.Errorf("default_model %q: %w", c.DefaultModel, errDefaultModelNotConfigured)
	}

	for name, model := range c.Models {
		if strings.TrimSpace(name) == "" {
			return errModelNameEmpty
		}

		err := model.Validate(name)
		if err != nil {
			return err
		}
	}

	return c.Tools.Validate()
}

func (c *Config) findModelTemplateName() string {
	names := make([]string, 0, len(c.Models))
	for name, m := range c.Models {
		if m.BaseURL != "" {
			names = append(names, name)
		}
	}

	if len(names) != 1 {
		return ""
	}

	return names[0]
}

// Validate reports unsupported model configuration values.
func (m Model) Validate(name string) error {
	switch m.APIType {
	case "", APITypeOpenAICompletions:
	default:
		return fmt.Errorf(
			"model %q uses unsupported api_type %q (supported: %q): %w",
			name, m.APIType, APITypeOpenAICompletions, errUnsupportedAPIType,
		)
	}

	if strings.TrimSpace(m.Provider) == "" {
		return fmt.Errorf("model %q provider is required: %w", name, errProviderRequired)
	}

	if m.MaxTokens < 0 {
		return fmt.Errorf("model %q max_tokens cannot be negative: %w", name, errMaxTokensNegative)
	}

	if m.ContextWindow < 0 {
		return fmt.Errorf("model %q context_window cannot be negative: %w", name, errContextWindowNegative)
	}

	switch m.ContextProbe {
	case "", ContextProbeAuto, ContextProbeOff, ContextProbeLlamaCPP:
	default:
		return fmt.Errorf(
			"model %q uses unsupported context_probe %q (supported: %q, %q, %q): %w",
			name,
			m.ContextProbe,
			ContextProbeAuto,
			ContextProbeOff,
			ContextProbeLlamaCPP,
			errContextProbeUnsupported,
		)
	}

	if m.BaseURL == "" {
		if m.Provider != "openai" {
			return fmt.Errorf("model %q provider %q requires base_url: %w", name, m.Provider, errBaseURLRequired)
		}

		return nil
	}

	parsedURL, err := url.Parse(m.BaseURL)
	if err != nil {
		return fmt.Errorf("model %q base_url is invalid: %w", name, err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("model %q base_url must use http or https: %w", name, errBaseURLScheme)
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("model %q base_url must include a host: %w", name, errBaseURLHost)
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
func (t *ToolConfig) Validate() error {
	seen := make(map[string]struct{}, len(t.Disabled))

	for _, name := range t.Disabled {
		if !slices.Contains(knownToolNames(), name) {
			return fmt.Errorf("tools.disabled contains unknown tool %q: %w", name, errUnknownToolDisabled)
		}

		if _, ok := seen[name]; ok {
			return fmt.Errorf("tools.disabled contains duplicate tool %q: %w", name, errDuplicateToolDisabled)
		}

		seen[name] = struct{}{}
	}

	if t.MaxReadBytes < 0 {
		return errMaxReadBytesNegative
	}

	if t.MaxWriteBytes < 0 {
		return errMaxWriteBytesNegative
	}

	if t.MaxBashOutputBytes < 0 {
		return errMaxBashOutputBytesNegative
	}

	if t.BashTimeoutSeconds < 0 {
		return errBashTimeoutNegative
	}

	if t.WorkspaceRoot != "" {
		_, err := filepath.Abs(t.WorkspaceRoot)
		if err != nil {
			return fmt.Errorf("tools.workspace_root is invalid: %w", err)
		}
	}

	return nil
}

// IsDisabled reports whether a named built-in tool is disabled.
func (t *ToolConfig) IsDisabled(name string) bool {
	return slices.Contains(t.Disabled, name)
}
