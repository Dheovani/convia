package accounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/api"
	"convia/internal/users"
)

const (
	/*
		maxConcurrentHashes bounds how many passwords are hashed at once.

		argon2id at 64 MiB is a memory cost paid per concurrent hash, and
		signing in is **unauthenticated** — so without a bound, anybody who can
		reach the API can ask Convia to allocate as much memory as it has. Four
		is a quarter of a gigabyte at peak, which is a deliberate number rather
		than a guess: raise the parameters or this bound and the product of the
		two is what the process must be given.

		Requests beyond it wait briefly and are then refused as busy. Queueing
		them instead would turn a flood into a slow death, because the server's
		write timeout is far longer than anybody will wait to sign in.
	*/
	maxConcurrentHashes = 4

	/*
		hashWaitTimeout is how long a request waits for its turn to hash.

		Short on purpose. A caller told to come back in a moment can retry; a
		caller held for thirty seconds has already given up, and the connection
		it is holding is one the instance cannot use for anybody else.
	*/
	hashWaitTimeout = 2 * time.Second
)

/*
ErrUnauthenticated reports that a presented email and password do not sign
anybody in.

Every reason collapses to this one error — unknown address, wrong password,
suspended account, deleted account, a digest this version cannot read. A caller
learns that the pair does not work and nothing more, which is the same promise
the other three credential families already make, and which is what stops the
sign-in form from being a way to discover who has an account.
*/
var ErrUnauthenticated = errors.New("the email and password do not authenticate")

/*
ErrBusy reports that Convia declined to hash a password right now.

It is not a refusal of the credentials and must never be reported as one: the
caller is told to retry, because the next attempt probably succeeds.
*/
var ErrBusy = errors.New("too many passwords are being verified")

/*
people is the behavior this package needs from the identity domain.

An account is a person, and everything else in Convia addresses a person as a
[users.User]. Resolving one here is what keeps rooms, calls, participants, and
presence working through the domains that already exist instead of growing a
second path for people who signed in rather than being asserted.
*/
type people interface {
	Resolve(ctx context.Context, applicationID string, identity users.Identity) (users.User, bool, error)
	Get(ctx context.Context, applicationID, id string) (users.User, error)
}

/*
Service applies Convia's rules for who may sign in to its own product.

The first-party application is fixed at construction rather than taken from a
request. There is no field anywhere on this surface that could name another
tenant, which is the same structural guarantee every other domain gets from
taking its application from a verified credential.
*/
type Service struct {
	store       *Store
	people      people
	application string
	logger      *slog.Logger

	/*
		hashing bounds concurrent argon2id work. A buffered channel rather than
		a semaphore type because that is what the standard library gives, and
		what it gives is enough.
	*/
	hashing chan struct{}
}

func NewService(store *Store, directory people, firstPartyApplication string, logger *slog.Logger) *Service {
	return &Service{
		store:       store,
		people:      directory,
		application: firstPartyApplication,
		logger:      logger,
		hashing:     make(chan struct{}, maxConcurrentHashes),
	}
}

/*
decoy is the digest an unknown email is compared against.

Without it, signing in with an address nobody has would return as soon as the
lookup missed, while a real address would take the tens of milliseconds argon2id
costs. That difference is measurable from across the internet and would turn the
sign-in form into a way to enumerate who has an account — undoing the
indistinguishable refusal that [ErrUnauthenticated] exists for.

It is built once, at startup, with the parameters currently in force. A test
asserts that it matches them, because the day somebody raises the cost and this
is left behind, the timing gap silently reopens.
*/
var decoy = mustHash("a password nobody has, hashed so that nobody can be found by timing")

func mustHash(password Password) Digest {
	digest, err := Hash(password)
	if err != nil {
		panic("accounts: cannot hash the decoy password: " + err.Error())
	}
	return digest
}

/*
Registration is what an operator supplies to create an account.

There is no password field, and its absence is the security decision this type
exists to carry. Convia generates the password, so an operator cannot choose a
weak one, cannot reuse one across the accounts they create, and never has to be
trusted to have picked well. What they receive is returned once and never
stored.
*/
type Registration struct {
	Email       string
	DisplayName string
}

