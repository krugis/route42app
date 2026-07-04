package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krugis/route42app/internal/config"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "app.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("db file not created: %v", err)
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		s.Close()
	}
}

func TestProviderKeyRoundTrip(t *testing.T) {
	s := openTest(t)

	if err := s.SetProviderKey("OpenAI", "sk-secret-123"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProviderKey("openai")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-secret-123" {
		t.Errorf("got %q", got)
	}

	// Aliases canonicalize on read and write.
	if err := s.SetProviderKey("google", "g-key"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetProviderKey("gemini"); got != "g-key" {
		t.Errorf("alias lookup got %q", got)
	}

	// Update replaces.
	s.SetProviderKey("openai", "sk-rotated")
	if got, _ := s.GetProviderKey("openai"); got != "sk-rotated" {
		t.Errorf("after update got %q", got)
	}

	providers, err := s.ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0] != "gemini" || providers[1] != "openai" {
		t.Errorf("providers = %v", providers)
	}

	if err := s.DeleteProviderKey("openai"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetProviderKey("openai"); got != "" {
		t.Errorf("after delete got %q", got)
	}
	if err := s.DeleteProviderKey("openai"); err != nil {
		t.Error("deleting missing key must not error")
	}
}

func TestKeysEncryptedAtRest(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetProviderKey("openai", "sk-plaintext-canary"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	for _, name := range []string{"test.db", "test.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), "sk-plaintext-canary") {
			t.Fatalf("plaintext key found in %s", name)
		}
	}
}

func TestKeyfilePersistsAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.SetProviderKey("groq", "gsk-1")
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got, _ := s2.GetProviderKey("groq"); got != "gsk-1" {
		t.Errorf("key lost across reopen: %q", got)
	}
}

func TestEncryptionKeyFromEnv(t *testing.T) {
	t.Setenv("ROUTE42_ENCRYPTION_KEY", "my-passphrase")
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetProviderKey("openai", "sk-env"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, keyFileName)); !os.IsNotExist(err) {
		t.Error("keyfile must not be created when env key is set")
	}
	if got, _ := s.GetProviderKey("openai"); got != "sk-env" {
		t.Errorf("got %q", got)
	}
}

func TestPrefsLifecycle(t *testing.T) {
	s := openTest(t)
	defaults := config.Default().Prefs

	if err := s.EnsurePrefs(defaults); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if p.Priority != "balanced" || p.FallbackDepth != 2 {
		t.Errorf("prefs = %+v", p)
	}

	// EnsurePrefs never overwrites existing values.
	p.Priority = "cheap"
	p.OnlyLocal = true
	p.DisallowedModels = []string{"gpt-4o", "o3"}
	if err := s.SetPrefs(p); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsurePrefs(defaults); err != nil {
		t.Fatal(err)
	}
	p2, _ := s.GetPrefs()
	if p2.Priority != "cheap" || !p2.OnlyLocal || len(p2.DisallowedModels) != 2 {
		t.Errorf("prefs overwritten by EnsurePrefs: %+v", p2)
	}
}

func TestInteractionsAndStats(t *testing.T) {
	s := openTest(t)

	add := func(model, provider, category, status string, tokens int, cost float64, daysAgo int) {
		t.Helper()
		if err := s.AddInteraction(Interaction{
			Timestamp:        time.Now().UTC().AddDate(0, 0, -daysAgo),
			Model:            model,
			Provider:         provider,
			Category:         category,
			Complexity:       0.4,
			Analyzer:         "heuristic",
			PromptTokens:     tokens / 2,
			CompletionTokens: tokens - tokens/2,
			CostCents:        cost,
			LatencyMs:        800,
			Status:           status,
		}); err != nil {
			t.Fatal(err)
		}
	}

	add("gpt-4o-mini", "openai", "chat", "ok", 100, 0.5, 0)
	add("gpt-4o-mini", "openai", "code", "ok", 200, 1.0, 0)
	add("llama3.2:3b", "ollama", "chat", "ok", 300, 0, 0)
	add("gpt-4o-mini", "openai", "chat", "error", 0, 0, 0)
	add("old-model", "openai", "chat", "ok", 999, 9.9, 40) // outside 30d window

	stats, err := s.GetStats(30)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Requests != 4 {
		t.Errorf("requests = %d, want 4 (40-day-old excluded)", stats.Requests)
	}
	if stats.TotalTokens != 600 || stats.CostCents != 1.5 || stats.Errors != 1 {
		t.Errorf("stats = %+v", stats)
	}
	if len(stats.ByModel) != 2 || stats.ByModel[0].Model != "gpt-4o-mini" || stats.ByModel[0].Requests != 3 {
		t.Errorf("by_model = %+v", stats.ByModel)
	}
	if stats.ByCategory["chat"] != 3 || stats.ByCategory["code"] != 1 {
		t.Errorf("by_category = %+v", stats.ByCategory)
	}

	all, err := s.GetStats(0)
	if err != nil {
		t.Fatal(err)
	}
	if all.Requests != 5 {
		t.Errorf("all-time requests = %d, want 5", all.Requests)
	}

	recent, err := s.RecentInteractions(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].Model != "old-model" {
		// newest-first by insertion order (id DESC)
		t.Errorf("recent = %+v", recent)
	}
}
