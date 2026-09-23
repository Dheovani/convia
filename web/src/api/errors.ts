/*
The two kinds of failure this interface knows, kept apart from the client that
raises them.

They are here rather than beside `call` so that the boundary with Convia's
desktop application can raise the same ones without the two files importing
each other. A screen that handles a wrong password does not need to learn a
second way of being told about one just because the request went through the
application instead of through fetch.
*/

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
