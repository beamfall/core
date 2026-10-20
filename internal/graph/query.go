package graph

import (
	"fmt"
	"strings"

	"github.com/beamfall/core/internal/graphquery"
	"github.com/vektah/gqlparser/v2/ast"
)

// topLevelFields preserves the focused helper around the maintained parser.
func topLevelFields(query string) map[string]bool {
	return graphquery.TopLevelFields(query)
}

func selectedOperationFields(query, operationName string) (map[string]bool, error) {
	fields, err := graphquery.SelectedOperationFields(query, operationName)
	if err != nil {
		return nil, clientError(err.Error())
	}
	return fields, nil
}

type operationOccurrence struct {
	canonical   string
	responseKey string
	input       map[string]any
}

type operationPlan struct {
	kind        ast.Operation
	occurrences []operationOccurrence
}

func prepareOperation(req request) (operationPlan, error) {
	parsed, err := graphquery.PrepareOperation(req.Query, req.OperationName, req.Variables)
	if err != nil {
		return operationPlan{}, clientError(err.Error())
	}
	plan := operationPlan{kind: parsed.Kind}
	type mergedResponse struct {
		occurrence operationOccurrence
		included   bool
	}
	byResponseKey := map[string]*mergedResponse{}
	for _, occurrence := range parsed.Occurrences {
		handler, known := fieldHandlerRegistry[occurrence.Canonical]
		if !known || occurrence.Name != handler.wireName {
			return operationPlan{}, clientError("unknown top-level field(s): " + occurrence.Name)
		}
		if parsed.Kind == ast.Query && handler.kind != fieldRead {
			return operationPlan{}, clientError(fmt.Sprintf("field %q is not readable by a query operation", occurrence.Canonical))
		}
		if parsed.Kind == ast.Mutation && handler.kind != fieldMutation {
			return operationPlan{}, clientError(fmt.Sprintf("field %q is not writable by a mutation operation", occurrence.Canonical))
		}
		responseKey := handler.dataKey
		if occurrence.Aliased {
			responseKey = occurrence.ResponseKey
		}
		normalized := operationOccurrence{canonical: occurrence.Canonical, responseKey: responseKey, input: occurrence.Input}
		if prior, exists := byResponseKey[responseKey]; exists {
			if prior.occurrence.canonical != normalized.canonical || !graphquery.EquivalentInputs(prior.occurrence.input, normalized.input) {
				return operationPlan{}, clientError(fmt.Sprintf("conflicting selections for response key %q", responseKey))
			}
			if occurrence.Included && !prior.included {
				plan.occurrences = append(plan.occurrences, normalized)
				prior.included = true
			}
			continue
		}
		byResponseKey[responseKey] = &mergedResponse{occurrence: normalized, included: occurrence.Included}
		if occurrence.Included {
			plan.occurrences = append(plan.occurrences, normalized)
		}
	}
	return plan, nil
}

func (p operationPlan) fields() map[string]bool {
	fields := make(map[string]bool, len(p.occurrences))
	for _, occurrence := range p.occurrences {
		fields[occurrence.canonical] = true
	}
	return fields
}

// vars reads typed optional variables from a request. Each getter returns
// ok=false when the variable is absent or JSON-null; a present value of the
// wrong shape records a typing error (the first one wins) so call sites stay
// linear and check Err once.
type vars struct {
	values map[string]any
	err    error
}

func newVars(req request) *vars {
	return &vars{values: req.Variables}
}

func (v *vars) Err() error { return v.err }

func (v *vars) fail(name, expected string) {
	if v.err == nil {
		// A mistyped variable is client-caused: the message is wire-safe.
		v.err = clientError(name + " must be " + expected)
	}
}

// present resolves the absent-or-null check shared by every getter.
func (v *vars) present(name string) (any, bool) {
	value, ok := v.values[name]
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}

func (v *vars) Bool(name string) (bool, bool) {
	value, ok := v.present(name)
	if !ok {
		return false, false
	}
	flag, ok := value.(bool)
	if !ok {
		v.fail(name, "a boolean")
		return false, false
	}
	return flag, true
}

// Number returns a JSON number variable (decoded as float64).
func (v *vars) Number(name string) (float64, bool) {
	value, ok := v.present(name)
	if !ok {
		return 0, false
	}
	number, ok := value.(float64)
	if !ok {
		v.fail(name, "a number")
		return 0, false
	}
	return number, true
}

func (v *vars) String(name string) (string, bool) {
	value, ok := v.present(name)
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		v.fail(name, "a string")
		return "", false
	}
	return text, true
}

// StringList returns a list-of-strings variable; kind names the expected
// element in the error ("language tags").
func (v *vars) StringList(name, kind string) ([]string, bool) {
	value, ok := v.present(name)
	if !ok {
		return nil, false
	}
	items, ok := value.([]any)
	if !ok {
		v.fail(name, "a list of "+kind)
		return nil, false
	}
	list := make([]string, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok {
			v.fail(name, "a list of "+kind)
			return nil, false
		}
		list[i] = text
	}
	return list, true
}

// Lenient variable readers — the optional-argument posture every handler
// shares: an absent, JSON-null, or mistyped variable quietly yields the zero
// value. Handlers that require an argument check the zero value explicitly
// and answer with a stable client message.

// stringVar reads a string variable.
func stringVar(req request, name string) string {
	value, _ := req.Variables[name].(string)
	return value
}

// trimmedVariable reads a string variable and trims it — the shape almost
// every ID-carrying mutation argument uses.
func trimmedVariable(req request, name string) string {
	return strings.TrimSpace(stringVar(req, name))
}

// boolVar reads a boolean variable.
func boolVar(req request, name string) bool {
	value, _ := req.Variables[name].(bool)
	return value
}

// intVar reads a numeric variable (JSON numbers decode as float64).
func intVar(req request, name string) int {
	switch value := req.Variables[name].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

func boundedIntVar(req request, name string, fallback, maximum int) int {
	value := intVar(req, name)
	return clampGraphInt(value, fallback, maximum)
}

func nonNegativeIntVar(req request, name string) int {
	value := intVar(req, name)
	if value < 0 {
		return 0
	}
	return value
}

func boundedNonNegativeInt(value, maximum int) int {
	if value < 0 {
		return 0
	}
	if maximum > 0 && value > maximum {
		return maximum
	}
	return value
}

func clampGraphInt(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if maximum > 0 && value > maximum {
		return maximum
	}
	return value
}

// stringSliceVar reads an array-of-strings variable. Non-string elements and
// blank entries are dropped; nil when nothing usable remains.
func stringSliceVar(req request, name string) []string {
	raw, _ := req.Variables[name].([]any)
	values := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			values = append(values, strings.TrimSpace(text))
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

// stringMapVar reads an object-of-strings variable. Non-string values and
// blank keys/values are dropped like every other malformed variable; nil when
// nothing usable remains.
func stringMapVar(req request, name string) map[string]string {
	raw, _ := req.Variables[name].(map[string]any)
	values := map[string]string{}
	for key, value := range raw {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(text) == "" {
			continue
		}
		values[key] = text
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

// anyMapVar reads an object variable without coercing the value types.
// Used for declarative filter shapes that carry typed scalars or nested
// gte/lte objects.
func anyMapVar(req request, name string) map[string]any {
	raw, _ := req.Variables[name].(map[string]any)
	if len(raw) == 0 {
		return nil
	}
	return raw
}
