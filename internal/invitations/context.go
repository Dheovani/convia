package invitations

import "context"

/*
holderKey is the context key under which a verified invitation travels.

It is an unexported type so that no other package can write one into a context.
Only the middleware that actually verified the presented secret can put one
there, which is what makes reading one a trustworthy answer to "which
invitation is this".
*/
type holderKey struct{}

// ContextWithHolder returns a context carrying a verified invitation.
func ContextWithHolder(ctx context.Context, invitation Invitation) context.Context {
	return context.WithValue(ctx, holderKey{}, invitation)
}

/*
HolderFromContext returns the invitation a request was authenticated with.

The second result is false on a request that carried none, so a caller cannot
mistake the zero value for an invitation: a zero Invitation names no
application and no call, and acting on it would be a bug rather than a safe
default.
*/
func HolderFromContext(ctx context.Context) (Invitation, bool) {
	invitation, found := ctx.Value(holderKey{}).(Invitation)
	return invitation, found
}
