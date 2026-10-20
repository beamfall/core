package graphshape

import (
	"reflect"
	"slices"
	"strings"
	"time"
)

var timeType = reflect.TypeOf(time.Time{})

// Field is one node in a JSON-shaped reflection tree.
type Field struct {
	Name      string  `json:"name,omitempty"`
	Kind      string  `json:"kind"`
	Optional  bool    `json:"optional,omitempty"`
	Nullable  bool    `json:"nullable,omitempty"`
	Recursive bool    `json:"recursive,omitempty"`
	Elem      *Field  `json:"elem,omitempty"`
	Children  []Field `json:"children,omitempty"`
}

// Shape describes v's recursive JSON serialization shape.
func Shape(v any) Field {
	return shape(reflect.TypeOf(v), nil)
}

func shape(t reflect.Type, stack []reflect.Type) Field {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return Field{Kind: "scalar"}
		}
		elem := shape(t.Elem(), stack)
		return Field{Kind: "list", Elem: &elem, Children: elem.Children}
	case reflect.Map:
		elem := shape(t.Elem(), stack)
		return Field{Kind: "map", Elem: &elem}
	case reflect.Struct:
		if t == timeType {
			return Field{Kind: "scalar"}
		}
		if slices.Contains(stack, t) {
			return Field{Kind: "object", Recursive: true}
		}
		return Field{Kind: "object", Children: structFields(t, append(stack, t))}
	default:
		return Field{Kind: "scalar"}
	}
}

func structFields(t reflect.Type, stack []reflect.Type) []Field {
	var fields []Field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, optional, omit := jsonField(f)
		if omit {
			continue
		}
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && ft != timeType {
				fields = append(fields, structFields(ft, stack)...)
				continue
			}
			name = strings.ToLower(ft.Name())
		}
		if name == "" {
			name = f.Name
		}
		node := shape(f.Type, stack)
		node.Name = name
		node.Optional = optional
		node.Nullable = nullableJSON(f.Type)
		fields = append(fields, node)
	}
	slices.SortFunc(fields, func(a, b Field) int {
		return strings.Compare(a.Name, b.Name)
	})
	return fields
}

func nullableJSON(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		return true
	}
	switch t.Kind() {
	case reflect.Interface, reflect.Map, reflect.Slice:
		return true
	default:
		return false
	}
}

func jsonField(f reflect.StructField) (name string, optional bool, omit bool) {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return "", false, false
	}
	if tag == "-" {
		return "", false, true
	}
	name = tag
	for i, part := range strings.Split(tag, ",") {
		if i == 0 {
			name = part
			continue
		}
		if part == "omitempty" {
			optional = true
		}
	}
	return name, optional, false
}
