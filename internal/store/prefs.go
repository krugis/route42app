package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/krugis/route42app/internal/config"
)

// Prefs are the routing preferences. Community Edition is single-user, so
// exactly one row exists; config.Prefs supplies first-run defaults.
type Prefs = config.Prefs

// EnsurePrefs inserts the initial preferences row if none exists.
// Existing preferences are left untouched.
func (s *Store) EnsurePrefs(defaults Prefs) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO prefs
		(id, priority, max_cost_cents, latency_tolerance_ms, only_free, only_local,
		 max_response_tokens, default_model, fallback_depth, disallowed_models, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		defaults.Priority, defaults.MaxCostCents, defaults.LatencyToleranceMs,
		boolToInt(defaults.OnlyFree), boolToInt(defaults.OnlyLocal),
		defaults.MaxResponseTokens, defaults.DefaultModel, defaults.FallbackDepth,
		strings.Join(defaults.DisallowedModels, ","), now())
	return err
}

// GetPrefs returns the stored preferences.
func (s *Store) GetPrefs() (Prefs, error) {
	var p Prefs
	var onlyFree, onlyLocal int
	var disallowed string
	err := s.db.QueryRow(`SELECT priority, max_cost_cents, latency_tolerance_ms,
		only_free, only_local, max_response_tokens, default_model, fallback_depth,
		disallowed_models FROM prefs WHERE id = 1`).Scan(
		&p.Priority, &p.MaxCostCents, &p.LatencyToleranceMs,
		&onlyFree, &onlyLocal, &p.MaxResponseTokens, &p.DefaultModel,
		&p.FallbackDepth, &disallowed)
	if errors.Is(err, sql.ErrNoRows) {
		return p, errors.New("preferences not initialized (call EnsurePrefs)")
	}
	if err != nil {
		return p, err
	}
	p.OnlyFree = onlyFree == 1
	p.OnlyLocal = onlyLocal == 1
	if disallowed != "" {
		p.DisallowedModels = strings.Split(disallowed, ",")
	}
	return p, nil
}

// SetPrefs replaces the stored preferences.
func (s *Store) SetPrefs(p Prefs) error {
	res, err := s.db.Exec(`UPDATE prefs SET priority = ?, max_cost_cents = ?,
		latency_tolerance_ms = ?, only_free = ?, only_local = ?,
		max_response_tokens = ?, default_model = ?, fallback_depth = ?,
		disallowed_models = ?, updated_at = ? WHERE id = 1`,
		p.Priority, p.MaxCostCents, p.LatencyToleranceMs,
		boolToInt(p.OnlyFree), boolToInt(p.OnlyLocal),
		p.MaxResponseTokens, p.DefaultModel, p.FallbackDepth,
		strings.Join(p.DisallowedModels, ","), now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("preferences not initialized (call EnsurePrefs)")
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
