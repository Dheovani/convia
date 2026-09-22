import type {
  Account,
  CallPresencePage,
  HistoryDirection,
  InvitationLook,
  Presence,
  PresenceList,
  JoinedRoom,
  JoinSession,
  Message,
  MessagePage,
  OwnRoom,
  PersonPage,
  ReadState,
  RemoteRoomPage,
  RoomCallList,
  RoomInvitation,
  RoomInvitationList,
  RoomMember,
  Sidebar,
} from './types'

import { ApiError, NetworkError } from './errors'
import type { ApiFailure } from './errors'
import { desktop, inApplication } from '../desktop/bridge'

export { ApiError, NetworkError } from './errors'
export type { ApiFailure } from './errors'

const prefix = '/v1'

/*
pageLimit is the largest page Convia serves.

The lists of people are read as one page of it. Somebody who shares rooms with
more than a hundred people would see the first hundred, which is a gap the
interface will have to page through once there is a screen where it matters.
*/
const pageLimit = 100

interface RequestOptions {
  method?: string
  body?: unknown
  signal?: AbortSignal
  keepalive?: boolean
}

/*
presenceLifetime is how long one heartbeat stands, in seconds. A page sends one
far more often than this, so a few that are lost do not take anybody offline.
*/
export const presenceLifetime = 60

/*
call performs one request against Convia's session surface.

`credentials: 'same-origin'` is explicit rather than left to the default,
because it is the whole authentication model of this interface: the session
travels in a cookie the page cannot read, and every request is same-origin by
construction. There is no token to attach and no header to set.
*/
async function call<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET'
  const headers: Record<string, string> = { Accept: 'application/json' }

  let body: string | undefined
  if (options.body !== undefined) {
    body = JSON.stringify(options.body)
    headers['Content-Type'] = 'application/json'
  }

  let response: Response
  try {
    response = await fetch(prefix + path, {
      method,
      headers,
      credentials: 'same-origin',
      ...(body === undefined ? {} : { body }),
      ...(options.signal ? { signal: options.signal } : {}),
      ...(options.keepalive ? { keepalive: true } : {}),
    })
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError') {
      throw cause
    }
    throw new NetworkError(cause)
  }

  if (response.status === 204) {
    return undefined as T
  }

  const payload: unknown = await response.json().catch(() => undefined)

  if (!response.ok) {
    const failure = (payload as { error?: ApiFailure } | undefined)?.error
    throw new ApiError(
      response.status,
      failure ?? { code: 'unknown', message: 'Convia refused the request.' },
    )
  }

  return payload as T
}

function query(parameters: Record<string, string | number | undefined>): string {
  const search = new URLSearchParams()
  for (const [name, value] of Object.entries(parameters)) {
    if (value !== undefined && value !== '') {
      search.set(name, String(value))
    }
  }
  const rendered = search.toString()
  return rendered === '' ? '' : `?${rendered}`
}

/*
RoomSource says where a room's conversation is read from.

A room here is read under `/me/rooms`; a room on another installation under
`/me/remote-rooms`, which this installation relays to the room's home. The two
answer with the same shapes, so everything that reads a conversation takes a
source and never needs to know which it has.
*/
export type RoomSource = { kind: 'local'; id: string } | { kind: 'remote'; id: string }

// sourceKey is a source as one string, for React keys and effect dependencies.
export function sourceKey(source: RoomSource): string {
  return `${source.kind}:${source.id}`
}

export interface RoomApi {
  history: (
    options: { limit?: number; cursor?: string; direction?: HistoryDirection },
    signal?: AbortSignal,
  ) => Promise<MessagePage>
  post: (body: string) => Promise<Message>
  edit: (messageId: string, body: string) => Promise<Message>
  withdraw: (messageId: string) => Promise<Message>
  markRead: (sequence: number) => Promise<ReadState>
  members: (signal?: AbortSignal) => Promise<PersonPage>
}

