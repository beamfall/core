package plugin

import (
	"encoding/json"
	"testing"
)

func FuzzLocaleStringJSON(f *testing.F) {
	f.Add([]byte(`"Fresh Entries"`))
	f.Add([]byte(`{"en":"Fresh Entries","fr":"Entrees recentes"}`))
	f.Add([]byte(`{"fr":"Sans anglais"}`))
	f.Add([]byte(`42`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var s LocaleString
		if err := json.Unmarshal(data, &s); err != nil {
			return
		}
		_ = validateLocaleString(s)
		_ = s.Resolve("fr-CA")
		if _, err := json.Marshal(s); err != nil {
			t.Fatalf("marshal locale string: %v", err)
		}
	})
}

func FuzzValidateBundleJSON(f *testing.F) {
	valid, err := json.Marshal(validBundle())
	if err != nil {
		f.Fatalf("marshal valid bundle seed: %v", err)
	}
	f.Add(valid)
	f.Add([]byte(`{"ID":"starlog","DisplayName":"Starlog","MediaLabel":"Entry"}`))
	f.Add([]byte(`{"ID":"bad id","DisplayName":{"fr":"No English"},"MediaLabel":""}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var bundle Bundle
		if err := json.Unmarshal(data, &bundle); err != nil {
			return
		}
		_ = Validate(bundle)
	})
}
