package ranking

import (
	"fmt"
	"strings"
)

// Explain renders a deterministic, human-readable report of a routing
// decision: the policy applied, the selected model with its per-factor
// breakdown, and the ranked candidates. The same RankResult always
// produces byte-identical output, which makes it suitable for golden
// tests and for the "why this model?" response field.
func (r *RankResult) Explain() string {
	if r == nil {
		return "<nil result>"
	}
	var b strings.Builder

	fmt.Fprintf(&b, "preference=%s complexity=%.2f category=%s", r.Policy.Preference, r.Policy.Complexity, r.Policy.Category)
	if r.Policy.ToolRequired {
		b.WriteString(" tools=required")
	}
	if r.Policy.SoftReset {
		b.WriteString(" soft_reset=true")
	}
	fmt.Fprintf(&b, "\nweights: quality=%.2f speed=%.2f cost=%.2f  floor=%.2f window=%d\n",
		r.Policy.Weights.Quality, r.Policy.Weights.Speed, r.Policy.Weights.Cost,
		r.Policy.QualityFloor, r.Policy.SelectionWindow)
	if len(r.Policy.HardFilters) > 0 {
		fmt.Fprintf(&b, "hard filters: %s\n", strings.Join(r.Policy.HardFilters, ", "))
	}
	if len(r.Policy.SoftFilters) > 0 {
		fmt.Fprintf(&b, "soft filters: %s\n", strings.Join(r.Policy.SoftFilters, ", "))
	}

	if r.Selected == nil {
		b.WriteString("selected: (none)\n")
		return b.String()
	}
	fmt.Fprintf(&b, "selected: %s\n", r.Selected.String())

	if len(r.Candidates) > 0 {
		b.WriteString("ranking:\n")
		for i, c := range r.Candidates {
			marker := "  "
			if i == 0 {
				marker = ">>"
			}
			fmt.Fprintf(&b, "%s #%d %s\n", marker, i+1, c.String())
		}
	}
	if len(r.Filtered) > 0 {
		b.WriteString("filtered:\n")
		for _, f := range r.Filtered {
			fmt.Fprintf(&b, "   - %s [%s]\n", candidateKey(f.Model), f.Reason)
		}
	}
	return b.String()
}

// String renders one candidate as a single deterministic line.
func (c RankedCandidate) String() string {
	return fmt.Sprintf("%s quality=%.3f speed=%.3f cost=%.3f composite=%.3f (q=%.3f s=%.3f c=%.3f) blended=$%.4f/M est=%.4fc",
		candidateKey(c.Model),
		c.QualityScore, c.SpeedScore, c.CostScore, c.Composite,
		c.Breakdown.Quality, c.Breakdown.Speed, c.Breakdown.Cost,
		c.BlendedPricePerMTok, c.EstCostCents)
}