// roomApi is how one room is read and written, wherever it lives.
export function roomApi(source: RoomSource): RoomApi {
  const id = encodeURIComponent(source.id)
  const room = source.kind === 'local' ? `/me/rooms/${id}` : `/me/remote-rooms/${id}`
  const message = (messageId: string) =>
    source.kind === 'local'
      ? `/me/messages/${encodeURIComponent(messageId)}`
      : `${room}/messages/${encodeURIComponent(messageId)}`

  return {
    history(options, signal) {
      const path =
        `${room}/messages` +
        query({ limit: options.limit, cursor: options.cursor, direction: options.direction })
      return call<MessagePage>(path, signal ? { signal } : {})
    },
    post(body) {
      return call<Message>(`${room}/messages`, { method: 'POST', body: { body } })
    },
    edit(messageId, body) {
      return call<Message>(message(messageId), { method: 'PATCH', body: { body } })
    },
    withdraw(messageId) {
      return call<Message>(`${message(messageId)}/delete`, { method: 'POST' })
    },
    markRead(sequence) {
      return call<ReadState>(`${room}/read_state`, { method: 'PUT', body: { sequence } })
    },
    members(signal) {
      const path = `${room}/members` + query({ limit: pageLimit })
      return call<PersonPage>(path, signal ? { signal } : {})
    },
  }
}

