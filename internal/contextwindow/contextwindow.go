// Package contextwindow resolves model context-window metadata.
package contextwindow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nncode/internal/config"
)

const (
	// DefaultResolveTimeout is the maximum time spent on live metadata probes.
	DefaultResolveTimeout = 2 * time.Second
	maxResponseBytes      = 1 << 20
)

// Source describes where a resolved context window came from.
type Source string

const (
	// SourceUnknown indicates that no context window is known.
	SourceUnknown Source = ""
	// SourceProps indicates llama.cpp /props metadata.
	SourceProps Source = "llama.cpp /props"
	// SourceModelsMeta indicates llama.cpp's nonstandard /v1/models metadata.
	SourceModelsMeta Source = "llama.cpp /v1/models meta.n_ctx_train"
	// SourceConfig indicates the model's configured context_window value.
	SourceConfig Source = "config context_window"
)

// Window holds the resolved context-window size in tokens.
type Window struct {
	Tokens int
	Source Source
}

// Known reports whether the window has a positive token size.
func (w Window) Known() bool {
	return w.Tokens > 0
}

// Resolver resolves context metadata for OpenAI-compatible model configs.
type Resolver struct {
	HTTPClient *http.Client
	APIKey     string
}

// Configured returns the static context window configured for model, if any.
func Configured(model config.Model) Window {
	if model.ContextWindow > 0 {
		return Window{Tokens: model.ContextWindow, Source: SourceConfig}
	}

	return unknownWindow()
}

// Resolve returns a context window using live metadata first when the endpoint
// is known-local or explicitly configured as llama.cpp, then config.
func (r Resolver) Resolve(ctx context.Context, model config.Model, requestModelID string) Window {
	if shouldProbeLive(model) {
		if window := r.resolveProps(ctx, model.BaseURL); window.Known() {
			return window
		}

		if window := r.resolveModelsMeta(ctx, model.BaseURL, requestModelID); window.Known() {
			return window
		}
	}

	if window := Configured(model); window.Known() {
		return window
	}

	return unknownWindow()
}

// FormatUsage renders used/free context tokens. Unknown values are shown as ?.
func FormatUsage(usedTokens int, window Window) string {
	usedKnown := usedTokens > 0
	windowKnown := window.Known()

	switch {
	case usedKnown && windowKnown:
		free := max(window.Tokens-usedTokens, 0)

		return FormatTokenCount(usedTokens) + "/" + FormatTokenCount(free)
	case usedKnown:
		return FormatTokenCount(usedTokens) + "/?"
	case windowKnown:
		return "?/" + FormatTokenCount(window.Tokens)
	default:
		return "?/?"
	}
}

// FormatSource returns a display label for the context-window source.
func FormatSource(window Window) string {
	if window.Source == SourceUnknown {
		return "unknown"
	}

	return string(window.Source)
}

func (r Resolver) resolveProps(ctx context.Context, baseURL string) Window {
	endpoint, err := propsEndpoint(baseURL)
	if err != nil {
		return unknownWindow()
	}

	var parsed propsResponse
	if !r.getJSON(ctx, endpoint, &parsed) {
		return unknownWindow()
	}

	nCtx := parsed.DefaultGenerationSettings.NCtx
	if nCtx <= 0 {
		return unknownWindow()
	}

	return Window{Tokens: nCtx, Source: SourceProps}
}

func (r Resolver) resolveModelsMeta(ctx context.Context, baseURL string, modelID string) Window {
	endpoint, err := modelsEndpoint(baseURL)
	if err != nil {
		return unknownWindow()
	}

	var parsed modelsResponse
	if !r.getJSON(ctx, endpoint, &parsed) {
		return unknownWindow()
	}

	if nCtx := matchingModelContext(parsed.Data, modelID); nCtx > 0 {
		return Window{Tokens: nCtx, Source: SourceModelsMeta}
	}

	return unknownWindow()
}

func (r Resolver) getJSON(ctx context.Context, endpoint string, target any) bool {
	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}

	if r.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))

		return false
	}

	err = json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(target)

	return err == nil
}

type propsResponse struct {
	DefaultGenerationSettings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`
}

type modelsResponse struct {
	Data []modelsObject `json:"data"`
}

type modelsObject struct {
	ID   string `json:"id"`
	Meta struct {
		NCtxTrain int `json:"n_ctx_train"`
	} `json:"meta"`
}

func unknownWindow() Window {
	return Window{Tokens: 0, Source: SourceUnknown}
}

func matchingModelContext(models []modelsObject, modelID string) int {
	modelID = strings.TrimSpace(modelID)
	for _, model := range models {
		if model.ID == modelID && model.Meta.NCtxTrain > 0 {
			return model.Meta.NCtxTrain
		}
	}

	if modelID != "" {
		return 0
	}

	for _, model := range models {
		if model.Meta.NCtxTrain > 0 {
			return model.Meta.NCtxTrain
		}
	}

	return 0
}

func shouldProbeLive(model config.Model) bool {
	if strings.TrimSpace(model.BaseURL) == "" {
		return false
	}

	switch model.ContextProbe {
	case config.ContextProbeOff:
		return false
	case config.ContextProbeLlamaCPP:
		return true
	}

	provider := strings.ToLower(strings.TrimSpace(model.Provider))
	if provider == "llamacpp" || provider == "llama.cpp" {
		return true
	}

	parsed, err := url.Parse(model.BaseURL)
	if err != nil {
		return false
	}

	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func propsEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}

	path := strings.TrimRight(parsed.Path, "/")
	if trimmed, ok := strings.CutSuffix(path, "/v1"); ok {
		path = trimmed
	}

	parsed.Path = strings.TrimRight(path, "/") + "/props"
	parsed.RawQuery = "autoload=false"
	parsed.Fragment = ""

	return parsed.String(), nil
}

func modelsEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/models"
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed.String(), nil
}

// FormatTokenCount returns a compact human-readable token count.
func FormatTokenCount(n int) string {
	const (
		thousand = 1_000
		million  = 1_000_000
	)

	switch {
	case n >= million:
		return fmt.Sprintf("%.1fM", float64(n)/million)
	case n >= thousand:
		return fmt.Sprintf("%.1fk", float64(n)/thousand)
	default:
		return strconv.Itoa(n)
	}
}
