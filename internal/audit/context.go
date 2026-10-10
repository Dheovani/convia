package audit

import "context"

/*
actorKey is the context key under which whoever is acting travels.

**This is the seam that keeps the trail out of every domain's dependencies.**
Convia verifies six kinds of caller, each in its own package, each behind its
own unexported context key -- which is what makes reading one a trustworthy
answer to "who is asking", and also what would force this package to import all
six to find out. Instead the middleware that verified a caller records the one
thing the trail needs about them, and nothing here knows how any of them were
verified.

It also means a domain service recording an entry does not take an actor
parameter it would have to be handed through every layer above it. The actor is
a property of the request, like the request identifier beside it, and it travels
the same way.
*/
type actorKey struct{}

/*
ContextWithActor returns a context carrying whoever is acting.

Only the middleware that verified a credential should call this. An actor
written anywhere else would be the trail recording an authority nobody checked.
*/
func ContextWithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

/*
ActorFromContext returns whoever is acting, and whether anybody is.

The second result is false on a request that authenticated as nobody, so a
caller cannot mistake the zero value for an actor: a zero [Actor] has no kind
and recording one would put an entry in the trail that names no authority at
all. What to do about that is the caller's decision -- a public route has no
actor by design, and a change made with none is the system's.
*/
func ActorFromContext(ctx context.Context) (Actor, bool) {
	actor, found := ctx.Value(actorKey{}).(Actor)
	if !found || !actor.Valid() {
		return Actor{}, false
	}
	return actor, true
}

/*
ActorOrSystem returns whoever is acting, or the system when nobody is.

It is what a service recording a change should use. Convia ends calls on the
media plane's evidence and forgets people on a janitor's schedule, and both
happen on a context no request ever touched: an entry for one of those is not
missing its actor, it has the one it should have.
*/
func ActorOrSystem(ctx context.Context) Actor {
	if actor, found := ActorFromContext(ctx); found {
		return actor
	}
	return System()
}
