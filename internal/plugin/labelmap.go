package plugin

import (
	"math"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Label is one model-emitted label with confidence in [0,1].
type Label struct {
	Name       string
	Confidence float64
}

// LabelTarget is one admitted LabelMap target. Exactly one of Tag or
// Kind+Relation is set, matching LabelMapSpec.
type LabelTarget struct {
	Label      string
	Confidence float64
	Tag        string
	Kind       string
	Relation   string
	EntityName string
}

// LabelMapResult is the application-ready, storage-neutral LabelMap result.
// Unmatched is the count of input labels that produced no surviving target.
type LabelMapResult struct {
	Tags      []LabelTarget
	Entities  []LabelTarget
	Unmatched int
}

// ResolveLabelMaps applies one plugin's LabelMaps by equality only after the
// fixed Core normalization. A LabelMap with MinConfidence zero uses
// fallbackFloor; explicit MinConfidence overrides it.
func ResolveLabelMaps(bundle Bundle, labels []Label, fallbackFloor float64) LabelMapResult {
	var out LabelMapResult
	for _, label := range labels {
		if math.IsNaN(label.Confidence) || math.IsInf(label.Confidence, 0) || label.Confidence < 0 || label.Confidence > 1 {
			out.Unmatched++
			continue
		}
		key := NormalizeLabelMapLabel(label.Name)
		if key == "" {
			out.Unmatched++
			continue
		}
		matched, applied := false, false
		for _, m := range bundle.LabelMaps {
			if NormalizeLabelMapLabel(m.Label) != key {
				continue
			}
			matched = true
			floor := m.MinConfidence
			if floor == 0 {
				floor = fallbackFloor
			}
			if label.Confidence < floor {
				continue
			}
			target := LabelTarget{
				Label:      m.Label,
				Confidence: label.Confidence,
				Tag:        strings.TrimSpace(m.Tag),
				Kind:       m.Kind,
				Relation:   m.Relation,
				EntityName: m.EntityName,
			}
			if target.Tag != "" {
				out.Tags = append(out.Tags, target)
			} else {
				out.Entities = append(out.Entities, target)
			}
			applied = true
		}
		if !matched || !applied {
			out.Unmatched++
		}
	}
	return out
}

func NormalizeLabelMapLabel(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(norm.NFC.String(s))), " ")
}
