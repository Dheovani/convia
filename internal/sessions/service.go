package sessions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/accounts"
	"convia/internal/api"
	"convia/internal/users"
)

/*
directory is the behavior this package needs from the account domain.

Authenticate is where the password is checked, the decoy is compared, and the
concurrency bound is applied. None of that belongs here: this package owns what
happens *after* somebody proves who they are.
*/
type directory interface {
	Authenticate(ctx context.Context, email string, password accounts.Password) (accounts.Account, error)
	Get(ctx context.Context, id string) (accounts.Account, error)
	ChangePassword(ctx context.Context, id string, current, next accounts.Password) error
}

/*
tenants is the behavior this package needs from the tenancy domain.

It asks whether the first-party application is being served, the same question
internal/credentials asks before honoring an application key. Without it,
suspending the tenant that owns Convia's own product would leave every signed-in
person working normally.
*/
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

/*
people is the behavior this package needs from the identity domain.

An account points at a user row, and that row has a lifecycle of its own. A
person whose user was suspended must stop authenticating even if the account
was left alone — otherwise there are two places to be wrong about one fact and
only one of them is enforced.
*/
type people interface {
	Get(ctx context.Context, applicationID, id string) (users.User, error)
}

// Service applies Convia's rules for who is signed in.
type Service struct {
	store       *Store
	accounts    directory
	tenants     tenants
	people      people
	application string
	logger      *slog.Logger
}

func NewService(store *Store, accountDirectory directory, owner tenants, directoryOfPeople people,
	firstPartyApplication string, logger *slog.Logger) *Service {
	return &Service{
		store:       store,
		accounts:    accountDirectory,
		tenants:     owner,
		people:      directoryOfPeople,
		application: firstPartyApplication,
		logger:      logger,
	}
}

/*
Begin signs somebody in.

Everything that decides whether it succeeds happens in the account domain,
which answers with one indistinguishable refusal. What happens here is what
follows: enforcing the ceiling on concurrent sessions, minting a secret, and
recording the row.
*/
func (service *Service) Begin(ctx context.Context, email string, password accounts.Password) (Session, string, error) {
	account, err := service.accounts.Authenticate(ctx, email, password)
	if err != nil {
		return Session{}, "", err
	}

	at := now()
	if err := service.makeRoom(ctx, account.ID, at); err != nil {
		return Session{}, "", err
	}

	value := NewSecret()
	session := Session{
		ID:                NewID(),
		AccountID:         account.ID,
		CreatedAt:         at,
		LastSeenAt:        at,
		AbsoluteExpiresAt: at.Add(AbsoluteLifetime),
	}

	if err := service.store.Create(ctx, session, Digest(value)); err != nil {
		return Session{}, "", err
	}

	service.audit(ctx, "session.began", session)
	return session, Token(session.ID, value), nil
}

