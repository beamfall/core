package plugin

import (
	"reflect"
	"strings"
	"testing"
)

func TestBundleManifestHasNoPluginStandingFields(t *testing.T) {
	forbidden := []string{"author", "publisher", "vendor", "provenance", "authority", "signature", "trust"}
	tests := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{"server bundle", reflect.TypeOf(Bundle{}), ""},
		{"detector seed", reflect.TypeOf(struct{ PluginPublisherID string }{}), "PluginPublisherID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			for i := range tt.typ.NumField() {
				field := tt.typ.Field(i)
				if !field.IsExported() {
					continue
				}
				for _, term := range forbidden {
					if strings.Contains(strings.ToLower(field.Name), term) {
						got = field.Name
						break
					}
				}
			}
			if got != tt.want {
				t.Fatalf("forbidden plugin-standing field = %q, want %q", got, tt.want)
			}
		})
	}
}
