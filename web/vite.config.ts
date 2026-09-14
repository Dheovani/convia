import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

/*
Convia serves its own interface, so the build writes into the Go package that
embeds it rather than into a directory beside this one.

The output directory is emptied on every build. Only the generated bundle lives
there — the page served when nothing has been built is a sibling of it, outside
the reach of that.
*/
export default defineConfig({
  /*
  Tailwind runs as a Vite plugin, which means it runs at build time and emits
  one static stylesheet. That is not a packaging preference: the play CDN
  generates styles in the browser and injects them inline, which would require
  `unsafe-inline` and a third-party origin in the Content-Security-Policy this
  page is served under — undoing most of what that policy is for.
  */
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../internal/web/assets/dist',
    emptyOutDir: true,
    /*
    Assets are inlined below this size, which would otherwise put a `data:` URL
    into the stylesheet and force `img-src data:` into the policy. Nothing here
    is small enough to be inlined, and setting it to zero keeps it that way.
    */
    assetsInlineLimit: 0,
    /*
    No source map in the bundle Convia embeds. It is roughly five times the
    size of the code it explains, and every byte of it would be compiled into
    the binary and shipped to every deployment. The sources are in the
    repository, which is where somebody debugging this should be looking.
    */
    sourcemap: false,
    /*
    The media client is one chunk of about 560 kB, loaded only when somebody
    joins a call rather than with the page. The default warning is about chunks
    a page waits for, and this is not one, so the limit is set just above it:
    anything that grows past it still warns.
    */
    chunkSizeWarningLimit: 600,
  },
  server: {
    /*
    Development proxies the API to a local Convia so that the browser still
    sees one origin. Without it the session cookie would be cross-site in
    development and same-site in production, which is the one difference that
    would make every CSRF and cookie decision untestable until deployment.
    */
    proxy: {
      /*
      `ws` carries the person's event stream through the same proxy.
      `changeOrigin` stays false for the reason above: the handshake's Origin is
      the page's, and Convia refuses one that does not match the Host it sees.
      */
      '/v1': { target: 'http://127.0.0.1:8080', changeOrigin: false, ws: true },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: true,
  },
})