/*
The six routes below answer with a session or end one, and in Convia's own
application they are the application's rather than this interface's.

The application refuses to carry them, because the answer to a carried request
is read by this webview and the session must never be. What comes back from it
instead is who is signed in, which is the same shape Convia answers a browser
with. Everything else on `api` is the same call either way: the application
carries it, with the session attached where this interface cannot see it.
*/
export const api = {
  signIn(username: string, password: string): Promise<Account> {
    if (inApplication()) {
      return desktop.signIn(username, password)
    }
    return call<Account>('/sessions', { method: 'POST', body: { username, password } })
  },

  // register creates an account and signs its owner in, in one request.
  register(username: string, password: string): Promise<Account> {
    if (inApplication()) {
      return desktop.register(username, password)
    }
    return call<Account>('/accounts', { method: 'POST', body: { username, password } })
  },

  signOut(): Promise<void> {
    if (inApplication()) {
      return desktop.signOut()
    }
    return call<void>('/sessions/current', { method: 'DELETE' })
  },

  // signOutEverywhere ends every session of the account, this one included.
  signOutEverywhere(): Promise<void> {
    if (inApplication()) {
      return desktop.signOutEverywhere()
    }
    return call<void>('/sessions', { method: 'DELETE' })
  },

  /*
  changePassword replaces the password. Convia rotates this session in the same
  answer and ends every other one; a wrong current password is `wrong_password`,
  and the session survives it.
  */
  changePassword(current: string, next: string): Promise<void> {
    if (inApplication()) {
      return desktop.changePassword(current, next)
    }
    return call<void>('/me/password', {
      method: 'PATCH',
      body: { current_password: current, new_password: next },
    })
  },

  /*
  deleteAccount deletes the account for good, having checked its password.
  Convia clears the cookie in the same answer; a wrong password is
  `wrong_password`, and changes nothing.
  */
  deleteAccount(password: string): Promise<void> {
    if (inApplication()) {
      return desktop.deleteAccount(password)
    }
    return call<void>('/me/delete', { method: 'POST', body: { password } })
  },

  me(signal?: AbortSignal): Promise<Account> {
    return call<Account>('/me', signal ? { signal } : {})
  },

  rooms(signal?: AbortSignal): Promise<Sidebar> {
    return call<Sidebar>('/me/rooms', signal ? { signal } : {})
  },

  // createRoom opens a room with this person in it. It sends a name and nothing
  // else, because nothing else is a person's to decide.
  createRoom(name: string): Promise<OwnRoom> {
    return call<OwnRoom>('/me/rooms', { method: 'POST', body: { name } })
  },

  addMember(roomId: string, userId: string): Promise<RoomMember> {
    return call<RoomMember>(
      `/me/rooms/${encodeURIComponent(roomId)}/members/${encodeURIComponent(userId)}`,
      { method: 'PUT' },
    )
  },

  leave(roomId: string): Promise<void> {
    return call<void>(`/me/rooms/${encodeURIComponent(roomId)}/leave`, { method: 'POST' })
  },

  // people is everybody this person could add to a room: the people they
  // already share one with, and nobody else.
  people(signal?: AbortSignal): Promise<PersonPage> {
    return call<PersonPage>('/me/people' + query({ limit: pageLimit }), signal ? { signal } : {})
  },

  /*
  What only a room's owner may do, and what its moderators may do too.

  A moderator removes, bans and lifts bans, but not on the owner or another
  moderator; everything else here is the owner's. Convia refuses the rest: a
  member is told 403, and somebody outside the room 404.
  */
  renameRoom(roomId: string, name: string): Promise<OwnRoom> {
    return call<OwnRoom>(`/me/rooms/${encodeURIComponent(roomId)}`, { method: 'PATCH', body: { name } })
  },

  closeRoom(roomId: string): Promise<OwnRoom> {
    return call<OwnRoom>(`/me/rooms/${encodeURIComponent(roomId)}/close`, { method: 'POST' })
  },

  reopenRoom(roomId: string): Promise<OwnRoom> {
    return call<OwnRoom>(`/me/rooms/${encodeURIComponent(roomId)}/reopen`, { method: 'POST' })
  },

  deleteRoom(roomId: string): Promise<void> {
    return call<void>(`/me/rooms/${encodeURIComponent(roomId)}`, { method: 'DELETE' })
  },

  removeMember(roomId: string, userId: string): Promise<void> {
    return call<void>(`/me/rooms/${encodeURIComponent(roomId)}/members/${encodeURIComponent(userId)}`, {
      method: 'DELETE',
    })
  },

  ban(roomId: string, userId: string): Promise<void> {
    return call<void>(`/me/rooms/${encodeURIComponent(roomId)}/bans/${encodeURIComponent(userId)}`, {
      method: 'PUT',
    })
  },

  unban(roomId: string, userId: string): Promise<void> {
    return call<void>(`/me/rooms/${encodeURIComponent(roomId)}/bans/${encodeURIComponent(userId)}`, {
      method: 'DELETE',
    })
  },

  nameModerator(roomId: string, userId: string): Promise<void> {
    return call<void>(`/me/rooms/${encodeURIComponent(roomId)}/moderators/${encodeURIComponent(userId)}`, {
      method: 'PUT',
    })
  },

  unnameModerator(roomId: string, userId: string): Promise<void> {
    return call<void>(`/me/rooms/${encodeURIComponent(roomId)}/moderators/${encodeURIComponent(userId)}`, {
      method: 'DELETE',
    })
  },

  // handOver makes another member the owner; this person stays as a member.
  handOver(roomId: string, userId: string): Promise<OwnRoom> {
    return call<OwnRoom>(`/me/rooms/${encodeURIComponent(roomId)}/owner`, {
      method: 'PUT',
      body: { user_id: userId },
    })
  },

  bans(roomId: string, signal?: AbortSignal): Promise<PersonPage> {
    return call<PersonPage>(
      `/me/rooms/${encodeURIComponent(roomId)}/bans` + query({ limit: pageLimit }),
      signal ? { signal } : {},
    )
  },

  /*
  Calls, one per room at a time.

  Joining starts a call when the room holds none, and leaving ends it when
  nobody is left, so there is nothing here that starts or ends one for
  everybody.
  */
  calls(signal?: AbortSignal): Promise<RoomCallList> {
    return call<RoomCallList>('/me/calls', signal ? { signal } : {})
  },

  callParticipants(roomId: string, signal?: AbortSignal): Promise<CallPresencePage> {
    return call<CallPresencePage>(
      `/me/rooms/${encodeURIComponent(roomId)}/call/participants` + query({ limit: pageLimit }),
      signal ? { signal } : {},
    )
  },

  joinCall(roomId: string): Promise<JoinSession> {
    return call<JoinSession>(`/me/rooms/${encodeURIComponent(roomId)}/call/join`, { method: 'POST' })
  },

  leaveCall(roomId: string): Promise<void> {
    return call<void>(`/me/rooms/${encodeURIComponent(roomId)}/call/leave`, { method: 'POST' })
  },

  // removeFromCall is the moderator putting somebody out of the call.
  removeFromCall(roomId: string, userId: string): Promise<void> {
    return call<void>(
      `/me/rooms/${encodeURIComponent(roomId)}/call/participants/${encodeURIComponent(userId)}`,
      { method: 'DELETE' },
    )
  },

  // invite makes an invitation into a room here for a handle, on any installation.
  invite(roomId: string, handle: string): Promise<RoomInvitation> {
    return call<RoomInvitation>(`/me/rooms/${encodeURIComponent(roomId)}/invitations`, {
      method: 'POST',
      body: { handle },
    })
  },

  // pendingInvitations lists the invitations this person made into a room that still work.
  pendingInvitations(roomId: string, signal?: AbortSignal): Promise<RoomInvitationList> {
    return call<RoomInvitationList>(`/me/rooms/${encodeURIComponent(roomId)}/invitations`, signal ? { signal } : {})
  },

  // withdrawInvitation makes an invitation this person made stop working.
  withdrawInvitation(invitationId: string): Promise<void> {
    return call<void>(`/me/room-invitations/${encodeURIComponent(invitationId)}`, { method: 'DELETE' })
  },

  /*
  Presence: each page is a device of its own, and says what it says as a
  heartbeat. keepalive lets the last one, sent as the page closes, arrive.
  */
  assertPresence(deviceId: string, state: Exclude<Presence['state'], 'offline'>): Promise<Presence> {
    return call<Presence>(`/me/presence/${encodeURIComponent(deviceId)}`, {
      method: 'PUT',
      body: { state, lifetime_seconds: presenceLifetime },
    })
  },

  withdrawPresence(deviceId: string): Promise<Presence> {
    return call<Presence>(`/me/presence/${encodeURIComponent(deviceId)}`, { method: 'DELETE', keepalive: true })
  },

  // peoplePresence reads the presence of the people this person shares a room with.
  peoplePresence(userIds: string[], signal?: AbortSignal): Promise<PresenceList> {
    const search = new URLSearchParams(userIds.map((id) => ['user_id', id]))
    return call<PresenceList>(`/me/people/presence?${search.toString()}`, signal ? { signal } : {})
  },

  // look asks the home of an invitation link what it is for.
  look(link: string): Promise<InvitationLook> {
    return call<InvitationLook>('/me/invitation-previews', { method: 'POST', body: { link } })
  },

  // join accepts an invitation link.
  join(link: string): Promise<JoinedRoom> {
    return call<JoinedRoom>('/me/remote-rooms', { method: 'POST', body: { link } })
  },

  remoteRooms(signal?: AbortSignal): Promise<RemoteRoomPage> {
    return call<RemoteRoomPage>('/me/remote-rooms', signal ? { signal } : {})
  },

  leaveRemote(remoteRoomId: string): Promise<void> {
    return call<void>(`/me/remote-rooms/${encodeURIComponent(remoteRoomId)}/leave`, {
      method: 'POST',
    })
  },

  // forgetRemote drops a room elsewhere from this installation without leaving it there.
  forgetRemote(remoteRoomId: string): Promise<void> {
    return call<void>(`/me/remote-rooms/${encodeURIComponent(remoteRoomId)}`, { method: 'DELETE' })
  },
}
