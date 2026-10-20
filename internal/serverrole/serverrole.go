// Package serverrole carries the two-face authority boundary of ADR-0123 §2
// (Origin Nodes, decision 2): the same AGPL server binary runs either as the
// single catalog/Stream-Token/engagement authority (Main) or as an attached
// Origin Node (Attached) that performs only byte-local work.
//
// The split is bounded and enumerable, derivable from ADR-0123 decision 2 alone:
//
//   - Authority face (Main-only): an Attached process NEVER authors catalog
//     rows, NEVER issues Stream Tokens, and NEVER authors engagement.
//   - Byte-local face (Node-only): Identification/scan/probe/fingerprint,
//     transcode, and file mutation run wherever the bytes live — including on an
//     Attached node — and are therefore NEVER gated by this package.
//
// Token issuance is also excluded from the node-host contract, so
// an Attached node holds no token-issuing capability at all.
package serverrole

import "errors"

// Role is the process-level authority posture, fixed at boot (no live mutation).
// The zero value is Main, so a server started with no role flag is the single
// authority — the single-server operator story is unchanged (ADR-0123
// Consequences) and existing call sites that never set a role keep main behavior.
type Role int

const (
	// Main is the sole catalog authority and Stream-Token issuer (the default).
	Main Role = iota
	// Attached is an Origin Node: it performs byte-local work only and refuses
	// every authority-face operation.
	Attached
)

// Face names one member of the closed authority set an Attached process refuses.
// The set is closed here so the two-face split is derivable from this package
// alone.
type Face string

const (
	// FaceCatalogRow is authoring a catalog-identity row (Media/Library/Entity).
	FaceCatalogRow Face = "catalog-row"
	// FaceStreamToken is issuing a Stream Token.
	FaceStreamToken Face = "stream-token"
	// FaceEngagement is authoring engagement (the events.db projection).
	FaceEngagement Face = "engagement"
)

// faceReason maps each authority face to the operator-facing refusal reason.
// Lookup-table dispatch keeps GuardAuthority free of per-face branching.
var faceReason = map[Face]string{
	FaceCatalogRow:  "an attached Origin Node never authors catalog rows; the main server is the sole catalog authority (ADR-0123 §2)",
	FaceStreamToken: "an attached Origin Node never issues Stream Tokens; issuance stays main-side (ADR-0123 §2, ADR-0129 §1)",
	FaceEngagement:  "an attached Origin Node never authors engagement; engagement is main-authored (ADR-0123 §2, decision 12)",
}

// ErrAttachedAuthorityRefused is the single sentinel every authority face returns
// when the process is Attached. Callers test it with errors.Is.
var ErrAttachedAuthorityRefused = errors.New("attached server mode: authority operation refused")

// Attached reports whether this role is an Origin Node.
func (r Role) Attached() bool { return r == Attached }

// GuardAuthority returns ErrAttachedAuthorityRefused (wrapped with the face's
// reason) iff this process is Attached; on a Main process it returns nil. It is
// the one gate the three authority seams consult — there is no second decision
// path, so the boundary cannot drift face-by-face.
func (r Role) GuardAuthority(face Face) error {
	if r != Attached {
		return nil
	}
	reason := faceReason[face]
	return &authorityError{face: face, reason: reason}
}

// authorityError carries the refused face while satisfying errors.Is against the
// shared sentinel, so call sites can both branch on the sentinel and surface the
// face-specific reason to an operator.
type authorityError struct {
	face   Face
	reason string
}

func (e *authorityError) Error() string {
	if e.reason == "" {
		return ErrAttachedAuthorityRefused.Error() + ": " + string(e.face)
	}
	return ErrAttachedAuthorityRefused.Error() + ": " + e.reason
}

func (e *authorityError) Is(target error) bool { return target == ErrAttachedAuthorityRefused }

// Face returns the refused authority face (for tests and structured surfacing).
func (e *authorityError) Face() Face { return e.face }
