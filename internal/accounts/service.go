package accounts

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"convia/internal/api"
	"convia/internal/users"
)

const (
	/*
		maxConcurrentHashes bounds how many argon2id derivations run at once.

		argon2id at 64 MiB is a memory cost paid per concurrent derivation, and
		signing in and registering are **unauthenticated** — so without a
		bound, anybody who can reach the API can ask Convia to allocate as much
		memory as it has. Four is a quarter of a gigabyte at peak, which is a
		deliberate number rather than a guess: raise the parameters or this
		bound and the product of the two is what the process must be given.

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
ErrUnauthenticated reports that a presented username and password do not sign
anybody in.

Every reason collapses to this one error — unknown username, wrong password,
suspended account, deleted account, a digest this version cannot read. A caller
learns that the pair does not work and nothing more, which is the same promise
the other three credential families already make.
*/
var ErrUnauthenticated = errors.New("the username and password do not authenticate")

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
decoy is the digest an unknown username is compared against.

Without it, signing in as a name nobody has would return as soon as the lookup
missed, while a real one would take the tens of milliseconds argon2id costs.
Registering already tells somebody whether a name is taken, so this is not what
stands between a stranger and a list of usernames; what it keeps is the promise
that **the sign-in form itself** answers every failure alike, in time as well as
in words.

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
Register creates an account for the person asking, and the user row that
represents it.

Everything cheap is checked before anything expensive: the username's shape, the
password's length, and whether the name is taken, so that a refusal costs a
lookup rather than two argon2id derivations and a user row nobody will use.
The unique index still decides a race between two people choosing one name.

The identifier is the fingerprint of a key generated here, and the user row is
resolved by it, so an account's `external_subject` in the first-party
application is its identifier rather than its username. A subject stays
reserved after a user is deleted, and a username is a thing a person chose and
may one day want back.
*/
func (service *Service) Register(ctx context.Context, username string, password Password) (Account, error) {
	name, err := NormalizeUsername(username)
	if err != nil {
		return Account{}, err
	}

	password, err = NormalizePassword(password)
	if err != nil {
		return Account{}, err
	}

	taken, err := service.store.UsernameTaken(ctx, name)
	if err != nil {
		return Account{}, err
	}
	if taken {
		return Account{}, ErrUsernameTaken
	}

	identity, err := NewIdentity()
	if err != nil {
		return Account{}, err
	}

	digest, err := service.hash(ctx, password)
	if err != nil {
		return Account{}, err
	}

	sealed, err := service.seal(ctx, identity, password)
	if err != nil {
		return Account{}, err
	}

	person, _, err := service.people.Resolve(ctx, service.application, users.Identity{
		ExternalSubject: identity.ID(),
		DisplayName:     name,
	})
	if err != nil {
		return Account{}, fmt.Errorf("resolve the account's user: %w", err)
	}

	created := now()
	account := Account{
		ID:        identity.ID(),
		Username:  name,
		PublicKey: identity.Public,
		UserID:    person.ID,
		Status:    StatusActive,
		CreatedAt: created,
		UpdatedAt: created,
	}

	if err := service.store.Create(ctx, account, digest, sealed); err != nil {
		return Account{}, err
	}

	service.audit(ctx, "account.registered", account)
	return account, nil
}

/*
Authenticate verifies a username and a password.

The order of the checks is the whole security of this function, and it is the
order internal/credentials already established: **the password is verified
first, and the lifecycle second.** Checking status first would reveal that a
name belongs to a suspended account without needing its password; reporting
suspension differently after a correct password would confirm that the password
was right. Both are oracles, and both are avoided by doing the expensive,
uninformative work first and answering identically afterwards.

A username nobody has is compared against a decoy so that it costs the same as
one somebody does.
*/
func (service *Service) Authenticate(ctx context.Context, username string, password Password) (Account, error) {
	normalized, err := NormalizeUsername(username)
	if err != nil {
		/*
			A malformed username cannot match anything, and the caller learns
			only what every other failure tells them. It is deliberately not
			reported as a validation error: doing so would distinguish "this is
			not a username" from "this is not your password", which is a
			distinction an attacker can use to skip work.
		*/
		return Account{}, ErrUnauthenticated
	}

	account, digest, sealed, err := service.store.Credentials(ctx, normalized)
	if errors.Is(err, ErrNotFound) {
		/*
			Hashed anyway, against the decoy, so an unknown name costs what a
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
		Secrets derived with weaker parameters are replaced now, which is the
		only moment Convia holds the password in the clear. A failure here is
		logged and swallowed: the person signed in correctly, and refusing them
		because an optional upgrade did not land would be the worse outcome.
	*/
	if Stale(digest) || SealStale(sealed) {
		service.upgrade(ctx, account, sealed, password)
	}

	service.audit(ctx, "account.authenticated", account)
	return account, nil
}

/*
ChangePassword replaces a password, having checked the current one, and seals
the account's key again under the new one.

The caller is already this account, so there is nothing to enumerate and the
failure can say what it means. What it must not do is skip the current-password
check: a session that was stolen would otherwise be enough to lock the owner out
of their own account permanently — and, since the key is sealed by the password,
out of their own identity with it.
*/
func (service *Service) ChangePassword(ctx context.Context, id string, current, next Password) error {
	account, err := service.store.Get(ctx, id)
	if err != nil {
		return err
	}

	_, digest, sealed, err := service.store.Credentials(ctx, account.Username)
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

	identity, err := service.open(ctx, sealed, account.PublicKey, current)
	if err != nil {
		return err
	}

	replacement, err := service.hash(ctx, normalized)
	if err != nil {
		return err
	}

	resealed, err := service.seal(ctx, identity, normalized)
	if err != nil {
		return err
	}

	if err := service.store.SetSecrets(ctx, id, replacement, resealed, now()); err != nil {
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

func (service *Service) transition(ctx context.Context, id string, status Status, event string) (Account, error) {
	account, err := service.store.SetStatus(ctx, id, status, now())
	if err != nil {
		return Account{}, err
	}

	service.audit(ctx, event, account)
	return account, nil
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

// seal encrypts a key under the same bound, because sealing derives with argon2id too.
func (service *Service) seal(ctx context.Context, identity Identity, password Password) (SealedKey, error) {
	release, err := service.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	return Seal(identity, password)
}

/*
open decrypts a key under the bound.

It is only ever called after the same password verified against the digest, so
a key that does not open is not somebody mistyping: the digest and the sealed
key disagree about the password, and the row needs an operator. It is reported
as unreadable and logged, never as a refusal.
*/
func (service *Service) open(ctx context.Context, sealed SealedKey, public ed25519.PublicKey,
	password Password) (Identity, error) {
	release, err := service.acquire(ctx)
	if err != nil {
		return Identity{}, err
	}
	defer release()

	identity, err := Open(sealed, public, password)
	if errors.Is(err, errKeyNotOpened) {
		err = fmt.Errorf("%w: the password verified but does not open the key", ErrKeyUnreadable)
	}
	if err != nil {
		service.logger.Error("a stored identity key could not be opened",
			"error", err, "request_id", api.RequestIDFromContext(ctx))
		return Identity{}, err
	}
	return identity, nil
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
upgrade replaces a digest and a sealed key made with weaker parameters.

Best effort by design: it runs after a successful sign-in, and its failure
costs an upgrade rather than an authentication. Both are replaced together, as
every change to them is, so the two never describe different passwords.
*/
func (service *Service) upgrade(ctx context.Context, account Account, sealed SealedKey, password Password) {
	identity, err := service.open(ctx, sealed, account.PublicKey, password)
	if err != nil {
		return
	}

	digest, err := service.hash(ctx, password)
	if err != nil {
		return
	}

	resealed, err := service.seal(ctx, identity, password)
	if err != nil {
		return
	}

	if err := service.store.SetSecrets(ctx, account.ID, digest, resealed, now()); err != nil {
		service.logger.Warn("an account's secrets could not be re-derived with the current parameters",
			"error", err, "account_id", account.ID)
	}
}

/*
audit records a change to who may sign in.

The account identifier is recorded and the username is not. An identifier Convia
derived says the same thing for an operator reading a log, without putting what
a person chose to be called into a file that is shipped and retained.
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
