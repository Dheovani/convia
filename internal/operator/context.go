package operator

import "context"

/*
principalKey is the context key under which a verified operator travels.

It is an unexported type so that no other package can write an operator
principal into a context. Only the middleware that actually verified an
operator key can put one there, which is what makes reading one a trustworthy
answer to "who is asking".

It is a different key from the one the credentials package uses, so an
application principal can never be read as an operator principal however the
two middlewares are composed.
*/
type principalKey struct{}

// ContextWithPrincipal returns a context carrying a verified operator identity.
func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

/*
PrincipalFromContext returns the verified operator a request carries.

The second result is false on a request that did not authenticate as an
operator, so a caller cannot mistake the zero value for a principal that
permits nothing in particular: a zero Principal carries no scopes, and acting
on it would be a bug rather than a safe default.
*/
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, found := ctx.Value(principalKey{}).(Principal)
	return principal, found
}
