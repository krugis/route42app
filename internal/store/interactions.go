package store

import (
	"fmt"
	"time"
)

// Interaction is one routed completion, recorded for local analytics.
type Interaction struct {
	ID               int64     `json:"id"`
	Timestamp        time.Time `json:"ts"`
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	Category         string    `json:"category,omitempty"`
	Complexity       float64   `json:"complexity"`
	Analyzer         string    `json:"analyzer,omitempty"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CostCents        float64   `json:"cost_cents"`
	LatencyMs        int       `json:"latency_ms"`
	TTFTMs           int       `json:"ttft_ms,omitempty"`
	Status           string    `json:"status"` // ok | error
	FallbackAttempts int       `json:"fallback_attempts,omitempty"`
}

// AddInteraction appends one record to the interaction log.
func (s *Store) AddInteraction(in Interaction) error {
	ts := in.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	status := in.Status
	if status == "" {
		status = "ok"
	}
	_, err := s.db.Exec(`INSERT INTO interactions
		(ts, model, provider, category, complexity, analyzer, prompt_tokens,
		 completion_tokens, cost_cents, latency_ms, ttft_ms, status, fallback_attempts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts.UTC().Format(time.RFC3339), in.Model, in.Provider, in.Category,
		in.Complexity, in.Analyzer, in.PromptTokens, in.CompletionTokens,
		in.CostCents, in.LatencyMs, in.TTFTMs, status, in.FallbackAttempts)
	return err
}

// RecentInteractions returns up to limit records, newest first.
func (s *Store) RecentInteractions(limit int) ([]Interaction, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, ts, model, provider, category, complexity,
		analyzer, prompt_tokens, completion_tokens, cost_cents, latency_ms, ttft_ms,
		status, fallback_attempts FROM interactions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Interaction
	for rows.Next() {
		var in Interaction
		var ts string
		if err := rows.Scan(&in.ID, &ts, &in.Model, &in.Provider, &in.Category,
			&in.Complexity, &in.Analyzer, &in.PromptTokens, &in.CompletionTokens,
			&in.CostCents, &in.LatencyMs, &in.TTFTMs, &in.Status, &in.FallbackAttempts); err != nil {
			return nil, err
		}
		in.Timestamp, _ = time.Parse(time.RFC3339, ts)
		out = append(out, in)
	}
	return out, rows.Err()
}

// ModelStats aggregates usage for one model.
type ModelStats struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Requests     int     `json:"requests"`
	TotalTokens  int     `json:"total_tokens"`
	CostCents    float64 `json:"cost_cents"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	Errors       int     `json:"errors"`
}

// Stats summarizes usage over a time window.
type Stats struct {
	Since       time.Time      `json:"since"`
	Requests    int            `json:"requests"`
	TotalTokens int            `json:"total_tokens"`
	CostCents   float64        `json:"cost_cents"`
	Errors      int            `json:"errors"`
	ByModel     []ModelStats   `json:"by_model"`
	ByCategory  map[string]int `json:"by_category"`
}

// GetStats aggregates the interaction log over the last `days` days
// (days <= 0 means all time).
func (s *Store) GetStats(days int) (*Stats, error) {
	since := time.Time{}
	if days > 0 {
		since = time.Now().UTC().AddDate(0, 0, -days)
	}
	sinceStr := since.Format(time.RFC3339)

	stats := &Stats{Since: since, ByCategory: map[string]int{}}
	err := s.db.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(prompt_tokens + completion_tokens), 0),
		COALESCE(SUM(cost_cents), 0),
		COALESCE(SUM(CASE WHEN status != 'ok' THEN 1 ELSE 0 END), 0)
		FROM interactions WHERE ts >= ?`, sinceStr).Scan(
		&stats.Requests, &stats.TotalTokens, &stats.CostCents, &stats.Errors)
	if err != nil {
		return nil, fmt.Errorf("stats totals: %w", err)
	}

	rows, err := s.db.Query(`SELECT model, provider, COUNT(*),
		COALESCE(SUM(prompt_tokens + completion_tokens), 0),
		COALESCE(SUM(cost_cents), 0),
		COALESCE(AVG(latency_ms), 0),
		COALESCE(SUM(CASE WHEN status != 'ok' THEN 1 ELSE 0 END), 0)
		FROM interactions WHERE ts >= ?
		GROUP BY model, provider ORDER BY COUNT(*) DESC`, sinceStr)
	if err != nil {
		return nil, fmt.Errorf("stats by model: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m ModelStats
		if err := rows.Scan(&m.Model, &m.Provider, &m.Requests, &m.TotalTokens,
			&m.CostCents, &m.AvgLatencyMs, &m.Errors); err != nil {
			return nil, err
		}
		stats.ByModel = append(stats.ByModel, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	catRows, err := s.db.Query(`SELECT category, COUNT(*) FROM interactions
		WHERE ts >= ? AND category != '' GROUP BY category`, sinceStr)
	if err != nil {
		return nil, fmt.Errorf("stats by category: %w", err)
	}
	defer catRows.Close()
	for catRows.Next() {
		var cat string
		var n int
		if err := catRows.Scan(&cat, &n); err != nil {
			return nil, err
		}
		stats.ByCategory[cat] = n
	}
	return stats, catRows.Err()
}
