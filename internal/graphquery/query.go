// Package graphquery validates and selects GraphQL executable documents for
// the hand-written graph dispatcher.
package graphquery

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Documented request-cost budgets, enforced by PrepareOperation before the
// caller authenticates, allocates audit state, or dispatches resolvers. A
// document over any budget is rejected with one stable client-caused error and
// produces no store or resolver side effect. Each maximum is an exact
// boundary: a document sitting precisely at the limit is accepted.
//
//   - maxSelectionDepth bounds selection nesting; fields, inline fragments,
//     and fragment spreads each add one level.
//   - maxDocumentSelections bounds the total selection nodes visited during
//     validation. Fragment bodies are counted once per spread, so this is
//     also the fragment-amplification bound.
//   - maxExpandedOccurrences bounds top-level field occurrences after alias,
//     repetition, and fragment expansion.
//   - maxReachableFragments bounds the distinct fragment definitions
//     reachable from the selected operation.
const (
	maxSelectionDepth      = 64
	maxDocumentSelections  = 4096
	maxExpandedOccurrences = 64
	maxReachableFragments  = 64
)

// Occurrence is one included top-level field occurrence in document order.
// Input is an occurrence-owned resolver-variable map: request variables plus
// materialized operation defaults, overlaid by this field's arguments.
type Occurrence struct {
	Name        string
	Canonical   string
	ResponseKey string
	Aliased     bool
	Included    bool
	Input       map[string]any
}

// Operation is the selected, validated executable operation.
type Operation struct {
	Kind        ast.Operation
	Occurrences []Occurrence
}

// PrepareOperation parses, validates, expands, and evaluates one operation.
// Schema-specific field membership/effect validation remains in package graph.
func PrepareOperation(query, operationName string, variables map[string]any) (Operation, error) {
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		return Operation{}, fmt.Errorf("invalid GraphQL document")
	}
	operations, fragments, err := definitions(doc)
	if err != nil {
		return Operation{}, err
	}
	operation, err := selectOperation(doc, operations, operationName)
	if err != nil {
		return Operation{}, err
	}
	if operation.Operation != ast.Query && operation.Operation != ast.Mutation {
		return Operation{}, fmt.Errorf("unsupported operation kind %q", operation.Operation)
	}
	if len(operation.Directives) != 0 {
		return Operation{}, fmt.Errorf("directives are not supported on operations")
	}
	base, declared, err := materializeVariables(operation, variables)
	if err != nil {
		return Operation{}, err
	}
	state := validationState{fragments: fragments, declaredVariables: declared, reachedFragments: map[string]bool{}}
	if err := state.validateSelections(operation.SelectionSet, 1, map[string]bool{}); err != nil {
		return Operation{}, err
	}
	root := "Query"
	if operation.Operation == ast.Mutation {
		root = "Mutation"
	}
	collector := occurrenceCollector{fragments: fragments, variables: base, rootType: root}
	if err := collector.collect(operation.SelectionSet, true, map[string]bool{}); err != nil {
		return Operation{}, err
	}
	return Operation{Kind: operation.Operation, Occurrences: collector.occurrences}, nil
}

func definitions(doc *ast.QueryDocument) (map[string]*ast.OperationDefinition, map[string]*ast.FragmentDefinition, error) {
	operations := make(map[string]*ast.OperationDefinition, len(doc.Operations))
	for _, operation := range doc.Operations {
		if operation.Name == "" {
			continue
		}
		if operations[operation.Name] != nil {
			return nil, nil, fmt.Errorf("duplicate operation name %q", operation.Name)
		}
		operations[operation.Name] = operation
	}
	fragments := make(map[string]*ast.FragmentDefinition, len(doc.Fragments))
	for _, fragment := range doc.Fragments {
		if fragments[fragment.Name] != nil {
			return nil, nil, fmt.Errorf("duplicate fragment name %q", fragment.Name)
		}
		fragments[fragment.Name] = fragment
	}
	return operations, fragments, nil
}

func selectOperation(doc *ast.QueryDocument, operations map[string]*ast.OperationDefinition, operationName string) (*ast.OperationDefinition, error) {
	if len(doc.Operations) == 0 {
		return nil, fmt.Errorf("document must contain an operation")
	}
	if len(doc.Operations) > 1 {
		for _, operation := range doc.Operations {
			if operation.Name == "" {
				return nil, fmt.Errorf("anonymous operation must be the only operation")
			}
		}
	}
	if operationName != "" {
		if operation := operations[operationName]; operation != nil {
			return operation, nil
		}
		return nil, fmt.Errorf("unknown operationName %q", operationName)
	}
	if len(doc.Operations) > 1 {
		return nil, fmt.Errorf("operationName is required for multiple operations")
	}
	return doc.Operations[0], nil
}

