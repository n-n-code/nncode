package contextwindow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"nncode/internal/config"
)

func TestResolvePropsReturnsNCtx(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/props", r.URL.Path)
		assert.Equal(t, "false", r.URL.Query().Get("autoload"))
		_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":128000}}`))
	})

	window := Resolver{HTTPClient: handlerClient(handler)}.Resolve(context.Background(), config.Model{
		BaseURL: "http://127.0.0.1:8033",
	}, "test")

	assert.Equal(t, Window{Tokens: 128000, Source: SourceProps}, window)
}

func TestResolvePropsTrimsV1BaseURL(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/props", r.URL.Path)
		_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":64000}}`))
	})

	window := Resolver{HTTPClient: handlerClient(handler)}.Resolve(context.Background(), config.Model{
		BaseURL: "http://127.0.0.1:8033/v1",
	}, "test")

	assert.Equal(t, Window{Tokens: 64000, Source: SourceProps}, window)
}

func TestResolveModelsMetaFallback(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			http.NotFound(w, r)
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"other","meta":{"n_ctx_train":32000}},{"id":"test","meta":{"n_ctx_train":128000}}]}`))
		default:
			http.NotFound(w, r)
		}
	})

	window := Resolver{HTTPClient: handlerClient(handler)}.Resolve(context.Background(), config.Model{
		BaseURL: "http://127.0.0.1:8033/v1",
	}, "test")

	assert.Equal(t, Window{Tokens: 128000, Source: SourceModelsMeta}, window)
}

func TestResolveOpenAIStandardModelsObjectUnknown(t *testing.T) {
	var requests int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/props":
			http.NotFound(w, r)
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test","object":"model","created":1,"owned_by":"openai"}]}`))
		default:
			http.NotFound(w, r)
		}
	})

	window := Resolver{HTTPClient: handlerClient(handler)}.Resolve(context.Background(), config.Model{
		ID:       "gpt-test",
		Provider: "openai",
		BaseURL:  "http://openai.test/v1",
	}, "gpt-test")

	assert.False(t, window.Known())
	assert.Equal(t, SourceUnknown, window.Source)
	assert.Zero(t, requests)
}

func TestResolveContextProbeOffSkipsLiveMetadata(t *testing.T) {
	var requests int
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":128000}}`))
	})

	window := Resolver{HTTPClient: handlerClient(handler)}.Resolve(context.Background(), config.Model{
		Provider:     "llamacpp",
		BaseURL:      "http://127.0.0.1:8033/v1",
		ContextProbe: config.ContextProbeOff,
	}, "test")

	assert.False(t, window.Known())
	assert.Zero(t, requests)
}

func TestResolveUsesConfiguredFallback(t *testing.T) {
	window := Resolver{}.Resolve(context.Background(), config.Model{ContextWindow: 4096}, "test")

	assert.Equal(t, Window{Tokens: 4096, Source: SourceConfig}, window)
}

func TestConfigured(t *testing.T) {
	assert.Equal(t, Window{Tokens: 4096, Source: SourceConfig}, Configured(config.Model{ContextWindow: 4096}))
	assert.False(t, Configured(config.Model{}).Known())
}

func TestResolveRemoteLlamaCppProviderProbesLiveMetadata(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/props", r.URL.Path)
		_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":32000}}`))
	})

	window := Resolver{HTTPClient: handlerClient(handler)}.Resolve(context.Background(), config.Model{
		Provider: "llamacpp",
		BaseURL:  "https://llama.example/v1",
	}, "test")

	assert.Equal(t, Window{Tokens: 32000, Source: SourceProps}, window)
}

func TestResolveContextProbeLlamaCPPForcesRemoteProbe(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/props", r.URL.Path)
		_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":64000}}`))
	})

	window := Resolver{HTTPClient: handlerClient(handler)}.Resolve(context.Background(), config.Model{
		Provider:     "openai",
		BaseURL:      "https://llama.example/v1",
		ContextProbe: config.ContextProbeLlamaCPP,
	}, "test")

	assert.Equal(t, Window{Tokens: 64000, Source: SourceProps}, window)
}

func TestResolveModelsMetaUnknownWhenRequestModelMissing(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			http.NotFound(w, r)
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"other","meta":{"n_ctx_train":32000}}]}`))
		default:
			http.NotFound(w, r)
		}
	})

	window := Resolver{HTTPClient: handlerClient(handler)}.Resolve(context.Background(), config.Model{
		BaseURL: "http://127.0.0.1:8033/v1",
	}, "test")

	assert.False(t, window.Known())
	assert.Equal(t, SourceUnknown, window.Source)
}

func TestFormatUsage(t *testing.T) {
	assert.Equal(t, "12.3k/115.7k", FormatUsage(12300, Window{Tokens: 128000, Source: SourceConfig}))
	assert.Equal(t, "?/128.0k", FormatUsage(0, Window{Tokens: 128000, Source: SourceConfig}))
	assert.Equal(t, "12.3k/?", FormatUsage(12300, Window{}))
	assert.Equal(t, "?/?", FormatUsage(0, Window{}))
}

func TestFormatSource(t *testing.T) {
	assert.Equal(t, "config context_window", FormatSource(Window{Tokens: 128000, Source: SourceConfig}))
	assert.Equal(t, "unknown", FormatSource(Window{}))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func handlerClient(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			return recorder.Result(), nil
		}),
		CheckRedirect: nil,
		Jar:           nil,
		Timeout:       0,
	}
}

func TestResolveProps_SendsAPIKey(t *testing.T) {
	var authHeader string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":64000}}`))
	})

	window := Resolver{HTTPClient: handlerClient(handler), APIKey: "secret"}.Resolve(context.Background(), config.Model{
		BaseURL: "http://127.0.0.1:8033",
	}, "test")

	assert.Equal(t, Window{Tokens: 64000, Source: SourceProps}, window)
	assert.Equal(t, "Bearer secret", authHeader)
}
