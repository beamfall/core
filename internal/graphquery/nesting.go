package graphquery

import (
	"fmt"
	"strings"
)

// maxValueDepth bounds list, input-object, and list-type nesting inside
// arguments and variable definitions. Like maxSelectionDepth it is enforced
// lexically before the parser runs, because gqlparser recurses once per
// nesting level and the request body cap alone allows a stack of well over
// a hundred megabytes per in-flight anonymous request.
const maxValueDepth = 64

// nesting is the lexical nesting profile of a GraphQL document.
type nesting struct {
	// selection is the deepest selection-set nesting: braces opened outside
	// argument or variable-definition parentheses.
	selection int
	// value is the deepest nesting of brackets, and of braces opened inside
	// parentheses (input objects, lists, and list types).
	value int
	// unbalanced reports a closing brace or bracket with no opener. No
	// executable document contains one.
	unbalanced bool
}

// scanNesting measures nesting in one linear pass without allocating. It
// skips strings, block strings, and comments with the same delimiters as the
// gqlparser lexer. Where the two could disagree the document is malformed,
// and the parser stops at the first malformed token, so the counts up to
// that point still bound the parser's recursion.
func scanNesting(query string) nesting {
	var result nesting
	selection, value := 0, 0
	parens := 0
	for i := 0; i < len(query); i++ {
		switch query[i] {
		case '#':
			// A comment runs to the first control character other than tab.
			for i+1 < len(query) && (query[i+1] > 0x1f || query[i+1] == '\t') {
				i++
			}
		case '"':
			i = skipString(query, i)
		case '(':
			parens++
		case ')':
			parens = max(parens-1, 0)
		case '{':
			if parens > 0 {
				value++
				result.value = max(result.value, value)
			} else {
				selection++
				result.selection = max(result.selection, selection)
			}
		case '[':
			value++
			result.value = max(result.value, value)
		case '}':
			if parens > 0 {
				value--
			} else {
				selection--
			}
			if value < 0 || selection < 0 {
				result.unbalanced = true
				return result
			}
		case ']':
			value--
			if value < 0 {
				result.unbalanced = true
				return result
			}
		}
	}
	return result
}

// skipString returns the index of the last byte of the string or block
// string that opens at query[start], or len(query)-1 when it is unterminated.
// A block string closes at the first run of three or more quotes, consuming
// the whole run, as in the gqlparser lexer.
func skipString(query string, start int) int {
	if !strings.HasPrefix(query[start:], `"""`) {
		for i := start + 1; i < len(query); i++ {
			switch query[i] {
			case '\\':
				i++
			case '"':
				return i
			}
		}
		return len(query) - 1
	}
	for i := start + 3; i < len(query); i++ {
		if query[i] == '"' {
			run := 1
			for i+run < len(query) && query[i+run] == '"' {
				run++
			}
			if run >= 3 {
				return i + run - 1
			}
		}
		if strings.HasPrefix(query[i:], `\"""`) {
			i += 3
		}
	}
	return len(query) - 1
}

// checkNesting rejects a document whose nesting exceeds a budget before it
// reaches the parser.
func checkNesting(query string) error {
	n := scanNesting(query)
	switch {
	case n.selection > maxSelectionDepth:
		return fmt.Errorf("GraphQL selection depth exceeds %d", maxSelectionDepth)
	case n.unbalanced, n.value > maxValueDepth:
		return fmt.Errorf("invalid GraphQL document")
	}
	return nil
}