/*
Create makes an account and the user row that represents it.

The identifier is generated first, because the user row is resolved by it: an
account's `external_subject` in the first-party application is its own account
identifier, rather than the email. That matters — a subject stays reserved after
a user is deleted, so using the address would make it unusable forever by the
person who owned it.
*/
func (service *Service) Create(ctx context.Context, registration Registration) (Account, Password, error) {
	email, err := NormalizeEmail(registration.Email)
	if err != nil {
		return Account{}, "", err
	}

	displayName, err := NormalizeDisplayName(registration.DisplayName)
	if err != nil {
		return Account{}, "", err
	}

	id := NewID()

	person, _, err := service.people.Resolve(ctx, service.application, users.Identity{
		ExternalSubject: id,
		DisplayName:     displayName,
	})
	if err != nil {
		return Account{}, "", fmt.Errorf("resolve the account's user: %w", err)
	}

	password := NewPassword()
	digest, err := service.hash(ctx, password)
	if err != nil {
		return Account{}, "", err
	}

	created := now()
	account := Account{
		ID:          id,
		Email:       email,
		DisplayName: displayName,
		UserID:      person.ID,
		Status:      StatusActive,
		CreatedAt:   created,
		UpdatedAt:   created,
	}

	if err := service.store.Create(ctx, account, digest); err != nil {
		return Account{}, "", err
	}

	service.audit(ctx, "account.created", account)
	return account, password, nil
}

/*
Authenticate verifies an email and a password.

The order of the checks is the whole security of this function, and it is the
order internal/credentials already established: **the password is verified
first, and the lifecycle second.** Checking status first would reveal that an
address belongs to a suspended account without needing its password; reporting
suspension differently after a correct password would confirm that the password
was right. Both are oracles, and both are avoided by doing the expensive,
uninformative work first and answering identically afterwards.

An address nobody has is compared against a decoy so that it costs the same as
one somebody does.
*/
func (service *Service) Authenticate(ctx context.Context, email string, password Password) (Account, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		/*
			A malformed address cannot match anything, and the caller learns
			only what every other failure tells them. It is deliberately not
			reported as a validation error: doing so would distinguish "this is
			not an address" from "this is not your password", which is a
			distinction an attacker can use to skip work.
		*/
		return Account{}, ErrUnauthenticated
	}

	account, digest, err := service.store.Credentials(ctx, normalized)
	if errors.Is(err, ErrNotFound) {
		/*
			Hashed anyway, against the decoy, so an unknown address costs what a
			known one costs. The result is discarded: it cannot match, and if it
			somehow did, the answer is still a refusal.
		*/
		if _, hashErr := service.verify(ctx, decoy, password); hashErr != nil {
			return Account{}, hashErr
		}
		return Account{}, ErrUnauthenticated
	}
	if err != nil {
		return Account{}, err
	}

	matches, err := service.verify(ctx, digest, password)
	if err != nil {
		return Account{}, err
	}
	if !matches {
		service.refused(ctx, account.ID, "password")
		return Account{}, ErrUnauthenticated
	}

	if !account.Active() {
		service.refused(ctx, account.ID, "status")
		return Account{}, ErrUnauthenticated
	}

	/*
		A digest made with weaker parameters is replaced now, which is the only
		moment Convia holds the password in the clear. A failure here is logged
		and swallowed: the person signed in correctly, and refusing them because
		an optional upgrade did not land would be the worse outcome.
	*/
	if Stale(digest) {
		service.rehash(ctx, account.ID, password)
	}

	service.audit(ctx, "account.authenticated", account)
	return account, nil
}

/*
ChangePassword replaces a password, having checked the current one.

The caller is already this account, so there is nothing to enumerate and the
failure can say what it means. What it must not do is skip the current-password
check: a session that was stolen would otherwise be enough to lock the owner
out of their own account permanently.
*/
func (service *Service) ChangePassword(ctx context.Context, id string, current, next Password) error {
	account, digest, err := service.credentialsOf(ctx, id)
	if err != nil {
		return err
	}

	matches, err := service.verify(ctx, digest, current)
	if err != nil {
		return err
	}
	if !matches {
		service.refused(ctx, id, "password")
		return ErrUnauthenticated
	}

	normalized, err := NormalizePassword(next)
	if err != nil {
		return err
	}

	replacement, err := service.hash(ctx, normalized)
	if err != nil {
		return err
	}

	if err := service.store.SetPassword(ctx, id, replacement, now()); err != nil {
		return err
	}

	service.audit(ctx, "account.password_changed", account)
	return nil
}

// Get returns one account.
func (service *Service) Get(ctx context.Context, id string) (Account, error) {
	return service.store.Get(ctx, id)
}

// Suspend withdraws an account's ability to sign in.
func (service *Service) Suspend(ctx context.Context, id string) (Account, error) {
	return service.transition(ctx, id, StatusSuspended, "account.suspended")
}

