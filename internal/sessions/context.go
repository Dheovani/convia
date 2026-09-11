package sessions

import "context"

/*
principalKey is the context key under which a verified session travels.

It is an unexported type so that no other package can write one into a context.
Only the middleware that actually verified the presented cookie can put one
there, which is what makes reading one a trustworthy answer to "who is this".

It is a different type from the one internal/credentials uses, and from the one
internal/operator uses, so a principal of one kind can never be read as
another — the mistake is not merely discouraged, it does not compile.
*/
type principalKey struct{}

// ContextWithPrincipal returns a context carrying a verified session.
func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

/*
PrincipalFromContext returns the session a request was authenticated with.

The second result is false on a request that carried none, so a caller cannot
mistake the zero value for somebody: a zero Principal names no account and no
person, and acting on it would be a bug rather than a safe default.
*/
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, found := ctx.Value(principalKey{}).(Principal)
	return principal, found
}
