package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root Route42 configuration. Values are resolved in order:
// built-in defaults, then the YAML file, then ROUTE42_* environment variables.
type Config struct {
	Server    Server              `yaml:"server"`
	Analyzer  Analyzer            `yaml:"analyzer"`
	Ollama    Ollama              `yaml:"ollama"`
	DB        DB                  `yaml:"db"`
	Providers map[string]Provider `yaml:"providers"`
	Prefs     Prefs               `yaml:"prefs"`
}

// Server configures the HTTP gateway.
type Server struct {
	// Port the gateway listens on (default 4242).
	Port int `yaml:"port"`
	// APIToken, when set, requires "Authorization: Bearer <token>" on all
	// /api endpoints. Empty (default) means no auth: local single-user use.
	APIToken string `yaml:"api_token"`
}

// Analyzer selects and configures the prompt analyzer.
type Analyzer struct {
	// Mode is "heuristic" (default) or "llm".
	Mode string `yaml:"mode"`
	LLM  AnalyzerLLM `yaml:"llm"`
}

// AnalyzerLLM configures the optional Ollama-backed analyzer.
type AnalyzerLLM struct {
	// Model is the Ollama model used for prompt classification.
	Model string `yaml:"model"`
	// TimeoutMs bounds the classification call; on timeout the request
	// falls back to the heuristic analyzer.
	TimeoutMs int `yaml:"timeout_ms"`
}

// Ollama configures the local Ollama endpoint used for discovery,
// completions, and the LLM analyzer.
type Ollama struct {
	BaseURL string `yaml:"base_url"`
}

// DB configures local persistence.
type DB struct {
	// Path to the SQLite database file. Created on first run.
	Path string `yaml:"path"`
}

// Provider holds static credentials for a cloud provider. Keys can also be
// managed at runtime via the /api/keys endpoints (stored encrypted in the DB);
// config-file keys take precedence when both are present.
type Provider struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"` // optional override, e.g. proxies
}

// Prefs holds the initial routing preferences applied on first run.
// After that, preferences live in the database (editable via /api/prefs).
type Prefs struct {
	// Priority is one of balanced, fast, cheap, accurate.
	Priority           string   `yaml:"priority"`
	MaxCostCents       float64  `yaml:"max_cost_cents"`
	LatencyToleranceMs int      `yaml:"latency_tolerance_ms"`
	OnlyFree           bool     `yaml:"only_free"`
	OnlyLocal          bool     `yaml:"only_local"`
	MaxResponseTokens  int      `yaml:"max_response_tokens"`
	DefaultModel       string   `yaml:"default_model"`
	FallbackDepth      int      `yaml:"fallback_depth"`
	DisallowedModels   []string `yaml:"disallowed_models"`
}

// Analyzer modes.
const (
	ModeHeuristic = "heuristic"
	ModeLLM       = "llm"
)

// Priority modes.
var validPriorities = []string{"balanced", "fast", "cheap", "accurate"}

// Default returns the built-in configuration: a zero-config setup that works
// with only a local Ollama installation.
func Default() *Config {
	return &Config{
		Server: Server{Port: 4242},
		Analyzer: Analyzer{
			Mode: ModeHeuristic,
			LLM:  AnalyzerLLM{Model: "qwen2.5:0.5b", TimeoutMs: 1500},
		},
		Ollama:    Ollama{BaseURL: "http://localhost:11434"},
		DB:        DB{Path: defaultDBPath()},
		Providers: map[string]Provider{},
		Prefs: Prefs{
			Priority:      "balanced",
			FallbackDepth: 2,
		},
	}
}

// defaultDBPath places the database under the OS user config directory,
// falling back to the working directory when that cannot be determined.
func defaultDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "route42.db"
	}
	return filepath.Join(dir, "route42", "route42.db")
}

