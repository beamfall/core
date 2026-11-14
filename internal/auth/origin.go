package auth

import "context"

type remoteOriginKey struct{}

// WithRemoteOrigin marks ctx as serving a request that did not come from the
// home network (relayed, forwarded without a declared proxy, or from a public
// address). The HTTP edge sets it; surfaces that are household-only read it
// with RemoteOrigin. An unmarked context reads as home-network.
func WithRemoteOrigin(ctx context.Context) context.Context {
	return context.WithValue(ctx, remoteOriginKey{}, true)
}

// RemoteOrigin reports whether ctx was marked by WithRemoteOrigin.
func RemoteOrigin(ctx context.Context) bool {
	remote, _ := ctx.Value(remoteOriginKey{}).(bool)
	return remote
}
