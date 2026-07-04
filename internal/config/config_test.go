package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config must validate, got: %v", err)
	}
	if cfg.Server.Port != 4242 {
		t.Errorf("default port = %d, want 4242", cfg.Server.Port)
	}
	if cfg.Analyzer.Mode != ModeHeuristic {
		t.Errorf("default analyzer mode = %q, want %q", cfg.Analyzer.Mode, ModeHeuristic)
	}
	if cfg.Analyzer.LLM.TimeoutMs != 1500 {
		t.Errorf("default llm timeout = %d, want 1500", cfg.Analyzer.LLM.TimeoutMs)
	}
	if cfg.Ollama.BaseURL != "http://localhost:11434" {
		t.Errorf("default ollama url = %q", cfg.Ollama.BaseURL)
	}
	if cfg.Prefs.Priority != "balanced" || cfg.Prefs.FallbackDepth != 2 {
		t.Errorf("default prefs = %+v", cfg.Prefs)
	}
	if cfg.DB.Path == "" {
		t.Error("default db path must not be empty")
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "route42.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadYAML(t *testing.T) {
	path := writeConfig(t, `
server:
  port: 5000
analyzer:
  mode: llm
  llm:
    model: llama3.2:1b
providers:
  openai:
    api_key: sk-test
prefs:
  priority: cheap
  only_local: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 5000 {
		t.Errorf("port = %d, want 5000", cfg.Server.Port)
	}
	if cfg.Analyzer.Mode != ModeLLM || cfg.Analyzer.LLM.Model != "llama3.2:1b" {
		t.Errorf("analyzer = %+v", cfg.Analyzer)
	}
	// Unset file values keep defaults.
	if cfg.Analyzer.LLM.TimeoutMs != 1500 {
		t.Errorf("timeout = %d, want default 1500", cfg.Analyzer.LLM.TimeoutMs)
	}
	if cfg.Providers["openai"].APIKey != "sk-test" {
		t.Errorf("providers = %+v", cfg.Providers)
	}
	if cfg.Prefs.Priority != "cheap" || !cfg.Prefs.OnlyLocal {
		t.Errorf("prefs = %+v", cfg.Prefs)
	}
}

func TestLoadUnknownFieldRejected(t *testing.T) {
	path := writeConfig(t, "server:\n  prot: 5000\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "prot") {
		t.Fatalf("want unknown-field error mentioning \"prot\", got: %v", err)
	}
}

func TestLoadMissingExplicitFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("explicit missing config file must error")
	}
}

func TestLoadMissingImplicitFileOK(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("implicit missing config must fall back to defaults, got: %v", err)
	}
	if cfg.Server.Port != 4242 {
		t.Errorf("port = %d, want default", cfg.Server.Port)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("ROUTE42_PORT", "9999")
	t.Setenv("ROUTE42_ANALYZER_MODE", "llm")
	t.Setenv("ROUTE42_ANALYZER_LLM_TIMEOUT_MS", "2500")
	t.Setenv("ROUTE42_OLLAMA_BASE_URL", "http://gpu-box:11434")
	t.Setenv("ROUTE42_DB_PATH", "custom.db")
	t.Setenv("ROUTE42_GROQ_API_KEY", "gsk-test")
	t.Setenv("ROUTE42_API_TOKEN", "secret") // must NOT become provider "api"/"" key

	path := writeConfig(t, "server:\n  port: 5000\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("env must beat file: port = %d, want 9999", cfg.Server.Port)
	}
	if cfg.Analyzer.Mode != ModeLLM || cfg.Analyzer.LLM.TimeoutMs != 2500 {
		t.Errorf("analyzer = %+v", cfg.Analyzer)
	}
	if cfg.Ollama.BaseURL != "http://gpu-box:11434" || cfg.DB.Path != "custom.db" {
		t.Errorf("ollama/db = %+v %+v", cfg.Ollama, cfg.DB)
	}
	if cfg.Providers["groq"].APIKey != "gsk-test" {
		t.Errorf("providers = %+v", cfg.Providers)
	}
	if cfg.Server.APIToken != "secret" {
		t.Errorf("api token = %q", cfg.Server.APIToken)
	}
	if _, ok := cfg.Providers[""]; ok {
		t.Error("ROUTE42_API_TOKEN must not create an empty provider entry")
	}
}

func TestEnvBadNumber(t *testing.T) {
	t.Setenv("ROUTE42_PORT", "not-a-port")
	if _, err := Load(writeConfig(t, "")); err == nil {
		t.Fatal("bad ROUTE42_PORT must error")
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"bad port", func(c *Config) { c.Server.Port = 0 }, "server.port"},
		{"bad mode", func(c *Config) { c.Analyzer.Mode = "ml" }, "analyzer.mode"},
		{"bad timeout", func(c *Config) { c.Analyzer.LLM.TimeoutMs = 0 }, "timeout_ms"},
		{"llm mode without model", func(c *Config) { c.Analyzer.Mode = ModeLLM; c.Analyzer.LLM.Model = "" }, "analyzer.llm.model"},
		{"empty ollama url", func(c *Config) { c.Ollama.BaseURL = "" }, "ollama.base_url"},
		{"empty db path", func(c *Config) { c.DB.Path = "" }, "db.path"},
		{"bad priority", func(c *Config) { c.Prefs.Priority = "coder" }, "prefs.priority"},
		{"negative fallback", func(c *Config) { c.Prefs.FallbackDepth = -1 }, "fallback_depth"},
		{"negative cost", func(c *Config) { c.Prefs.MaxCostCents = -1 }, "max_cost_cents"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
