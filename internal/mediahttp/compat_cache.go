package mediahttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// compatBindingKey is the private context key carrying compatBinding.
type compatBindingKey struct{}

// compatBinding names the compatibility decision a request was authorised
// under, including its policy generation and the subject it was decided for.
type compatBinding struct {
	generation string
	subject    string
	decisionID string
}

// WithCompatBinding records the compatibility decision a request is bound to.
// The compatibility middleware attaches it before the byte routes run, so a
// route that hands the client a reusable cache entry can key that entry on the
// decision ID, generation and subject as well as the content version. A
// response authorised under one decision then never satisfies a request made
// under another. An incomplete binding makes the response uncacheable.
func WithCompatBinding(ctx context.Context, generation, subject, decisionID string) context.Context {
	return context.WithValue(ctx, compatBindingKey{}, compatBinding{generation, subject, decisionID})
}

// CompatBindingFromContext reports the compatibility binding attached to ctx.
// bound is false when the request is not compatibility-bound at all.
func CompatBindingFromContext(ctx context.Context) (generation, subject, decisionID string, bound bool) {
	binding, bound := ctx.Value(compatBindingKey{}).(compatBinding)
	return binding.generation, binding.subject, binding.decisionID, bound
}

// VersionedResponseNotModified sets the cache policy for a versioned byte
// response and writes 304 if its validator matches. Pass an empty version to
// skip validation. An unnamed compatibility binding always forces no-store.
// Only an unbound request can use the immutable cache, when the caller allows it.
func VersionedResponseNotModified(w http.ResponseWriter, r *http.Request, version string, allowImmutable bool) bool {
	generation, subject, decisionID, bound := CompatBindingFromContext(r.Context())
	if bound && (generation == "" || subject == "" || decisionID == "") {
		w.Header().Set("Cache-Control", "no-store")
		return false
	}
	if version == "" {
		return false
	}

	validator := version
	cacheControl := "private, no-cache"
	if bound {
		validator += "-" + compatCacheTag(generation, subject, decisionID)
	} else if allowImmutable {
		cacheControl = "private, max-age=31536000, immutable"
	}
	etag := `"` + validator + `"`
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("ETag", etag)
	if ifNoneMatchMatches(r.Header.Values("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// ifNoneMatchMatches evaluates RFC 9110 §13.1.2 weak ETag comparison across
// multiple header lines, comma-separated tag lists, weak W/ prefixes, and *.
func ifNoneMatchMatches(headers []string, etag string) bool {
	target := strings.TrimPrefix(etag, "W/")
	for _, h := range headers {
		for _, part := range strings.Split(h, ",") {
			token := strings.TrimSpace(part)
			if token == "*" || strings.TrimPrefix(token, "W/") == target {
				return true
			}
		}
	}
	return false
}

// compatCacheTag folds the compatibility binding into an opaque tag.
// The subject identifies a credential, so it is hashed rather than echoed in a
// response header.
func compatCacheTag(generation, subject, decisionID string) string {
	sum := sha256.Sum256([]byte(generation + "\x00" + subject + "\x00" + decisionID))
	return hex.EncodeToString(sum[:])[:32]
}
