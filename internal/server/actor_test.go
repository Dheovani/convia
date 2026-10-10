package server

import (
	"context"
	"testing"

	"convia/internal/audit"
	"convia/internal/credentials"
	"convia/internal/invitations"
	"convia/internal/operator"
	"convia/internal/sessions"
)

/*
TestEachVerifierNamesWhoItVerified is the seam that keeps the audit trail out of
every domain's dependencies, checked where it is made.

Each verifier is the one place that knows what kind of caller it just checked,
so each one records it. A verifier that forgot would leave that caller's every
action in the trail as the system's, which is the one claim about an action
that must never be wrong -- and nothing downstream could tell, because a
missing actor and Convia acting on its own look the same from there.
*/
func TestEachVerifierNamesWhoItVerified(t *testing.T) {
	cases := map[string]struct {
		verify func(context.Context) (context.Context, error)
		want   audit.Actor
	}{
		"application": {
			verify: func(ctx context.Context) (context.Context, error) {
				return tenantVerifier{service: stubAuthenticator{principal: credentials.Principal{
					ApplicationID: "app_A", CredentialID: "cred_A"}}}.Verify(ctx, "cvk_token")
			},
			want: audit.Actor{Kind: audit.KindApplication, ID: "cred_A"},
		},
		"operator": {
			verify: func(ctx context.Context) (context.Context, error) {
				return operatorVerifier{service: stubOperatorAuthenticator{principal: operator.Principal{
					CredentialID: "oper_A"}}}.Verify(ctx, "cvo_token")
			},
			want: audit.Actor{Kind: audit.KindOperator, ID: "oper_A"},
		},
		"guest": {
			verify: func(ctx context.Context) (context.Context, error) {
				return invitationVerifier{service: stubInvitationAuthenticator{invitation: invitations.Invitation{
					ID: "inv_A"}}}.Verify(ctx, "cvi_token")
			},
			want: audit.Actor{Kind: audit.KindGuest, ID: "inv_A"},
		},
		"person": {
			verify: func(ctx context.Context) (context.Context, error) {
				return sessionVerifier{service: stubSessionAuthenticator{principal: sessions.Principal{
					SessionID: "ses_A", AccountID: "acc_A"}}}.Verify(ctx, "cvs_token")
			},
			want: audit.Actor{Kind: audit.KindPerson, ID: "acc_A"},
		},
	}

	for name, test := range cases {
		ctx, err := test.verify(context.Background())
		if err != nil {
			t.Fatalf("%s: Verify() error = %v", name, err)
		}

		actor, found := audit.ActorFromContext(ctx)
		if !found || actor != test.want {
			t.Errorf("%s: actor = %+v (%v), want %+v", name, actor, found, test.want)
		}
	}
}

/*
TestARefusedCredentialNamesNobody keeps a failed verification from leaving an
actor behind, which would be the trail crediting an action to somebody whose
key did not work.
*/
func TestARefusedCredentialNamesNobody(t *testing.T) {
	ctx, err := operatorVerifier{service: stubOperatorAuthenticator{err: operator.ErrUnauthenticated}}.
		Verify(context.Background(), "cvo_token")
	if err == nil {
		t.Fatal("a refused key verified")
	}
	if ctx != nil {
		if _, found := audit.ActorFromContext(ctx); found {
			t.Error("a refused key left an actor in the context")
		}
	}
}
