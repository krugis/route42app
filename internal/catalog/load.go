package catalog

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/krugis/route42app/data"
)

// Load returns the embedded catalog snapshot.
func Load() (*Catalog, error) {
	return parse(data.CatalogJSON, "embedded catalog")
}

// LoadFile loads a catalog snapshot from disk, overriding the embedded one.
func LoadFile(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", path, err)
	}
	return parse(raw, path)
}

func parse(raw []byte, source string) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &c, nil
}

// Validate checks structural invariants: supported schema, non-empty
// unique (provider, id) pairs, valid sources, sane metric ranges.
func (c *Catalog) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema_version %d (this build supports 1)", c.SchemaVersion)
	}
	if len(c.Models) == 0 {
		return fmt.Errorf("catalog contains no models")
	}
	seen := make(map[string]struct{}, len(c.Models))
	for i, m := range c.Models {
		if m.ID == "" || m.Provider == "" {
			return fmt.Errorf("model %d: id and provider are required", i)
		}
		key := m.Provider + "/" + m.ID
		if _, dup := seen[key]; dup {
			return fmt.Errorf("duplicate model %s", key)
		}
		seen[key] = struct{}{}
		if m.Source != SourceCloud && m.Source != SourceLocal {
			return fmt.Errorf("model %s: invalid source %q", key, m.Source)
		}
		if m.QualityScore < 0 || m.QualityScore > 100 {
			return fmt.Errorf("model %s: quality_score %g out of [0,100]", key, m.QualityScore)
		}
		if m.InputPricePerMTok < 0 || m.OutputPricePerMTok < 0 {
			return fmt.Errorf("model %s: negative price", key)
		}
	}
	return nil
}

// ByProvider groups models by canonical provider name.
func (c *Catalog) ByProvider() map[string][]ModelInfo {
	out := map[string][]ModelInfo{}
	for _, m := range c.Models {
		out[m.Provider] = append(out[m.Provider], m)
	}
	return out
}
