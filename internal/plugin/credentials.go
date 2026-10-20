package plugin

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const pluginCredentialRefPrefix = "bfplugin-"

// ScopedCredentialRef binds a manifest-declared local credential name to its
// plugin using a portable filename. Invalid, traversal-shaped, or already-
// scoped input fails closed as the empty string.
func ScopedCredentialRef(pluginID ID, ref string) string {
	if !slugPattern.MatchString(string(pluginID)) || !validPortableCredentialName(ref) {
		return ""
	}
	pluginPart := hex.EncodeToString([]byte(pluginID))
	refPart := hex.EncodeToString([]byte(ref))
	result := pluginCredentialRefPrefix + pluginPart + "-" + refPart
	if len(result) > 255 {
		return ""
	}
	return result
}

// ParseScopedCredentialRef reverses ScopedCredentialRef for diagnostics and
// secure-store migrations. It rejects malformed or non-canonical encodings.
func ParseScopedCredentialRef(scoped string) (ID, string, error) {
	encoded, ok := strings.CutPrefix(scoped, pluginCredentialRefPrefix)
	if !ok {
		return "", "", errors.New("plugin credential ref is not scoped")
	}
	pluginHex, refHex, ok := strings.Cut(encoded, "-")
	if !ok || pluginHex == "" || refHex == "" {
		return "", "", errors.New("plugin credential ref has invalid encoding")
	}
	pluginBytes, err := hex.DecodeString(pluginHex)
	if err != nil {
		return "", "", errors.New("plugin credential ref has invalid plugin encoding")
	}
	refBytes, err := hex.DecodeString(refHex)
	if err != nil {
		return "", "", errors.New("plugin credential ref has invalid local-name encoding")
	}
	pluginID := ID(pluginBytes)
	ref := string(refBytes)
	if ScopedCredentialRef(pluginID, ref) != scoped {
		return "", "", errors.New("plugin credential ref is not canonical")
	}
	return pluginID, ref, nil
}

func validPortableCredentialName(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, pluginCredentialRefPrefix) {
		return false
	}
	if ref == "." || ref == ".." || strings.HasPrefix(ref, ".") {
		return false
	}
	if strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, " ") {
		return false
	}
	for _, r := range ref {
		if r < 0x20 || r > 0x7e || strings.ContainsRune(`<>:"/\\|?*`, r) {
			return false
		}
	}
	base, _, _ := strings.Cut(ref, ".")
	upper := strings.ToUpper(base)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" {
		return false
	}
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
		return false
	}
	return true
}

func validateBundleCredentialRefs(bundle Bundle) error {
	for _, scraper := range bundle.Scrapers {
		if scraper.AuthRef != "" && !validPortableCredentialName(scraper.AuthRef) {
			return fmt.Errorf("plugin %s scraper %s has invalid credential ref %q", bundle.ID, scraper.ID, scraper.AuthRef)
		}
	}
	for _, importer := range bundle.Importers {
		if importer.CredentialRef != "" && !validPortableCredentialName(importer.CredentialRef) {
			return fmt.Errorf("plugin %s importer %s has invalid credential ref %q", bundle.ID, importer.ID, importer.CredentialRef)
		}
	}
	return nil
}

func scopeBundleCredentialRefs(bundle *Bundle) {
	for i := range bundle.Scrapers {
		if bundle.Scrapers[i].AuthRef != "" {
			bundle.Scrapers[i].AuthRef = ScopedCredentialRef(bundle.ID, bundle.Scrapers[i].AuthRef)
		}
	}
	for i := range bundle.Importers {
		if bundle.Importers[i].CredentialRef != "" {
			bundle.Importers[i].CredentialRef = ScopedCredentialRef(bundle.ID, bundle.Importers[i].CredentialRef)
		}
	}
}