// Activate restores a suspended account.
func (service *Service) Activate(ctx context.Context, id string) (Account, error) {
	return service.transition(ctx, id, StatusActive, "account.activated")
}

// CountActive reports how many people can sign in, for the startup advisory.
func (service *Service) CountActive(ctx context.Context) (int, error) {
	return service.store.CountActive(ctx)
}

func (service *Service) transition(ctx context.Context, id string, status Status, event string) (Account, error) {
	account, err := service.store.SetStatus(ctx, id, status, now())
	if err != nil {
		return Account{}, err
	}

	service.audit(ctx, event, account)
	return account, nil
}

// credentialsOf reads an account and its digest by identifier.
func (service *Service) credentialsOf(ctx context.Context, id string) (Account, Digest, error) {
	account, err := service.store.Get(ctx, id)
	if err != nil {
		return Account{}, "", err
	}

	stored, digest, err := service.store.Credentials(ctx, account.Email)
	if err != nil {
		return Account{}, "", err
	}
	return stored, digest, nil
}

/*
hash derives a digest under the concurrency bound.

The bound is the point. Everything else in this file is about not leaking who
has an account; this is about not letting a stranger decide how much memory the
process allocates.
*/
func (service *Service) hash(ctx context.Context, password Password) (Digest, error) {
	release, err := service.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	return Hash(password)
}

// verify compares a password under the same bound, for the same reason.
func (service *Service) verify(ctx context.Context, stored Digest, password Password) (bool, error) {
	release, err := service.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	matches, err := Verify(stored, password)
	if errors.Is(err, ErrPasswordUnreadable) {
		/*
			A digest this build cannot read is an operator's problem and the
			person signing in is told nothing about it. Logged at error because
			somebody needs to know a row is unusable; answered as a refusal
			because that is what every other failure answers.
		*/
		service.logger.Error("a stored password digest could not be read",
			"error", err, "request_id", api.RequestIDFromContext(ctx))
		return false, nil
	}
	return matches, err
}

/*
acquire takes a place in the hashing bound, or gives up.

Giving up is reported as [ErrBusy] rather than as a refused password, because
the two mean opposite things to a caller: one says the credentials are wrong and
one says to try again.
*/
func (service *Service) acquire(ctx context.Context) (func(), error) {
	waiting, cancel := context.WithTimeout(ctx, hashWaitTimeout)
	defer cancel()

	select {
	case service.hashing <- struct{}{}:
		return func() { <-service.hashing }, nil
	case <-waiting.Done():
		if ctx.Err() != nil {
			return nil, ctx.Err() // The caller went away; that is not busyness.
		}

		service.logger.Warn("passwords are being verified faster than they can be hashed",
			"concurrency", maxConcurrentHashes,
			"request_id", api.RequestIDFromContext(ctx))
		return nil, ErrBusy
	}
}

/*
rehash replaces a digest made with weaker parameters.

Best effort by design: it runs after a successful sign-in, and its failure
costs an upgrade rather than an authentication.
*/
func (service *Service) rehash(ctx context.Context, id string, password Password) {
	digest, err := service.hash(ctx, password)
	if err != nil {
		return
	}

	if err := service.store.SetPassword(ctx, id, digest, now()); err != nil {
		service.logger.Warn("a password could not be re-hashed with the current parameters",
			"error", err, "account_id", id)
	}
}

/*
audit records a change to who may sign in.

The account identifier is recorded and the email is not. An address is the
person's own and appears in nothing else Convia writes down; an identifier
Convia assigned says the same thing for an operator reading a log, without
putting a contactable address in a file that is shipped and retained.
*/
func (service *Service) audit(ctx context.Context, event string, account Account) {
	service.logger.Info("audit event",
		"event", event,
		"account_id", account.ID,
		"user_id", account.UserID,
		"account_status", string(account.Status),
		"request_id", api.RequestIDFromContext(ctx),
	)
}

/*
refused records a sign-in that did not work, and why, for an operator.

The reason is recorded here and never returned to the caller — that asymmetry
is the point. An operator investigating a locked-out colleague needs to know
whether the password was wrong or the account suspended; the person at the form
must not be able to tell the difference.
*/
func (service *Service) refused(ctx context.Context, accountID, reason string) {
	service.logger.Info("audit event",
		"event", "account.refused",
		"account_id", accountID,
		"reason", reason,
		"request_id", api.RequestIDFromContext(ctx),
	)
}

// now returns the timestamp Convia stores for a change.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
