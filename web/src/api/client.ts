import type {
  Account,
  HistoryDirection,
  Message,
  MessagePage,
  OwnRoom,
  PersonPage,
  ReadState,
  RoomMember,
  Sidebar,
} from './types'

const prefix = '/v1'

/*
pageLimit is the largest page Convia serves.

The lists of people are read as one page of it. Somebody who shares rooms with
more than a hundred people would see the first hundred, which is a gap the
interface will have to page through once there is a screen where it matters.
*/
const pageLimit = 100

/*
ApiFailure is the error body every Convia route answers with.

The code is the part to branch on: it is a documented, stable identifier, while
the message is prose that may be reworded. Nothing in this interface decides
anything from the message text.
*/
export interface ApiFailure {
  code: string
  message: string
  request_id?: string
}

/*
ApiError carries a refusal Convia explained.

The status is kept alongside the code because the two answer different
questions: the status decides whether the session survived, and the code
decides what to tell the person.
*/
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string | undefined

  constructor(status: number, failure: ApiFailure) {
    super(failure.message)
    this.name = 'ApiError'
    this.status = status
    this.code = failure.code
    this.requestId = failure.request_id
  }

  // unauthenticated reports a session that is gone rather than a request that
  // was wrong. It is the one failure that ends the signed-in state.
  get unauthenticated(): boolean {
    return this.status === 401
  }
}

/*
NetworkError is a request that never reached Convia.

It is a separate type because the two need different words: a refusal has an
explanation worth showing, and an unreachable server has none — telling somebody
their password was wrong when the network was down would be a lie.
*/
export class NetworkError extends Error {
  constructor(cause: unknown) {
    super('Convia could not be reached.')
    this.name = 'NetworkError'
    this.cause = cause
  }
}

interface RequestOptions {
  method?: string
  body?: unknown
  signal?: AbortSignal
}

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

export const api = {
  signIn(email: string, password: string): Promise<Account> {
    return call<Account>('/sessions', { method: 'POST', body: { email, password } })
  },

  signOut(): Promise<void> {
    return call<void>('/sessions/current', { method: 'DELETE' })
  },

  me(signal?: AbortSignal): Promise<Account> {
    return call<Account>('/me', signal ? { signal } : {})
  },

  rooms(signal?: AbortSignal): Promise<Sidebar> {
    return call<Sidebar>('/me/rooms', signal ? { signal } : {})
  },

  history(
    roomId: string,
    options: { limit?: number; cursor?: string; direction?: HistoryDirection } = {},
    signal?: AbortSignal,
  ): Promise<MessagePage> {
    const path =
      `/me/rooms/${encodeURIComponent(roomId)}/messages` +
      query({ limit: options.limit, cursor: options.cursor, direction: options.direction })
    return call<MessagePage>(path, signal ? { signal } : {})
  },

  post(roomId: string, body: string): Promise<Message> {
    return call<Message>(`/me/rooms/${encodeURIComponent(roomId)}/messages`, {
      method: 'POST',
      body: { body },
    })
  },

  edit(messageId: string, body: string): Promise<Message> {
    return call<Message>(`/me/messages/${encodeURIComponent(messageId)}`, {
      method: 'PATCH',
      body: { body },
    })
  },

  withdraw(messageId: string): Promise<Message> {
    return call<Message>(`/me/messages/${encodeURIComponent(messageId)}/delete`, {
      method: 'POST',
    })
  },

  markRead(roomId: string, sequence: number): Promise<ReadState> {
    return call<ReadState>(`/me/rooms/${encodeURIComponent(roomId)}/read_state`, {
      method: 'PUT',
      body: { sequence },
    })
  },

  // createRoom opens a room with this person in it. It sends a name and nothing
  // else, because nothing else is a person's to decide.
  createRoom(name: string): Promise<OwnRoom> {
    return call<OwnRoom>('/me/rooms', { method: 'POST', body: { name } })
  },

  members(roomId: string, signal?: AbortSignal): Promise<PersonPage> {
    const path = `/me/rooms/${encodeURIComponent(roomId)}/members` + query({ limit: pageLimit })
    return call<PersonPage>(path, signal ? { signal } : {})
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
}