// Load builds the effective configuration. path may be empty, in which case
// "route42.yaml" in the working directory is used if it exists; a non-empty
// path must exist. Environment variables override file values.
func Load(path string) (*Config, error) {
	cfg := Default()

	explicit := path != ""
	if !explicit {
		path = "route42.yaml"
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist) && !explicit:
		// zero-config run: defaults only
	default:
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	if err := cfg.applyEnv(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyEnv overlays ROUTE42_* environment variables. Provider API keys use
// ROUTE42_<PROVIDER>_API_KEY (e.g. ROUTE42_OPENAI_API_KEY).
func (c *Config) applyEnv() error {
	if v := os.Getenv("ROUTE42_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("ROUTE42_PORT: %q is not a number", v)
		}
		c.Server.Port = p
	}
	if v := os.Getenv("ROUTE42_API_TOKEN"); v != "" {
		c.Server.APIToken = v
	}
	if v := os.Getenv("ROUTE42_ANALYZER_MODE"); v != "" {
		c.Analyzer.Mode = v
	}
	if v := os.Getenv("ROUTE42_ANALYZER_LLM_MODEL"); v != "" {
		c.Analyzer.LLM.Model = v
	}
	if v := os.Getenv("ROUTE42_ANALYZER_LLM_TIMEOUT_MS"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("ROUTE42_ANALYZER_LLM_TIMEOUT_MS: %q is not a number", v)
		}
		c.Analyzer.LLM.TimeoutMs = ms
	}
	if v := os.Getenv("ROUTE42_OLLAMA_BASE_URL"); v != "" {
		c.Ollama.BaseURL = v
	}
	if v := os.Getenv("ROUTE42_DB_PATH"); v != "" {
		c.DB.Path = v
	}

	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}
		provider, found := strings.CutPrefix(name, "ROUTE42_")
		if !found {
			continue
		}
		provider, found = strings.CutSuffix(provider, "_API_KEY")
		if !found || provider == "" {
			continue
		}
		key := strings.ToLower(provider)
		if c.Providers == nil {
			c.Providers = map[string]Provider{}
		}
		p := c.Providers[key]
		p.APIKey = value
		c.Providers[key] = p
	}
	return nil
}

// Validate reports the first configuration error found.
func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port: %d is not a valid TCP port (1-65535)", c.Server.Port)
	}
	if c.Analyzer.Mode != ModeHeuristic && c.Analyzer.Mode != ModeLLM {
		return fmt.Errorf("analyzer.mode: %q is not supported (use %q or %q)",
			c.Analyzer.Mode, ModeHeuristic, ModeLLM)
	}
	if c.Analyzer.LLM.TimeoutMs <= 0 {
		return fmt.Errorf("analyzer.llm.timeout_ms: must be positive, got %d", c.Analyzer.LLM.TimeoutMs)
	}
	if c.Analyzer.Mode == ModeLLM && c.Analyzer.LLM.Model == "" {
		return errors.New("analyzer.llm.model: required when analyzer.mode is \"llm\"")
	}
	if c.Ollama.BaseURL == "" {
		return errors.New("ollama.base_url: must not be empty")
	}
	if c.DB.Path == "" {
		return errors.New("db.path: must not be empty")
	}
	if !contains(validPriorities, c.Prefs.Priority) {
		return fmt.Errorf("prefs.priority: %q is not supported (use one of %s)",
			c.Prefs.Priority, strings.Join(validPriorities, ", "))
	}
	if c.Prefs.FallbackDepth < 0 {
		return fmt.Errorf("prefs.fallback_depth: must be >= 0, got %d", c.Prefs.FallbackDepth)
	}
	if c.Prefs.MaxCostCents < 0 {
		return fmt.Errorf("prefs.max_cost_cents: must be >= 0, got %g", c.Prefs.MaxCostCents)
	}
	if c.Prefs.LatencyToleranceMs < 0 {
		return fmt.Errorf("prefs.latency_tolerance_ms: must be >= 0, got %d", c.Prefs.LatencyToleranceMs)
	}
	if c.Prefs.MaxResponseTokens < 0 {
		return fmt.Errorf("prefs.max_response_tokens: must be >= 0, got %d", c.Prefs.MaxResponseTokens)
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
