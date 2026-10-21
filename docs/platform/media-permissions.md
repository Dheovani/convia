# Media permissions in the window

What Wails and WebView2 do when the interface asks for the microphone, the camera or the screen ([ROADMAP](../../ROADMAP.md), M17-001), as read from Wails v2.16.0 and go-webview2 v1.0.23, the versions Convia builds with.

## What happens

- **Every permission is granted without asking.** When it creates the window, Wails calls `SetGlobalPermission(CoreWebView2PermissionStateAllow)` on the WebView2 controller (`internal/frontend/desktop/windows/frontend.go`). go-webview2 answers every `PermissionRequested` event with that global state before looking at any per-kind setting, so microphone, camera, notifications, geolocation and every other kind are allowed, and WebView2 shows no prompt of its own. Wails offers no option to change it.
- **Windows still decides.** The system's privacy settings — _Let desktop apps access your microphone_, and the same for the camera — apply to Convia as to any desktop application. When they are off, `getUserMedia()` fails with `NotAllowedError` though WebView2 granted it, and the interface has to say where to turn them on.
- **A screen is chosen by the person.** `getDisplayMedia()` is not a permission WebView2 grants: it opens the system's own picker of screens and windows each time, and the person may cancel it. Screen capture needs the WebView2 runtime 131 or later (`ScreenCapture` in go-webview2's capabilities); an older runtime has no `getDisplayMedia()`.
- **The permissions last as long as the window.** Nothing is remembered between runs beyond what WebView2 keeps in its user data folder, which is one per data directory ([ADR 0016](../adr/0016-how-convia-runs-on-windows.md), `main.go`).

## What it means for Convia

- The window only ever shows Convia's own interface, built into the binary; links open in the system's browser after confirmation ([ADR 0030](../adr/0030-messages-are-written-in-a-subset-of-markdown.md)). So the permissions granted without asking are granted to Convia's code alone, and that code asks for the microphone and the camera only when a call needs them.
- Since no prompt guards them, **the interface itself shows when the microphone, the camera or the screen is in use**, for as long as it is.
- The interface handles `NotAllowedError` from Windows, and `NotFoundError` when there is no such device, in the person's words.

## Seen on a real machine

Checked in the built application on Windows 11 with the call's interface (M17-006):

1. `getUserMedia({ audio: true })` and `({ video: true })` succeed with no prompt, and each side hears and sees the other.
2. With _Let desktop apps access your microphone_ off, `getUserMedia({ audio: true })` fails with `NotAllowedError`, and the interface says where to allow it.
3. A call nobody answers stops ringing after 30 seconds: the caller is told nobody answered, and the callee keeps a missed call ([ADR 0044](../adr/0044-a-call-rings-for-30-seconds.md)).
4. With the window hidden in the tray, a call that rings is told of by a system notification.

## Still to see on a real machine

With screen sharing (M18), checked in the built application on Windows 11 and recorded here:

1. `getDisplayMedia()` opens the system's picker; cancelling it fails with `NotAllowedError`, and nothing is shared.
2. With the computer's sound chosen, the picker offers sound for a whole screen, and the other side hears it ([ADR 0046](../adr/0046-a-shared-screen-may-carry-the-computers-sound.md)).
3. The system's own "stop sharing" control ends the track, and the other side stops seeing the screen.
