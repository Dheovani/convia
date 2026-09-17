/*
The shapes Convia's session surface answers with.

They are written by hand rather than generated. The surface a person reaches is
small and deliberately narrow, and a generator would pull in every operator and
tenant operation alongside it — most of which this interface must never call.
Generation is worth revisiting when the SDK arrives (M19); until then the cost
of keeping thirty lines in step with the contract is lower than the cost of the
tooling.
*/

/*
Account is the person the session belongs to.

`handle` is how they are named to somebody else — the username, a #, and the
account identifier with a check character — and it is rendered by Convia so the
page never has to compute the check character itself.
*/
export interface Account {
  account_id: string
  user_id: string
  username: string
  handle: string
}

export type RoomStatus = 'open' | 'closed' | 'deleted'

// SidebarRoom is one row of the room list: what to show, and whether to look.
export interface SidebarRoom {
  id: string
  alias?: string
  name: string
  status: RoomStatus
  unread: number
  // owned is whether this person owns the room, and so may moderate it.
  owned: boolean
  // moderator is whether this person moderates the room without owning it.
  moderator: boolean
}

export interface Sidebar {
  data: SidebarRoom[]
  next_cursor?: string
}

/*
Message carries an author that may be absent.

A withdrawn message keeps its place in the order and loses its body and its
author, so `body` and `user_id` are optional for a reason that is part of the
domain rather than an accident of serialisation.
*/
export interface Message {
  id: string
  application_id: string
  room_id: string
  sequence: number
  user_id?: string
  invitation_id?: string
  body?: string
  deleted: boolean
  created_at: string
  edited_at?: string
  deleted_at?: string
  // deleted_by says whether the author or the room's owner took it down.
  deleted_by?: 'author' | 'owner'
}

export interface MessagePage {
  data: Message[]
  next_cursor?: string
}

export interface ReadState {
  room_id: string
  user_id: string
  sequence: number
  unread: number
  updated_at?: string
}

export type HistoryDirection = 'older' | 'newer'

// OwnRoom is a room as a person in it sees it.
export interface OwnRoom {
  id: string
  name: string
  status: RoomStatus
  owned: boolean
  created_at: string
}

/*
Person is somebody this person can see, by the name they go by.

It exists on this surface and not on the application's, where a member carries
no name: an application owns its people's names and reads them itself, and a
person has no other way to learn what to call somebody.
*/
export interface Person {
  user_id: string
  display_name: string
  // role is present only when the person is listed as a member of a room.
  role?: 'owner' | 'moderator' | 'member'
}

export interface PersonPage {
  data: Person[]
  next_cursor?: string
}

/*
ConviaEvent is one thing that happened in a room this person is in.

`type` is a string rather than a union of the five this interface understands,
because the vocabulary is additive: Convia may deliver a type this build has
never heard of, and the contract says to ignore it rather than fail. `data` is
loose for the same reason — a key this build does not know is not an error.
*/
export interface ConviaEvent {
  id: string
  version: number
  type: string
  occurred_at: string
  application_id: string
  subject: { type: string; id: string }
  data?: Record<string, unknown>
  // cursor is where the event sits in what Convia kept; handing it back resumes the stream.
  cursor?: string
}

// RoomInvitation is an invitation into a room here, and the link to send.
export interface RoomInvitation {
  id: string
  link: string
  invitee: string
  expires_at: string
}

export interface RoomInvitationList {
  data: RoomInvitation[]
}

// PresenceState is what Convia says about whether somebody is available.
export type PresenceState = 'online' | 'away' | 'busy' | 'offline'

// Presence is one person's presence, as the people who share a room with them see it.
export interface Presence {
  user_id: string
  state: PresenceState
  since?: string
  expires_at?: string
}

export interface PresenceList {
  data: Presence[]
}

// InvitationLook is what the home of a link says the invitation is for.
export interface InvitationLook {
  home: string
  room_name: string
  inviter: string
  invitee: string
  expires_at: string
}

/*
RemoteRoom is a room this person is in on another installation.

`user_id` is who they are there, which is not their `user_id` here: it is what
tells their own messages apart in that room.
*/
export interface RemoteRoom {
  id: string
  home: string
  room_id: string
  user_id: string
  name: string
}

export interface RemoteRoomPage {
  data: RemoteRoom[]
}

// JoinedRoom carries remote_room only when the room lives elsewhere.
export interface JoinedRoom {
  room_id: string
  room_name: string
  remote_room?: RemoteRoom
}

// RoomCall is a call a room is holding now.
export interface RoomCall {
  id: string
  room_id: string
  status: 'active'
  created_at: string
}

export interface RoomCallList {
  data: RoomCall[]
}

/*
CallPresence is somebody in a call now, by the name they go by.

`participant_id` is also who they are to the media server, which is how a
connection on the screen is matched to a name.
*/
export interface CallPresence {
  participant_id: string
  user_id: string
  display_name: string
  role: 'moderator' | 'member'
  joined_at: string
}

export interface CallPresencePage {
  data: CallPresence[]
  next_cursor?: string
}

/*
JoinSession is what to connect to a call with.

`media_token` is a credential. It is handed to the media client and kept nowhere
else: not in state, not in storage, not in a log.
*/
export interface JoinSession {
  participant_id: string
  call_id: string
  media_url: string
  media_token: string
  expires_at: string
}

export interface RoomMember {
  application_id: string
  room_id: string
  user_id: string
  created_at: string
}
