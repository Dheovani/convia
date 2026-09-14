import '@testing-library/jest-dom/vitest'
import { beforeEach } from 'vitest'

import { FakeSocket } from './socket'

/*
jsdom implements no layout, so it has no scrollIntoView.

This is a gap in the test environment rather than one in the interface, and it
is stubbed here rather than guarded at the call site: a `typeof … === 'function'`
check in the component would be a branch that exists only because of the
renderer these tests use, and it would quietly hide the day the call really did
stop working.
*/
Element.prototype.scrollIntoView = function scrollIntoView() {}

/*
jsdom does implement WebSocket, and would try to connect to a server that is not
there. Assigned directly rather than with vi.stubGlobal, because the tests undo
their stubs after each case and this is meant to stay.
*/
globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket
beforeEach(() => FakeSocket.reset())