/*
Authenticate turns a presented token into who it proves.

The order is cost first, then facts. A token of the wrong shape is refused
without a query. A token of the right shape costs one read by primary key and
one constant-time comparison. Only then does Convia ask the three questions
that can change between one request and the next: is the session still live, is
the account still served, is the person still served.

Every one of those failing produces the same [ErrUnauthenticated], because a
caller learning *which* would learn something about somebody else's account.
*/
func (service *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	id, value, err := ParseToken(token)
	if err != nil {
		return Principal{}, err
	}

	session, digest, err := service.store.Credentials(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}

	if !Matches(digest, value) {
		return Principal{}, ErrUnauthenticated
	}

	at := now()
	if !session.Live(at) {
		return Principal{}, ErrUnauthenticated
	}

	account, err := service.accounts.Get(ctx, session.AccountID)
	if errors.Is(err, accounts.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	if !account.Active() {
		return Principal{}, ErrUnauthenticated
	}

	if err := service.stillServed(ctx, account.UserID); err != nil {
		return Principal{}, err
	}

	/*
		Recorded last, and only when stale. It is the one write on this path,
		it happens at most once an hour per session, and its failure costs
		promptness on an idle timeout rather than an authentication — so it is
		logged and swallowed.
	*/
	if session.Stale(at) {
		if err := service.store.Touch(ctx, session.ID, at); err != nil {
			service.logger.Warn("a session's last use could not be recorded",
				"error", err, "session_id", session.ID)
		}
	}

	return Principal{
		SessionID:     session.ID,
		AccountID:     account.ID,
		UserID:        account.UserID,
		ApplicationID: service.application,
	}, nil
}

/*
stillServed checks the two things outside this package that can withdraw a
session.

An application Convia stopped serving and a person an operator suspended both
have to stop authenticating immediately, for the same reason a revoked key
does: suspension that leaves existing sessions working is not suspension.
*/
func (service *Service) stillServed(ctx context.Context, userID string) error {
	active, err := service.tenants.Active(ctx, service.application)
	if err != nil {
		return fmt.Errorf("check the first-party application: %w", err)
	}
	if !active {
		return ErrUnauthenticated
	}

	person, err := service.people.Get(ctx, service.application, userID)
	if errors.Is(err, users.ErrNotFound) {
		return ErrUnauthenticated
	}
	if err != nil {
		return fmt.Errorf("check the account's user: %w", err)
	}
	if person.Status != users.StatusActive {
		return ErrUnauthenticated
	}
	return nil
}

/*
End signs one session out.

Revoking the row is the real sign-out: the token is a bearer credential, so
clearing the cookie is a courtesy to a client that may or may not honor it,
while the row is what Convia itself consults. It succeeds whether or not the
session was live, so that it cannot be used to ask whether one is.
*/
func (service *Service) End(ctx context.Context, sessionID string) error {
	if err := service.store.Revoke(ctx, sessionID, now()); err != nil {
		return err
	}

	service.audit(ctx, "session.ended", Session{ID: sessionID})
	return nil
}

/*
EndAll signs every one of a person's sessions out, including this one.

It is what somebody reaches for when they think a device was lost, so it
deliberately does not spare the browser asking: leaving the current session
alive would mean "sign out everywhere" did not.
*/
func (service *Service) EndAll(ctx context.Context, accountID string) (int, error) {
	ended, err := service.store.RevokeAllFor(ctx, accountID, now(), "")
	if err != nil {
		return 0, err
	}

	service.logger.Info("audit event",
		"event", "session.ended_everywhere",
		"account_id", accountID,
		"sessions", ended,
		"request_id", api.RequestIDFromContext(ctx),
	)
	return ended, nil
}

/*
ChangePassword replaces a password and applies what that means to sessions.

Three things happen together and the grouping is the point. The password
changes; every *other* session is revoked, because the usual reason to change a
password is believing somebody else has it; and the current session is
**rotated** — a new row and a new secret — because the token in the browser
doing this may itself have leaked, and keeping it alive would keep the leak
alive.

The caller receives a new token to set, so the person stays signed in where
they are and nowhere else.
*/
func (service *Service) ChangePassword(ctx context.Context, principal Principal,
	current, next accounts.Password) (Session, string, error) {
	if err := service.accounts.ChangePassword(ctx, principal.AccountID, current, next); err != nil {
		return Session{}, "", err
	}

	at := now()
	if _, err := service.store.RevokeAllFor(ctx, principal.AccountID, at, ""); err != nil {
		return Session{}, "", err
	}

	value := NewSecret()
	rotated := Session{
		ID:                NewID(),
		AccountID:         principal.AccountID,
		CreatedAt:         at,
		LastSeenAt:        at,
		AbsoluteExpiresAt: at.Add(AbsoluteLifetime),
	}

	if err := service.store.Create(ctx, rotated, Digest(value)); err != nil {
		return Session{}, "", err
	}

	service.audit(ctx, "session.rotated", rotated)
	return rotated, Token(rotated.ID, value), nil
}

/*
makeRoom keeps one person's session count bounded.

Somebody who signs in on an eleventh device means it; what they do not mean is
to accumulate sessions without limit, which is what lets anybody holding their
password make revocation the owner's problem. The least recently used session
goes, because it is the one least likely to be a device still in a pocket.
*/
func (service *Service) makeRoom(ctx context.Context, accountID string, at time.Time) error {
	live, err := service.store.Live(ctx, accountID, at)
	if err != nil {
		return err
	}

	for index := 0; len(live)-index >= MaxPerAccount; index++ {
		if err := service.store.Revoke(ctx, live[index].ID, at); err != nil {
			return err
		}
		service.logger.Info("audit event",
			"event", "session.evicted",
			"account_id", accountID,
			"session_id", live[index].ID,
			"request_id", api.RequestIDFromContext(ctx),
		)
	}
	return nil
}

/*
Account returns the person a session belongs to.

It is here rather than on the account service so that the HTTP layer of this
package depends on one thing. What it returns is the same account any other
caller would see; this package adds nothing to it.
*/
func (service *Service) Account(ctx context.Context, accountID string) (accounts.Account, error) {
	return service.accounts.Get(ctx, accountID)
}

// Prune deletes sessions that stopped mattering, so the table does not grow
// without bound. Its caller is the composition root.
func (service *Service) Prune(ctx context.Context, grace time.Duration) (int, error) {
	return service.store.Prune(ctx, now().Add(-grace))
}

/*
audit records a change to who is signed in.

The account and session identifiers are recorded; the token never is, and
neither is the email. Convia assigned both identifiers, so they say what an
operator needs without putting a credential or a contactable address into a log
that is shipped and retained.
*/
func (service *Service) audit(ctx context.Context, event string, session Session) {
	attributes := []any{
		"event", event,
		"session_id", session.ID,
		"request_id", api.RequestIDFromContext(ctx),
	}
	if session.AccountID != "" {
		attributes = append(attributes, "account_id", session.AccountID)
	}

	service.logger.Info("audit event", attributes...)
}

// now returns the timestamp Convia stores for a change.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
