package mediahttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// compatBindingKey is the private context key carrying compatBinding.
type compatBindingKey struct{}

// compatBinding names the compatibility decision a request was authorised
// under: the policy generation that decided it, and the subject it was decided
// for. Either field empty means the decision could not be established.
type compatBinding struct {
	generation string
	subject    string
}

// WithCompatBinding records the compatibility decision a request is bound to.
// The compatibility middleware attaches it before the byte routes run, so a
// route that hands the client a reusable cache entry can key that entry on the
// decision generation and the subject as well as the content version. A
// response authorised under one decision then never satisfies a request made
// under another. An empty generation or subject means the decision could not be
// established, and the response fails closed to uncacheable.
func WithCompatBinding(ctx context.Context, generation, subject string) context.Context {
	return context.WithValue(ctx, compatBindingKey{}, compatBinding{generation: generation, subject: subject})
}

// CompatBindingFromContext reports the compatibility binding attached to ctx.
// bound is false when the request is not compatibility-bound at all.
func CompatBindingFromContext(ctx context.Context) (generation, subject string, bound bool) {
	binding, ok := ctx.Value(compatBindingKey{}).(compatBinding)
	if !ok {
		return "", "", false
	}
	return binding.generation, binding.subject, true
}

// VersionedResponseNotModified sets the cache policy for a versioned byte
// response and writes 304 if its validator matches. Pass an empty version to
// skip validation. An unnamed compatibility binding always forces no-store.
// Only an unbound request can use the immutable cache, when the caller allows it.
func VersionedResponseNotModified(w http.ResponseWriter, r *http.Request, version string, allowImmutable bool) bool {
	generation, subject, bound := CompatBindingFromContext(r.Context())
	if bound && (generation == "" || subject == "") {
		w.Header().Set("Cache-Control", "no-store")
		return false
	}
	if version == "" {
		return false
	}

	validator := version
	cacheControl := "private, no-cache"
	if bound {
		validator += "-" + compatCacheTag(generation, subject)
	} else if allowImmutable {
		cacheControl = "private, max-age=31536000, immutable"
	}
	etag := `"` + validator + `"`
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// compatCacheTag folds the decision generation and subject into an opaque tag.
// The subject identifies a credential, so it is hashed rather than echoed in a
// response header.
func compatCacheTag(generation, subject string) string {
	sum := sha256.Sum256([]byte(generation + "\x00" + subject))
	return hex.EncodeToString(sum[:])[:32]
}
