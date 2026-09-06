package credentials

import "context"

/*
principalKey is the context key under which a verified identity travels.

It is an unexported type so that no other package can write a principal into a
context. Only the middleware that actually verified a key can put one there,
which is what makes reading one a trustworthy answer to "who is asking".
*/
type principalKey struct{}

// ContextWithPrincipal returns a context carrying a verified identity.
func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

/*
PrincipalFromContext returns the verified identity a request carries.

The second result is false on an unauthenticated request, so a caller cannot
mistake the zero value for a principal that permits nothing in particular: a
zero Principal has an empty application and no scopes, and acting on it would
be a bug rather than a safe default.
*/
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, found := ctx.Value(principalKey{}).(Principal)
	return principal, found
}
