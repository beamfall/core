package libencrypt

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// ObjectContext is the externally supplied identity every v2 at-rest object is
// cryptographically bound to. The caller — not
// the file, not its path, and never the header — declares which object it is
// reading or writing; the v2 object key and AAD are derived from this context,
// so ciphertext presented under the wrong identity fails authentication before
// any plaintext is returned.
//
// Callers derive ImmutableObjectID from persisted generic identifiers (content
// fingerprints, library ids, backup ids, ordinals) — NEVER from file paths, so a
// rename with unchanged identity keeps decrypting and a swapped/spliced object
// does not.
type ObjectContext struct {
	// LibraryID is the owning scope (an opaque app-generated library id, or a
	// fixed non-secret scope constant for non-library objects such as backups).
	LibraryID string
	// ObjectClass is one of the closed ObjectClass constants below.
	ObjectClass ObjectClass
	// ImmutableObjectID is the object's persisted, rename-stable identity within
	// (LibraryID, ObjectClass) — e.g. a content fingerprint or a backup id.
	ImmutableObjectID string
}

// ObjectClass is the closed, domain-agnostic vocabulary of encrypted object
// classes. Only these four values are valid; the encoder rejects anything else
// so two callers can never improvise colliding class strings.
type ObjectClass string

const (
	// ObjectClassMediaBytes is an original Media byte object.
	ObjectClassMediaBytes ObjectClass = "media-bytes"
	// ObjectClassEncryptedLibraryDB is the per-Encrypted-Library metadata DB file.
	ObjectClassEncryptedLibraryDB ObjectClass = "encrypted-library-db"
	// ObjectClassGeneratedAsset is a derived/generated asset file.
	ObjectClassGeneratedAsset ObjectClass = "generated-asset"
	// ObjectClassBackupPayload is an off-site backup payload stream.
	ObjectClassBackupPayload ObjectClass = "backup-payload"
)

// validObjectClass is the closed-set membership check for ObjectClass.
func validObjectClass(c ObjectClass) bool {
	switch c {
	case ObjectClassMediaBytes, ObjectClassEncryptedLibraryDB, ObjectClassGeneratedAsset, ObjectClassBackupPayload:
		return true
	}
	return false
}

// objectContextFieldMax is the per-field byte-length ceiling (the ticket's exact
// contract: nonempty valid UTF-8, at most 4096 bytes).
const objectContextFieldMax = 4096

// ErrObjectContext is returned when an ObjectContext is structurally invalid
// (empty field, invalid UTF-8, oversize field, or unknown ObjectClass). It
// deliberately never echoes the offending value.
var ErrObjectContext = errors.New("libencrypt: invalid object context")

// MediaObjectContext builds the ObjectContext for one original Media byte
// object, identified by its persisted content fingerprint (never its path).
func MediaObjectContext(libraryID, contentFingerprint string) ObjectContext {
	return ObjectContext{
		LibraryID:         libraryID,
		ObjectClass:       ObjectClassMediaBytes,
		ImmutableObjectID: contentFingerprint,
	}
}

// LibraryDBObjectContext builds the ObjectContext for the per-library encrypted
// metadata DB. The DB is a per-library singleton, so its immutable identity is a
// fixed label under the library scope.
func LibraryDBObjectContext(libraryID string) ObjectContext {
	return ObjectContext{
		LibraryID:         libraryID,
		ObjectClass:       ObjectClassEncryptedLibraryDB,
		ImmutableObjectID: "encrypted-library-db",
	}
}

// GeneratedAssetObjectContext builds the ObjectContext for one Generated Asset
// file, identified by the persisted content fingerprint of the media it derives
// from plus the asset's name within that fingerprint's asset set.
func GeneratedAssetObjectContext(libraryID, contentFingerprint, assetName string) ObjectContext {
	return ObjectContext{
		LibraryID:         libraryID,
		ObjectClass:       ObjectClassGeneratedAsset,
		ImmutableObjectID: contentFingerprint + "/" + assetName,
	}
}

// encode validates the context and serializes it as the exact byte contract:
// context = enc(LibraryID) || enc(ObjectClass) || enc(ImmutableObjectID), with
// enc(value) = u16be(byte-length) || UTF8(value). The length prefix makes the
// encoding injective, so no two distinct contexts share bytes.
func (o ObjectContext) encode() ([]byte, error) {
	if !validObjectClass(o.ObjectClass) {
		return nil, fmt.Errorf("%w: unknown object class", ErrObjectContext)
	}
	fields := []string{o.LibraryID, string(o.ObjectClass), o.ImmutableObjectID}
	buf := make([]byte, 0, 2*len(fields)+len(o.LibraryID)+len(o.ObjectClass)+len(o.ImmutableObjectID))
	for _, field := range fields {
		if len(field) == 0 {
			return nil, fmt.Errorf("%w: empty field", ErrObjectContext)
		}
		if len(field) > objectContextFieldMax {
			return nil, fmt.Errorf("%w: field exceeds %d bytes", ErrObjectContext, objectContextFieldMax)
		}
		if !utf8.ValidString(field) {
			return nil, fmt.Errorf("%w: field is not valid UTF-8", ErrObjectContext)
		}
		buf = binary.BigEndian.AppendUint16(buf, uint16(len(field))) //nolint:gosec // bounded by objectContextFieldMax
		buf = append(buf, field...)
	}
	return buf, nil
}
