package scrape

// Response-path resolution for the two document shapes scrapers map from.
//
// JSON paths (http-json, http-graphql): dot-separated segments; a segment is
// an object key or, on arrays, a zero-based index — `data.0.name`. A key may
// carry a predicate selecting the first array element whose keys match every
// comma-separated condition — `artworks[type=3,language=eng].image`.
//
// Map paths (FieldMap, ArtworkMap and the search maps, plus CandidateIDPath)
// may list alternatives separated by "||": the first alternative that
// resolves to a non-empty value wins — `translated.name||name`.
//
// XML paths (rss): dot-separated segments resolved against the element tree
// with namespace *prefixes* kept verbatim (`itunes:image`). A numeric segment
// indexes the current match set (`item.0.title`). `name@attr` reads an
// attribute (`itunes:image@href`); a trailing name segment that matches no
// child element falls back to an attribute of the current element, so
// `item.0.enclosure.url` reads <enclosure url="…">. Element-name segments
// match the nearest descendants, so `item.0.title` works without spelling
// out `channel.`.

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

func resolveJSONPath(root any, path string) (string, bool) {
	node, ok := resolveJSONScalar(root, path)
	if !ok {
		return "", false
	}
	return scalarString(node)
}

func resolveJSONScalar(root any, path string) (any, bool) {
	if strings.TrimSpace(path) == "$" {
		return nil, false
	}
	node, ok := resolveJSONNode(root, path)
	if !ok {
		return nil, false
	}
	if _, ok := scalarString(node); !ok {
		return nil, false
	}
	return node, true
}

// resolveJSONNode walks the scraper path to an object, array or scalar.
// List paths can name the root with $ and filter all matches at the end.
func resolveJSONNode(root any, path string) (any, bool) {
	trimmed := strings.TrimSpace(path)
	if path == "" || trimmed == "$" {
		return root, true
	}
	if strings.HasPrefix(trimmed, "$[") {
		path = trimmed
	}
	segments, ok := splitJSONPath(path)
	if !ok {
		return nil, false
	}
	current := root
	for i, segment := range segments {
		name, conditions, filterAll, ok := jsonPathPredicate(segment)
		if !ok || filterAll && i != len(segments)-1 {
			return nil, false
		}
		if strings.TrimSpace(name) == "$" {
			if name != "$" || !filterAll || len(segments) != 1 {
				return nil, false
			}
		} else {
			if _, array := current.([]any); array && len(conditions) != 0 {
				return nil, false
			}
			current, ok = jsonPathValue(current, name)
			if !ok {
				return nil, false
			}
		}
		if len(conditions) != 0 {
			current, ok = selectJSONPredicate(current, conditions, filterAll)
			if !ok {
				return nil, false
			}
		}
	}
	return current, true
}

func jsonPathValue(node any, name string) (any, bool) {
	switch node := node.(type) {
	case map[string]any:
		value, ok := node[name]
		return value, ok
	case []any:
		index, err := strconv.Atoi(name)
		if err == nil && index >= 0 && index < len(node) {
			return node[index], true
		}
	}
	return nil, false
}

func splitJSONPath(path string) ([]string, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, false
	}
	var segments []string
	start, brackets := 0, 0
	for i, r := range path {
		switch r {
		case '[':
			brackets++
		case ']':
			if brackets == 0 {
				return nil, false
			}
			brackets--
		case '.':
			if brackets == 0 {
				segments = append(segments, path[start:i])
				start = i + 1
			}
		}
	}
	return append(segments, path[start:]), brackets == 0
}

func jsonPathPredicate(segment string) (name string, conditions [][2]string, filterAll, ok bool) {
	name, predicate, found := strings.Cut(segment, "[")
	if !found {
		return name, nil, false, name != ""
	}
	predicate, closed := strings.CutSuffix(predicate, "]")
	if name == "" || !closed {
		return "", nil, false, false
	}
	predicate, filterAll = strings.CutPrefix(predicate, "?")
	if filterAll {
		if _, balanced := splitJSONPath(predicate); !balanced {
			return "", nil, false, false
		}
	}
	for _, condition := range strings.Split(predicate, ",") {
		key, value, found := strings.Cut(condition, "=")
		if !found || key == "" {
			return "", nil, false, false
		}
		conditions = append(conditions, [2]string{key, value})
	}
	return name, conditions, filterAll, true
}

func selectJSONPredicate(node any, conditions [][2]string, filterAll bool) (any, bool) {
	elements, ok := node.([]any)
	if !ok {
		return nil, false
	}
	var matches []any
	for _, element := range elements {
		object, ok := element.(map[string]any)
		if !ok || !matchesConditions(object, conditions) {
			continue
		}
		if !filterAll {
			return element, true
		}
		matches = append(matches, element)
	}
	if filterAll {
		return matches, true
	}
	return nil, false
}

