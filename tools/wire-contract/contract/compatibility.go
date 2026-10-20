package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ValidateVersionTransition permits additive changes within a major version and
// requires a major bump for removals or changed existing shapes.
func ValidateVersionTransition(previousJSON []byte, current Snapshot) error {
	var previous map[string]any
	if err := json.Unmarshal(previousJSON, &previous); err != nil {
		return fmt.Errorf("decode previous wire contract: %w", err)
	}
	previousVersion, _ := previous["contractVersion"].(string)
	oldVersion, err := parseVersion(previousVersion)
	if err != nil {
		return fmt.Errorf("previous contract: %w", err)
	}
	newVersion, err := parseVersion(current.ContractVersion)
	if err != nil {
		return fmt.Errorf("new contract: %w", err)
	}
	if compareVersion(newVersion, oldVersion) <= 0 {
		return fmt.Errorf("contract version %s must be newer than %s", current.ContractVersion, previousVersion)
	}
	if newVersion[0] > oldVersion[0] {
		return nil
	}
	return validateAdditiveSnapshot(previous, current)
}

// ValidateAdditiveRefresh permits regenerating the current artifact only when
// its version and bytes are unchanged. Normal publication uses
// ValidateVersionTransition and requires a newer version for content changes.
func ValidateAdditiveRefresh(previousJSON []byte, current Snapshot) error {
	var previous map[string]any
	if err := json.Unmarshal(previousJSON, &previous); err != nil {
		return fmt.Errorf("decode previous wire contract: %w", err)
	}
	previousVersion, _ := previous["contractVersion"].(string)
	if previousVersion != current.ContractVersion {
		return fmt.Errorf(
			"additive refresh version %s does not match current artifact %s",
			current.ContractVersion,
			previousVersion,
		)
	}
	currentJSON, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return fmt.Errorf("encode current wire contract: %w", err)
	}
	currentJSON = append(currentJSON, '\n')
	if !bytes.Equal(previousJSON, currentJSON) {
		return errors.New("active wire contract changed without a version bump")
	}
	return nil
}

func validateAdditiveSnapshot(previous map[string]any, current Snapshot) error {
	currentJSON, err := json.Marshal(current)
	if err != nil {
		return err
	}
	var next map[string]any
	if err := json.Unmarshal(currentJSON, &next); err != nil {
		return err
	}
	delete(previous, "contractVersion")
	delete(next, "contractVersion")
	for _, metadata := range []string{"compatibility", "knownDrift"} {
		delete(previous, metadata)
		delete(next, metadata)
	}
	if !jsonSubset(previous, next) {
		return fmt.Errorf("wire contract removes or changes an existing shape; bump the major version")
	}
	return nil
}

func parseVersion(raw string) ([3]int, error) {
	var version [3]int
	parts := strings.Split(strings.TrimPrefix(raw, "v"), ".")
	if len(parts) != 3 {
		return version, fmt.Errorf("version %q is not semantic x.y.z", raw)
	}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return version, fmt.Errorf("version %q is not semantic x.y.z", raw)
		}
		version[i] = value
	}
	return version, nil
}

func compareVersion(a, b [3]int) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func jsonSubset(previous, next any) bool {
	switch old := previous.(type) {
	case map[string]any:
		current, ok := next.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range old {
			candidate, found := current[key]
			if !found || !jsonSubset(value, candidate) {
				return false
			}
		}
		return true
	case []any:
		current, ok := next.([]any)
		if !ok {
			return false
		}
		for i, value := range old {
			key, keyed := jsonIdentity(value)
			if !keyed {
				if i >= len(current) || !jsonSubset(value, current[i]) {
					return false
				}
				continue
			}
			matched := false
			for _, candidate := range current {
				candidateKey, candidateKeyed := jsonIdentity(candidate)
				if candidateKeyed && candidateKey == key && jsonSubset(value, candidate) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	default:
		return fmt.Sprint(previous) == fmt.Sprint(next)
	}
}

func jsonIdentity(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	for _, field := range []string{"field", "name", "operationID", "pattern", "ticket"} {
		if identity, ok := object[field].(string); ok && identity != "" {
			return field + ":" + identity, true
		}
	}
	return "", false
}
