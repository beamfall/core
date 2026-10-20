package pathmap

// PreviewRow is one sample source path shown with the effect of a Ruleset: the
// rewritten path and the index of the rule that fired (RuleIndex == -1 when no
// rule matched). The wizard renders these so a mapping is verified against real
// sample paths BEFORE apply, rather than surfacing as a match-rate collapse
// after.
type PreviewRow struct {
	Source    string
	Rewritten string
	RuleIndex int
}

// Preview applies the Ruleset to each sample source path and returns a row per
// sample in input order. It mutates nothing — it is the read-only "what would
// this ruleset do" view the wizard shows before the operator commits.
func (rs *Ruleset) Preview(samples []string) []PreviewRow {
	rows := make([]PreviewRow, 0, len(samples))
	for _, source := range samples {
		rows = append(rows, rs.previewOne(source))
	}
	return rows
}

// previewOne resolves one sample to its rewritten form and the rule index that
// produced it. It re-runs the winning-rule search so the reported index matches
// exactly the rule Rewrite would apply (longest match, ties by declared order).
func (rs *Ruleset) previewOne(source string) PreviewRow {
	rewritten, ok := rs.Rewrite(source)
	if !ok {
		return PreviewRow{Source: source, Rewritten: source, RuleIndex: -1}
	}
	return PreviewRow{Source: source, Rewritten: rewritten, RuleIndex: rs.winningRule(source)}
}
