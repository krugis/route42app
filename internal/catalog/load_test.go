package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEmbedded(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models) < 200 {
		t.Errorf("embedded catalog has %d models, expected 200+", len(c.Models))
	}
	if c.SnapshotDate == "" {
		t.Error("snapshot_date missing")
	}

	byProv := c.ByProvider()
	for _, p := range []string{"openai", "anthropic", "gemini", "mistral", "groq", "deepseek"} {
		if len(byProv[p]) == 0 {
			t.Errorf("no models for provider %s", p)
		}
	}

	// The router needs a meaningful number of quality-scored and
	// tool-capable models to do its job.
	quality, tools := 0, 0
	for _, m := range c.Models {
		if m.QualityScore > 0 {
			quality++
		}
		if m.SupportsTools {
			tools++
		}
	}
	if quality < 50 {
		t.Errorf("only %d models with quality scores", quality)
	}
	if tools < 100 {
		t.Errorf("only %d tool-capable models", tools)
	}
}

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFileOverride(t *testing.T) {
	path := writeCatalog(t, `{"schema_version":1,"snapshot_date":"2026-07-04","models":[
		{"id":"m1","provider":"openai","source":"cloud","quality_score":50,"input_price_per_mtok":1,"output_price_per_mtok":2}]}`)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models) != 1 || c.Models[0].ID != "m1" {
		t.Errorf("catalog = %+v", c)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"wrong schema", `{"schema_version":2,"models":[{"id":"a","provider":"p","source":"cloud"}]}`, "schema_version"},
		{"no models", `{"schema_version":1,"models":[]}`, "no models"},
		{"missing id", `{"schema_version":1,"models":[{"provider":"p","source":"cloud"}]}`, "required"},
		{"duplicate", `{"schema_version":1,"models":[{"id":"a","provider":"p","source":"cloud"},{"id":"a","provider":"p","source":"cloud"}]}`, "duplicate"},
		{"bad source", `{"schema_version":1,"models":[{"id":"a","provider":"p","source":"space"}]}`, "source"},
		{"quality range", `{"schema_version":1,"models":[{"id":"a","provider":"p","source":"cloud","quality_score":101}]}`, "quality_score"},
		{"negative price", `{"schema_version":1,"models":[{"id":"a","provider":"p","source":"cloud","input_price_per_mtok":-1}]}`, "price"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadFile(writeCatalog(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestIsFree(t *testing.T) {
	free := ModelInfo{InputPricePerMTok: 0, OutputPricePerMTok: 0}
	paid := ModelInfo{InputPricePerMTok: 0, OutputPricePerMTok: 0.5}
	if !free.IsFree() || paid.IsFree() {
		t.Error("IsFree misclassifies")
	}
}
