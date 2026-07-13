package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krugis/route42app/internal/config"
	"github.com/krugis/route42app/internal/store"
)

// newUITestServer builds a Server on a temp store with the given config
// mutations applied to the defaults.
func newUITestServer(t *testing.T, mutate func(*config.Config)) *httptest.Server {
	t.Helper()
	cfg := config.Default()
	cfg.DB.Path = filepath.Join(t.TempDir(), "test.db")
	if mutate != nil {
		mutate(cfg)
	}
	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsurePrefs(cfg.Prefs); err != nil {
		t.Fatalf("seed prefs: %v", err)
	}
	srv, err := New(cfg, st, Options{})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestWebUIServedAtRoot(t *testing.T) {
	ts := newUITestServer(t, nil) // UI defaults to on

	res := get(t, ts.URL+"/", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("GET /: content-type = %q, want text/html", ct)
	}

	for _, asset := range []string{"/app.js", "/style.css", "/logo.svg"} {
		if res := get(t, ts.URL+asset, ""); res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", asset, res.StatusCode)
		}
	}
}

func TestWebUIDisabled(t *testing.T) {
	ts := newUITestServer(t, func(c *config.Config) { c.Server.UI = false })

	if res := get(t, ts.URL+"/", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / with ui disabled: status = %d, want 404", res.StatusCode)
	}
	// The API is unaffected.
	if res := get(t, ts.URL+"/health", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("GET /health with ui disabled: status = %d, want 200", res.StatusCode)
	}
	if res := get(t, ts.URL+"/api/prefs", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/prefs with ui disabled: status = %d, want 200", res.StatusCode)
	}
}

func TestWebUIAuthScope(t *testing.T) {
	ts := newUITestServer(t, func(c *config.Config) { c.Server.APIToken = "secret" })

	// Static console and health stay public.
	if res := get(t, ts.URL+"/", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("GET / with auth on: status = %d, want 200", res.StatusCode)
	}
	if res := get(t, ts.URL+"/health", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("GET /health with auth on: status = %d, want 200", res.StatusCode)
	}
	// API routes still require the token.
	if res := get(t, ts.URL+"/api/prefs", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/prefs without token: status = %d, want 401", res.StatusCode)
	}
	if res := get(t, ts.URL+"/api/prefs", "secret"); res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/prefs with token: status = %d, want 200", res.StatusCode)
	}
}

func TestInteractionsEndpoint(t *testing.T) {
	ts := newUITestServer(t, nil)

	res := get(t, ts.URL+"/api/interactions?limit=10", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/interactions: status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Interactions []store.Interaction `json:"interactions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode interactions: %v", err)
	}
	if body.Interactions == nil {
		t.Fatal("interactions should be an empty array, not null")
	}
}
