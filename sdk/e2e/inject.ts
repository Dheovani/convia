/*
The whole SDK, on `window`, so a page can be driven through it.

The integration tests run **inside a browser on Convia's own origin**, because
that is the only place the thing being tested exists: the session is a cookie
the page cannot read, `credentials: 'same-origin'` means nothing outside a
browser, and Node's fetch keeps no cookie jar. A test that drove the SDK from
Node would be testing a different program.

This is bundled by `npm run build:e2e` into a file Playwright injects. It is not
how anybody consumes the package -- they import it -- and it exists only because
a page cannot import TypeScript.
*/

import * as sdk from '../src/index.js'

declare global {
  interface Window {
    sdk: typeof sdk
  }
}

window.sdk = sdk