func materializeVariables(operation *ast.OperationDefinition, provided map[string]any) (map[string]any, map[string]bool, error) {
	values := cloneMap(provided)
	declared := make(map[string]bool, len(operation.VariableDefinitions))
	for _, definition := range operation.VariableDefinitions {
		if declared[definition.Variable] {
			return nil, nil, fmt.Errorf("duplicate variable definition %q", definition.Variable)
		}
		declared[definition.Variable] = true
		if _, exists := values[definition.Variable]; exists || definition.DefaultValue == nil {
			continue
		}
		value, err := decodeValue(definition.DefaultValue, values)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid default for variable %q", definition.Variable)
		}
		values[definition.Variable] = value
	}
	return values, declared, nil
}

type validationState struct {
	fragments         map[string]*ast.FragmentDefinition
	declaredVariables map[string]bool
	selections        int
	reachedFragments  map[string]bool
}

func (s *validationState) validateSelections(selections ast.SelectionSet, depth int, visiting map[string]bool) error {
	if len(selections) == 0 {
		return nil
	}
	if depth > maxSelectionDepth {
		return fmt.Errorf("GraphQL selection depth exceeds %d", maxSelectionDepth)
	}
	for _, selection := range selections {
		s.selections++
		if s.selections > maxDocumentSelections {
			return fmt.Errorf("GraphQL selection work exceeds %d", maxDocumentSelections)
		}
		switch selection := selection.(type) {
		case *ast.Field:
			if err := validateArguments(selection.Arguments, s.declaredVariables); err != nil {
				return fmt.Errorf("field %q: %w", selection.Name, err)
			}
			if _, err := includeSelection(selection.Directives, nil, s.declaredVariables, false); err != nil {
				return fmt.Errorf("field %q: %w", selection.Name, err)
			}
			if err := s.validateSelections(selection.SelectionSet, depth+1, visiting); err != nil {
				return err
			}
		case *ast.InlineFragment:
			if _, err := includeSelection(selection.Directives, nil, s.declaredVariables, false); err != nil {
				return err
			}
			if err := s.validateSelections(selection.SelectionSet, depth+1, visiting); err != nil {
				return err
			}
		case *ast.FragmentSpread:
			if _, err := includeSelection(selection.Directives, nil, s.declaredVariables, false); err != nil {
				return err
			}
			fragment := s.fragments[selection.Name]
			if fragment == nil {
				return fmt.Errorf("undefined fragment %q", selection.Name)
			}
			if len(fragment.Directives) != 0 {
				return fmt.Errorf("directives are not supported on fragment definitions")
			}
			if !s.reachedFragments[selection.Name] {
				s.reachedFragments[selection.Name] = true
				if len(s.reachedFragments) > maxReachableFragments {
					return fmt.Errorf("GraphQL reachable fragments exceed %d", maxReachableFragments)
				}
			}
			if visiting[selection.Name] {
				return fmt.Errorf("fragment cycle involving %q", selection.Name)
			}
			next := cloneBoolMap(visiting)
			next[selection.Name] = true
			if err := s.validateSelections(fragment.SelectionSet, depth+1, next); err != nil {
				return err
			}
		}
	}
	return nil
}

type occurrenceCollector struct {
	fragments   map[string]*ast.FragmentDefinition
	variables   map[string]any
	rootType    string
	occurrences []Occurrence
}