func matchesConditions(object map[string]any, conditions [][2]string) bool {
	for _, condition := range conditions {
		name, want := condition[0], condition[1]
		value, ok := object[name]
		if !ok {
			value = object
			for key := range strings.SplitSeq(name, ".") {
				value, ok = jsonPathValue(value, key)
				if !ok {
					return false
				}
			}
		}
		got, ok := scalarString(value)
		if number, numeric := value.(json.Number); numeric {
			// Compare the numeric value using the SDK's scalar spelling.
			n, err := number.Float64()
			if err == nil {
				if n == math.Trunc(n) && n >= math.MinInt64 && n < -float64(math.MinInt64) {
					got = strconv.FormatInt(int64(n), 10)
				} else {
					got = strconv.FormatFloat(n, 'g', -1, 64)
				}
			}
		}
		if !ok || got != want {
			return false
		}
	}
	return true
}

// pathAlternatives splits a map path on "||". A path without alternatives is
// returned whole, so existing paths resolve exactly as before.
func pathAlternatives(path string) []string {
	if !strings.Contains(path, "||") {
		return []string{path}
	}
	parts := strings.Split(path, "||")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	return parts
}

// resolveAlternatives returns the first alternative of path that resolves to
// a non-empty scalar. With a single alternative an empty value still counts,
// as it always has.
func resolveAlternatives(path string, resolve func(string) (any, bool)) (any, bool) {
	alternatives := pathAlternatives(path)
	for i, alternative := range alternatives {
		raw, ok := resolve(alternative)
		if !ok {
			continue
		}
		if value, _ := scalarString(raw); value == "" && i < len(alternatives)-1 {
			continue
		}
		return raw, true
	}
	return nil, false
}

func jsonScalar(value any) (string, bool) {
	return scalarString(value)
}

func scalarString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case bool:
		return strconv.FormatBool(v), true
	default:
		return "", false
	}
}

// xmlNode is a generic element-tree node. Names keep the raw prefix
// ("itunes:image") because feeds address extensions by prefix, not URI.
type xmlNode struct {
	name     string
	attrs    map[string]string
	text     string
	children []*xmlNode
}

func parseXML(data []byte) (*xmlNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	root := &xmlNode{}
	stack := []*xmlNode{root}
	for {
		// RawToken keeps namespace prefixes as written in the document.
		token, err := decoder.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			node := &xmlNode{name: rawName(t.Name)}
			if len(t.Attr) > 0 {
				node.attrs = make(map[string]string, len(t.Attr))
				for _, attr := range t.Attr {
					node.attrs[rawName(attr.Name)] = attr.Value
				}
			}
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, node)
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			stack[len(stack)-1].text += string(t)
		}
	}
	return root, nil
}

func rawName(name xml.Name) string {
	if name.Space != "" {
		return name.Space + ":" + name.Local
	}
	return name.Local
}

func resolveXMLPath(root *xmlNode, path string) (string, bool) {
	segments := strings.Split(path, ".")
	current := []*xmlNode{root}
	for i, segment := range segments {
		last := i == len(segments)-1
		if index, err := strconv.Atoi(segment); err == nil {
			if index < 0 || index >= len(current) {
				return "", false
			}
			current = current[index : index+1]
			continue
		}
		name, attr := segment, ""
		if at := strings.IndexByte(segment, '@'); at >= 0 {
			name, attr = segment[:at], segment[at+1:]
		}
		if name != "" {
			matched := findElements(current, name)
			if matched == nil {
				// Trailing bare name: fall back to an attribute of the
				// current element (e.g. enclosure.url → <enclosure url=…>).
				if last && attr == "" && len(current) == 1 {
					value, ok := current[0].attrs[name]
					return value, ok
				}
				return "", false
			}
			current = matched
		}
		if attr != "" {
			if !last || len(current) == 0 {
				return "", false
			}
			value, ok := current[0].attrs[attr]
			return value, ok
		}
	}
	if len(current) == 0 {
		return "", false
	}
	text := strings.TrimSpace(current[0].text)
	return text, text != ""
}

// findElements returns the nearest descendants of scope named name: a
// breadth-first search that stops at the first depth producing any match,
// preserving document order so numeric indexes are stable.
func findElements(scope []*xmlNode, name string) []*xmlNode {
	level := make([]*xmlNode, 0, len(scope))
	for _, node := range scope {
		level = append(level, node.children...)
	}
	for len(level) > 0 {
		var matched, next []*xmlNode
		for _, node := range level {
			if node.name == name {
				matched = append(matched, node)
			}
			next = append(next, node.children...)
		}
		if matched != nil {
			return matched
		}
		level = next
	}
	return nil
}
