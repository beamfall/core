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

// artworkImmutableCacheControl is the unbound long-cache directive. It applies
// only to a request that carries no compatibility binding at all; a
// decision-bound response can outlive its decision under it, so a bound
// response never uses it.
const artworkImmutableCacheControl = "private, max-age=31536000, immutable"

// artworkBoundCacheControl keeps a decision-bound response storable but forces
// revalidation, so the bound validator below decides every reuse instead of a
// wall-clock freshness window.
const artworkBoundCacheControl = "private, no-cache"

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

// artworkCacheUnestablished reports a request that is compatibility-bound but
// whose decision cannot be named in full. Such a response is uncacheable.
func artworkCacheUnestablished(r *http.Request) bool {
	generation, subject, bound := CompatBindingFromContext(r.Context())
	if !bound {
		return false
	}
	return generation == "" || subject == ""
}

// artworkCacheDirectives derives the cache validator and Cache-Control for an
// artwork response whose bytes are at content version `version`. An unbound
// request keeps the content-addressed long cache. A bound request gets a
// validator that also covers the decision generation and the subject, under a
// revalidating directive. Callers reject the unestablished case first.
func artworkCacheDirectives(r *http.Request, version string) (validator, cacheControl string) {
	generation, subject, bound := CompatBindingFromContext(r.Context())
	if !bound {
		return version, artworkImmutableCacheControl
	}
	return version + "-" + compatCacheTag(generation, subject), artworkBoundCacheControl
}

// compatCacheTag folds the decision generation and subject into an opaque tag.
// The subject identifies a credential, so it is hashed rather than echoed in a
// response header.
func compatCacheTag(generation, subject string) string {
	sum := sha256.Sum256([]byte(generation + "\x00" + subject))
	return hex.EncodeToString(sum[:])[:32]
}