func (c *occurrenceCollector) collect(selections ast.SelectionSet, parentIncluded bool, visiting map[string]bool) error {
	for _, selection := range selections {
		switch selection := selection.(type) {
		case *ast.Field:
			included, err := includeSelection(selection.Directives, c.variables, nil, true)
			if err != nil {
				return fmt.Errorf("field %q: %w", selection.Name, err)
			}
			input := cloneMap(c.variables)
			for _, argument := range selection.Arguments {
				value, err := decodeValue(argument.Value, c.variables)
				if err != nil {
					return fmt.Errorf("field %q argument %q is invalid", selection.Name, argument.Name)
				}
				input[argument.Name] = value
			}
			responseKey := selection.Name
			aliased := selection.Alias != "" && selection.Alias != selection.Name
			if aliased {
				responseKey = selection.Alias
			}
			c.occurrences = append(c.occurrences, Occurrence{
				Name:        selection.Name,
				Canonical:   strings.ToLower(selection.Name),
				ResponseKey: responseKey,
				Aliased:     aliased,
				Included:    parentIncluded && included,
				Input:       input,
			})
			if len(c.occurrences) > maxExpandedOccurrences {
				return fmt.Errorf("GraphQL expanded field work exceeds %d", maxExpandedOccurrences)
			}
		case *ast.InlineFragment:
			included, err := includeSelection(selection.Directives, c.variables, nil, true)
			if err != nil {
				return err
			}
			if selection.TypeCondition != "" && selection.TypeCondition != c.rootType {
				return fmt.Errorf("fragment type condition %q does not match %s", selection.TypeCondition, c.rootType)
			}
			if err := c.collect(selection.SelectionSet, parentIncluded && included, visiting); err != nil {
				return err
			}
		case *ast.FragmentSpread:
			included, err := includeSelection(selection.Directives, c.variables, nil, true)
			if err != nil {
				return err
			}
			fragment := c.fragments[selection.Name]
			if fragment.TypeCondition != c.rootType {
				return fmt.Errorf("fragment %q type condition %q does not match %s", selection.Name, fragment.TypeCondition, c.rootType)
			}
			if visiting[selection.Name] {
				return fmt.Errorf("fragment cycle involving %q", selection.Name)
			}
			next := cloneBoolMap(visiting)
			next[selection.Name] = true
			if err := c.collect(fragment.SelectionSet, parentIncluded && included, next); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateArguments(arguments ast.ArgumentList, declared map[string]bool) error {
	seen := map[string]bool{}
	for _, argument := range arguments {
		if seen[argument.Name] {
			return fmt.Errorf("duplicate argument %q", argument.Name)
		}
		seen[argument.Name] = true
		if err := validateValueVariables(argument.Value, declared); err != nil {
			return err
		}
	}
	return nil
}

func validateValueVariables(value *ast.Value, declared map[string]bool) error {
	if value == nil {
		return nil
	}
	if value.Kind == ast.Variable && !declared[value.Raw] {
		return fmt.Errorf("undefined variable %q", value.Raw)
	}
	for _, child := range value.Children {
		if err := validateValueVariables(child.Value, declared); err != nil {
			return err
		}
	}
	return nil
}

func includeSelection(directives ast.DirectiveList, variables map[string]any, declared map[string]bool, evaluate bool) (bool, error) {
	included := true
	seen := map[string]bool{}
	for _, directive := range directives {
		if directive.Name != "skip" && directive.Name != "include" {
			return false, fmt.Errorf("unsupported directive @%s", directive.Name)
		}
		if seen[directive.Name] {
			return false, fmt.Errorf("duplicate directive @%s", directive.Name)
		}
		seen[directive.Name] = true
		if len(directive.Arguments) != 1 || directive.Arguments[0].Name != "if" {
			return false, fmt.Errorf("directive @%s requires exactly one if argument", directive.Name)
		}
		if declared != nil {
			if err := validateValueVariables(directive.Arguments[0].Value, declared); err != nil {
				return false, err
			}
		}
		if !evaluate {
			continue
		}
		value, err := decodeValue(directive.Arguments[0].Value, variables)
		if err != nil {
			return false, fmt.Errorf("directive @%s has invalid if argument", directive.Name)
		}
		condition, ok := value.(bool)
		if !ok {
			return false, fmt.Errorf("directive @%s if argument must be a boolean", directive.Name)
		}
		if directive.Name == "skip" && condition || directive.Name == "include" && !condition {
			included = false
		}
	}
	return included, nil
}

func decodeValue(value *ast.Value, variables map[string]any) (any, error) {
	decoded, err := value.Value(variables)
	if err != nil {
		return nil, err
	}
	return normalizeJSONNumber(decoded), nil
}

func normalizeJSONNumber(value any) any {
	switch value := value.(type) {
	case int64:
		return float64(value)
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = normalizeJSONNumber(value[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = normalizeJSONNumber(item)
		}
		return out
	default:
		return value
	}
}

func cloneMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneValue(value any) any {
	switch value := value.(type) {
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = cloneValue(value[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = cloneValue(item)
		}
		return out
	default:
		return value
	}
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	out := make(map[string]bool, len(values)+1)
	for key, value := range values {
		out[key] = value
	}
	return out
}

// EquivalentInputs reports structural equality for field-merging validation.
func EquivalentInputs(a, b map[string]any) bool { return reflect.DeepEqual(a, b) }

// SelectedOperationFields retains the compatibility helper used by focused
// tests; production callers use PrepareOperation and preserve occurrences.
func SelectedOperationFields(query, operationName string) (map[string]bool, error) {
	operation, err := PrepareOperation(query, operationName, nil)
	if err != nil {
		return nil, err
	}
	fields := map[string]bool{}
	for _, occurrence := range operation.Occurrences {
		fields[occurrence.Canonical] = true
	}
	return fields, nil
}

// TopLevelFields preserves the small helper used by focused tests.
func TopLevelFields(query string) map[string]bool {
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		return map[string]bool{}
	}
	operations, fragments, err := definitions(doc)
	if err != nil {
		return map[string]bool{}
	}
	operation, err := selectOperation(doc, operations, "")
	if err != nil {
		return map[string]bool{}
	}
	fields := map[string]bool{}
	collectSyntacticFields(operation.SelectionSet, fragments, fields, map[string]bool{})
	return fields
}

func collectSyntacticFields(selections ast.SelectionSet, fragments map[string]*ast.FragmentDefinition, fields map[string]bool, visiting map[string]bool) {
	for _, selection := range selections {
		switch selection := selection.(type) {
		case *ast.Field:
			fields[strings.ToLower(selection.Name)] = true
		case *ast.InlineFragment:
			collectSyntacticFields(selection.SelectionSet, fragments, fields, visiting)
		case *ast.FragmentSpread:
			if visiting[selection.Name] || fragments[selection.Name] == nil {
				continue
			}
			next := cloneBoolMap(visiting)
			next[selection.Name] = true
			collectSyntacticFields(fragments[selection.Name].SelectionSet, fragments, fields, next)
		}
	}
}
