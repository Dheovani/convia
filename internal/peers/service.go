package peers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"convia/internal/accounts"
	"convia/internal/api"
	"convia/internal/rooms"
	"convia/internal/sessions"
	"convia/internal/users"
)

// pruneInterval is how often, at most, an instance forgets expired nonces.
const pruneInterval = time.Minute

// roomDirectory is what this package needs from rooms.
type roomDirectory interface {
	Get(ctx context.Context, applicationID, id string) (rooms.Room, error)
	IsMember(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	IsBanned(ctx context.Context, applicationID, roomID, userID string) (bool, error)
	AddUnlessBanned(ctx context.Context, applicationID, roomID, userID string) (rooms.Member, bool, error)
}

// people is what this package needs from users.
type people interface {
	Resolve(ctx context.Context, applicationID string, identity users.Identity) (users.User, bool, error)
	BySubject(ctx context.Context, applicationID, subject string) (users.User, error)
	Get(ctx context.Context, applicationID, id string) (users.User, error)
}

// tenants is whether the first-party application is being served at all.
type tenants interface {
	Active(ctx context.Context, applicationID string) (bool, error)
}

// localAccounts tells whether a signer is somebody who signs in here.
type localAccounts interface {
	Get(ctx context.Context, id string) (accounts.Account, error)
}

// relay makes a signed request to another installation.
type relay interface {
	Do(ctx context.Context, identity accounts.Identity, method, home, target string, body []byte) (Response, error)
}

/*
Service shares rooms between installations, in both directions.

As a **home** it issues invitations, verifies signed requests, and admits the
people who accept. As somebody's **own installation** it previews and accepts
invitations on their behalf, remembers the rooms they joined elsewhere, and
relays what they do in them. One installation is usually both.
*/
type Service struct {
	store       *Store
	rooms       roomDirectory
	people      people
	tenants     tenants
	accounts    localAccounts
	client      relay
	application string
	logger      *slog.Logger
	now         func() time.Time

	// lastPrune is when this instance last forgot expired nonces, in Unix seconds.
	lastPrune atomic.Int64
}

func NewService(
	store *Store,
	directory roomDirectory,
	people people,
	owner tenants,
	local localAccounts,
	client relay,
	firstPartyApplication string,
	logger *slog.Logger,
) *Service {
	return &Service{
		store:       store,
		rooms:       directory,
		people:      people,
		tenants:     owner,
		accounts:    local,
		client:      client,
		application: firstPartyApplication,
		logger:      logger,
		now:         func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
	}
}

// Preview is what a home says about an invitation to the person it is for.
type Preview struct {
	RoomName  string
	Inviter   string
	Invitee   string
	ExpiresAt time.Time
}

// Accepted is what a home says to somebody who joined a room there.
type Accepted struct {
	RoomID   string
	RoomName string
	// UserID is who the person is at the home.
	UserID string
	// Local is true when the person signs in at the home itself.
	Local bool
}

/*
Invite makes an invitation into a room the inviting person is in.

The handle is checked here, check character included, so a mistyped one is
refused before a link exists to be sent. Somebody already in the room is not
invited again: the link would do nothing, and saying so is kinder than letting
it. Nor is somebody the room's owner banned, whose link could never be used.
*/
func (service *Service) Invite(ctx context.Context, principal sessions.Principal, roomID, handle string) (Invitation, error) {
	member, err := service.rooms.IsMember(ctx, principal.ApplicationID, roomID, principal.UserID)
	if err != nil {
		return Invitation{}, fmt.Errorf("check membership: %w", err)
	}
	if !member {
		return Invitation{}, ErrRoomNotFound
	}

	username, accountID, err := accounts.ParseHandle(handle)
	if err != nil {
		var invalid accounts.ValidationError
		if errors.As(err, &invalid) {
			return Invitation{}, ValidationError{Field: "handle", Message: invalid.Message}
		}
		return Invitation{}, err
	}

	invitee, err := service.people.BySubject(ctx, principal.ApplicationID, accountID)
	switch {
	case err == nil:
		inRoom, err := service.rooms.IsMember(ctx, principal.ApplicationID, roomID, invitee.ID)
		if err != nil {
			return Invitation{}, fmt.Errorf("check membership: %w", err)
		}

		if inRoom {
			return Invitation{}, ErrAlreadyMember
		}

		banned, err := service.rooms.IsBanned(ctx, principal.ApplicationID, roomID, invitee.ID)
		if err != nil {
			return Invitation{}, fmt.Errorf("check for a ban: %w", err)
		}

		if banned {
			return Invitation{}, ErrBanned
		}
	case !errors.Is(err, users.ErrNotFound):
		return Invitation{}, fmt.Errorf("look for the invitee: %w", err)
	}

	created := service.now()
	invitation := Invitation{
		ID:               NewInvitationID(),
		ApplicationID:    principal.ApplicationID,
		RoomID:           roomID,
		InviterUserID:    principal.UserID,
		InviteeAccountID: accountID,
		InviteeUsername:  username,
		CreatedAt:        created,
		ExpiresAt:        created.Add(InvitationLifetime),
	}
	if err := service.store.CreateInvitation(ctx, invitation); err != nil {
		return Invitation{}, err
	}

	service.audit(ctx, "room_invitation.created", invitation)
	return invitation, nil
}

// Revoke withdraws an invitation the person asking made and nobody accepted.
/*
Pending lists the invitations a person made into a room here that nobody has
accepted and that still work, so they can be sent again or withdrawn.

It is bounded rather than paged: a link lasts a day, so a person holding more
than this many at once is not somebody a page would help.
*/
func (service *Service) Pending(ctx context.Context, principal sessions.Principal, roomID string) ([]Invitation, error) {
	member, err := service.rooms.IsMember(ctx, principal.ApplicationID, roomID, principal.UserID)
	if err != nil {
		return nil, fmt.Errorf("check membership: %w", err)
	}
	if !member {
		return nil, ErrRoomNotFound
	}
	return service.store.PendingInvitations(ctx, principal.ApplicationID, roomID, principal.UserID,
		service.now(), MaxPendingInvitations)
}

// MaxPendingInvitations is the most invitations Pending lists at once.
const MaxPendingInvitations = 100

func (service *Service) Revoke(ctx context.Context, principal sessions.Principal, id string) error {
	if !ValidInvitationID(id) {
		return ErrNotFound
	}

	revoked, err := service.store.RevokeInvitation(ctx, principal.ApplicationID, id, principal.UserID, service.now())
	if err != nil {
		return err
	}
	if !revoked {
		return ErrNotFound
	}

	service.logger.Info("audit event", "event", "room_invitation.revoked", "invitation_id", id,
		"request_id", api.RequestIDFromContext(ctx))
	return nil
}

/*
usable returns an invitation the named account may use, or [ErrNotFound].

Every reason it may not is one answer — see ErrNotFound — and the check that it
is addressed to that account comes first, so that nothing about an invitation
for somebody else is read into a decision at all.

It takes an account rather than a [Signer] because an account is all it needs,
and because the two ways of proving one are different: a signature from another
installation, or a session here. See [Service.Look].
*/
func (service *Service) usable(ctx context.Context, accountID, id string) (Invitation, rooms.Room, error) {
	if !ValidInvitationID(id) {
		return Invitation{}, rooms.Room{}, ErrNotFound
	}

	invitation, err := service.store.Invitation(ctx, id)
	if err != nil {
		return Invitation{}, rooms.Room{}, err
	}
	if invitation.InviteeAccountID != accountID || !invitation.Pending(service.now()) ||
		invitation.ApplicationID != service.application {
		return Invitation{}, rooms.Room{}, ErrNotFound
	}

	room, err := service.rooms.Get(ctx, invitation.ApplicationID, invitation.RoomID)
	if errors.Is(err, rooms.ErrNotFound) {
		return Invitation{}, rooms.Room{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, rooms.Room{}, fmt.Errorf("read the room: %w", err)
	}
	if room.Status == rooms.StatusDeleted {
		return Invitation{}, rooms.Room{}, ErrNotFound
	}
	return invitation, room, nil
}

// Preview tells the person an invitation is for what they would be joining,
// for a request another installation signed.
func (service *Service) Preview(ctx context.Context, signer Signer, id string) (Preview, error) {
	return service.preview(ctx, signer.AccountID, id)
}

func (service *Service) preview(ctx context.Context, accountID, id string) (Preview, error) {
	invitation, room, err := service.usable(ctx, accountID, id)
	if err != nil {
		return Preview{}, err
	}

	inviter, err := service.people.Get(ctx, invitation.ApplicationID, invitation.InviterUserID)
	if err != nil && !errors.Is(err, users.ErrNotFound) {
		return Preview{}, fmt.Errorf("read the inviter: %w", err)
	}

	return Preview{
		RoomName:  room.Name,
		Inviter:   nameOf(inviter),
		Invitee:   invitation.InviteeHandle(),
		ExpiresAt: invitation.ExpiresAt,
	}, nil
}

/*
Accept admits the person an invitation is for.

The signature already proved the key; the username must also be the one the
invitation named. Then the person becomes a user here — the same one they
already are if they sign in here too — and the invitation is claimed and the
membership made. A membership that cannot be made releases the claim, so the
link still works once whatever refused it is fixed.
*/
func (service *Service) Accept(ctx context.Context, signer Signer, username, id string) (Accepted, error) {
	return service.accept(ctx, signer.AccountID, username, id)
}

func (service *Service) accept(ctx context.Context, accountID, username, id string) (Accepted, error) {
	normalized, err := accounts.NormalizeUsername(username)
	if err != nil {
		return Accepted{}, ErrNotFound
	}

	invitation, room, err := service.usable(ctx, accountID, id)
	if err != nil {
		return Accepted{}, err
	}
	if invitation.InviteeUsername != normalized {
		return Accepted{}, ErrNotFound
	}

	local := true
	if _, err := service.accounts.Get(ctx, accountID); errors.Is(err, accounts.ErrNotFound) {
		local = false
	} else if err != nil {
		return Accepted{}, fmt.Errorf("look for a local account: %w", err)
	}

	person, _, err := service.people.Resolve(ctx, invitation.ApplicationID, users.Identity{
		ExternalSubject: accountID,
		DisplayName:     visitorName(normalized, accountID),
	})
	if errors.Is(err, users.ErrSubjectDeleted) {
		return Accepted{}, ErrNotFound
	}
	if err != nil {
		return Accepted{}, fmt.Errorf("resolve the invitee: %w", err)
	}
	if person.Status != users.StatusActive {
		return Accepted{}, ErrNotFound
	}

	claimed, err := service.store.ClaimInvitation(ctx, invitation.ID, accountID, normalized, person.ID, service.now())
	if err != nil {
		return Accepted{}, err
	}
	if !claimed {
		return Accepted{}, ErrNotFound
	}

	/*
		A ban is checked here, under the room's lock, rather than before the
		claim: a person banned between the two would otherwise be admitted. The
		claim is released like any refused membership, so the link works again
		if the ban is lifted before it expires.
	*/
	if _, _, err := service.rooms.AddUnlessBanned(ctx, invitation.ApplicationID, room.ID, person.ID); err != nil {
		if releaseErr := service.store.ReleaseInvitation(ctx, invitation.ID); releaseErr != nil {
			service.logger.Error("an accepted invitation could not be released after its membership failed",
				"error", releaseErr, "invitation_id", invitation.ID)
		}
		if errors.Is(err, rooms.ErrNotFound) || errors.Is(err, rooms.ErrUserUnavailable) ||
			errors.Is(err, rooms.ErrUserNotFound) || errors.Is(err, rooms.ErrBanned) {
			return Accepted{}, ErrNotFound
		}
		return Accepted{}, fmt.Errorf("add the invitee: %w", err)
	}

	invitation.AcceptedUserID = person.ID
	service.audit(ctx, "room_invitation.accepted", invitation)
	return Accepted{RoomID: room.ID, RoomName: room.Name, UserID: person.ID, Local: local}, nil
}

/*
Verify checks a signed request from another installation and claims its nonce.

A request whose first sight this is not is refused as unauthenticated, like
every other failure: a replay is somebody presenting a credential that no longer
works. So is any request while the first-party application is not served,
because suspending Convia's own product suspends its visitors too.
*/
func (service *Service) Verify(ctx context.Context, request *http.Request, body []byte) (Signer, error) {
	at := service.now()
	verified, err := verifySignature(request, body, at)
	if err != nil {
		return Signer{}, err
	}

	fresh, err := service.store.ClaimNonce(ctx, verified.AccountID, verified.nonce, at.Add(2*MaxSkew))
	if err != nil {
		return Signer{}, err
	}
	if !fresh {
		return Signer{}, ErrUnauthenticated
	}
	service.pruneNonces(ctx, at)

	active, err := service.tenants.Active(ctx, service.application)
	if err != nil {
		return Signer{}, fmt.Errorf("check the first-party application: %w", err)
	}
	if !active {
		return Signer{}, ErrUnauthenticated
	}
	return verified.Signer, nil
}

/*
Visit turns a verified signer into the person they are here, for the routes a
member of a room uses.

Only somebody who already is somebody here gets through: a place is made by
accepting an invitation, never by a signed request that happens to arrive. A
suspended person is refused on their next request, as a suspended session is.
*/
func (service *Service) Visit(ctx context.Context, signer Signer) (sessions.Principal, error) {
	person, err := service.people.BySubject(ctx, service.application, signer.AccountID)
	if errors.Is(err, users.ErrNotFound) {
		return sessions.Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return sessions.Principal{}, fmt.Errorf("read the visitor: %w", err)
	}
	if person.Status != users.StatusActive {
		return sessions.Principal{}, ErrUnauthenticated
	}

	return sessions.Principal{
		AccountID:     signer.AccountID,
		UserID:        person.ID,
		ApplicationID: service.application,
	}, nil
}

func (service *Service) pruneNonces(ctx context.Context, at time.Time) {
	last := service.lastPrune.Load()
	if at.Unix()-last < int64(pruneInterval.Seconds()) || !service.lastPrune.CompareAndSwap(last, at.Unix()) {
		return
	}
	if _, err := service.store.PruneNonces(ctx, at); err != nil {
		service.logger.Warn("expired nonces could not be forgotten", "error", err)
	}
}

// Joined is the result of accepting an invitation on somebody's behalf.
type Joined struct {
	RoomID   string
	RoomName string
	// Remote is the pointer kept here, or nil when the room is on this installation.
	Remote *RemoteRoom
}

/*
Look previews an invitation on behalf of the person it names.

Links to this installation stay in process so local invitations do not depend
on the server being allowed to dial its own private address. Other homes must
still pass through the guarded peer client.
*/
func (service *Service) Look(
	ctx context.Context,
	here string,
	principal sessions.Principal,
	identity accounts.Identity,
	rawLink string,
) (Link, Preview, error) {
	link, err := ParseLink(rawLink)
	if err != nil {
		return Link{}, Preview{}, err
	}

	if link.Home == here {
		preview, err := service.preview(ctx, principal.AccountID, link.InvitationID)
		if err != nil {
			return Link{}, Preview{}, err
		}
		return link, preview, nil
	}

	response, err := service.client.Do(ctx, identity, http.MethodGet, link.Home, invitationTarget(link), nil)
	if err != nil {
		return Link{}, Preview{}, err
	}
	if err := refusal(response); err != nil {
		return Link{}, Preview{}, err
	}

	var body previewBody
	if err := json.Unmarshal(response.Body, &body); err != nil {
		return Link{}, Preview{}, fmt.Errorf("%w: the invitation could not be read", ErrUnreachable)
	}
	expires, err := time.Parse(time.RFC3339Nano, body.ExpiresAt)
	if err != nil || !plainText(body.RoomName, 120) || !plainText(body.Inviter, 200) {
		return Link{}, Preview{}, fmt.Errorf("%w: the invitation could not be read", ErrUnreachable)
	}

	return link, Preview{RoomName: body.RoomName, Inviter: body.Inviter, Invitee: body.Invitee,
		ExpiresAt: expires.UTC()}, nil
}

/*
Join accepts an invitation link on behalf of a person signed in here, and remembers the room.

A room whose home is this installation needs no pointer: the person is now a
member of it here, and it appears in their own list like any other.
*/
func (service *Service) Join(
	ctx context.Context,
	here string,
	account accounts.Account,
	identity accounts.Identity,
	rawLink string,
) (Joined, error) {
	link, err := ParseLink(rawLink)
	if err != nil {
		return Joined{}, err
	}

	if link.Home == here {
		accepted, err := service.accept(ctx, account.ID, account.Username, link.InvitationID)
		if err != nil {
			return Joined{}, err
		}
		return Joined{RoomID: accepted.RoomID, RoomName: accepted.RoomName}, nil
	}

	payload, err := json.Marshal(acceptRequest{Username: account.Username})
	if err != nil {
		return Joined{}, fmt.Errorf("encode the acceptance: %w", err)
	}

	response, err := service.client.Do(ctx, identity, http.MethodPost, link.Home,
		invitationTarget(link)+"/accept", payload)
	if err != nil {
		return Joined{}, err
	}
	if err := refusal(response); err != nil {
		return Joined{}, err
	}

	var body acceptedBody
	if err := json.Unmarshal(response.Body, &body); err != nil || !rooms.ValidID(body.RoomID) ||
		!users.ValidID(body.UserID) || !plainText(body.RoomName, 120) {
		return Joined{}, fmt.Errorf("%w: the acceptance could not be read", ErrUnreachable)
	}

	if body.Local {
		return Joined{RoomID: body.RoomID, RoomName: body.RoomName}, nil
	}

	remote, err := service.store.SaveRemoteRoom(ctx, RemoteRoom{
		ID:        NewRemoteRoomID(),
		AccountID: account.ID,
		Home:      link.Home,
		RoomID:    body.RoomID,
		UserID:    body.UserID,
		Name:      body.RoomName,
		CreatedAt: service.now(),
	})
	if err != nil {
		return Joined{}, err
	}

	service.logger.Info("audit event", "event", "remote_room.joined", "account_id", account.ID,
		"remote_room_id", remote.ID, "request_id", api.RequestIDFromContext(ctx))
	return Joined{RoomID: remote.RoomID, RoomName: remote.Name, Remote: &remote}, nil
}

// RemoteRooms lists the rooms elsewhere an account belongs to.
func (service *Service) RemoteRooms(ctx context.Context, accountID string) ([]RemoteRoom, error) {
	return service.store.RemoteRooms(ctx, accountID)
}

// RemoteRoom returns one of them.
func (service *Service) RemoteRoom(ctx context.Context, accountID, id string) (RemoteRoom, error) {
	if !ValidRemoteRoomID(id) {
		return RemoteRoom{}, ErrRemoteRoomNotFound
	}
	return service.store.RemoteRoom(ctx, accountID, id)
}

// Relay sends one request about a remote room to its home, signed as the person.
func (service *Service) Relay(
	ctx context.Context,
	identity accounts.Identity,
	remote RemoteRoom,
	method,
	target string,
	body []byte,
) (Response, error) {
	return service.client.Do(ctx, identity, method, remote.Home, target, body)
}

/*
Leave takes a person out of a remote room, at its home and then here.

A home that says the person is not in the room any more has already done the
first half, so the pointer goes. Any other answer keeps the pointer, because
dropping it silently would leave a membership nobody here remembers; [Forget]
is the person choosing to do that anyway.
*/
func (service *Service) Leave(ctx context.Context, identity accounts.Identity, remote RemoteRoom) error {
	response, err := service.client.Do(ctx, identity, http.MethodPost, remote.Home,
		"/v1/peer/rooms/"+remote.RoomID+"/leave", nil)
	if err != nil {
		return err
	}
	if response.Status != http.StatusNoContent && response.Status != http.StatusNotFound {
		return refusal(response)
	}
	return service.store.DeleteRemoteRoom(ctx, remote.AccountID, remote.ID)
}

/*
Forget drops the pointer to a remote room without asking its home.

It is how somebody gets rid of a room whose home is gone, has moved, or refuses
them, which Leave cannot get past. They stay a member at the home, and nothing
here can take them out later; the interface says so before it asks.
*/
func (service *Service) Forget(ctx context.Context, remote RemoteRoom) error {
	return service.store.DeleteRemoteRoom(ctx, remote.AccountID, remote.ID)
}

/*
Farewell is what a person deleting their account did at other installations.
*/
type Farewell struct {
	// Left is how many remote rooms confirmed the person left.
	Left int
	// Forgotten is how many were dropped here without their home confirming.
	Forgotten int
	// Withdrawn is how many pending invitations the person had made.
	Withdrawn int64
}

/*
Depart takes a person deleting their account out of every remote room, and
withdraws the invitations they made that nobody accepted yet.

Each remote room is left at its home first. A home that does not confirm is
forgotten instead: the person asked to be gone from here, and a home that will
not answer cannot keep them. They may stay a member there, which the interface
says before the account is deleted.
*/
func (service *Service) Depart(ctx context.Context, principal sessions.Principal,
	identity accounts.Identity) (Farewell, error) {
	remotes, err := service.RemoteRooms(ctx, principal.AccountID)
	if err != nil {
		return Farewell{}, err
	}

	var farewell Farewell
	for _, remote := range remotes {
		if err := service.Leave(ctx, identity, remote); err == nil {
			farewell.Left++
			continue
		} else if ctx.Err() != nil {
			return farewell, err
		}
		if err := service.Forget(ctx, remote); err != nil {
			return farewell, err
		}
		farewell.Forgotten++
	}

	withdrawn, err := service.store.RevokeInvitationsFrom(ctx, principal.ApplicationID, principal.UserID,
		service.now())
	if err != nil {
		return farewell, err
	}
	farewell.Withdrawn = withdrawn
	return farewell, nil
}

/*
refusal turns what a home answered into this package's errors.

A home that refuses a signed request is not saying anything about this
installation's session, so nothing here is ever reported as unauthenticated: a
401 from elsewhere must not sign somebody out of their own Convia.
*/
func refusal(response Response) error {
	switch {
	case response.Status >= 200 && response.Status < 300:
		return nil
	case response.Status == http.StatusNotFound:
		return ErrNotFound
	case response.Status == http.StatusConflict:
		return ErrAlreadyMember
	default:
		return fmt.Errorf("%w: the other installation answered %d", ErrUnreachable, response.Status)
	}
}

func invitationTarget(link Link) string { return "/v1/peer/invitations/" + link.InvitationID }

// visitorName is what a person from elsewhere is called at a home: their name,
// and enough of their identifier to tell two people with one name apart.
func visitorName(username, accountID string) string {
	return username + handleSeparator + accountID[len("acc_"):len("acc_")+4]
}

// handleSeparator matches the one handles use.
const handleSeparator = "#"

// nameOf is what an inviter is shown as: their handle where they have one.
func nameOf(person users.User) string {
	if person.ID == "" {
		return "Somebody who left"
	}
	if accounts.ValidID(person.ExternalSubject) {
		if _, err := accounts.NormalizeUsername(person.DisplayName); err == nil {
			return accounts.Handle(person.DisplayName, person.ExternalSubject)
		}
	}
	return person.DisplayName
}

// plainText reports whether a label from elsewhere is one this interface can show.
func plainText(value string, limit int) bool {
	if value == "" || utf8.RuneCountInString(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func (service *Service) audit(ctx context.Context, event string, invitation Invitation) {
	attributes := []any{
		"event", event,
		"invitation_id", invitation.ID,
		"room_id", invitation.RoomID,
		"inviter_user_id", invitation.InviterUserID,
		"invitee_account_id", invitation.InviteeAccountID,
		"request_id", api.RequestIDFromContext(ctx),
	}
	if invitation.AcceptedUserID != "" {
		attributes = append(attributes, "user_id", invitation.AcceptedUserID)
	}
	service.logger.Info("audit event", attributes...)
}
