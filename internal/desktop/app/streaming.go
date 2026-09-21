package app

import (
	"context"

	"convia/internal/desktop/client"
	"convia/internal/events"
)

/*
watch keeps the person's events flowing to the window for as long as they are
signed in.

The stream is opened here rather than in the webview because the session
travels in a header, and a page cannot set one on a handshake — see
docs/adr/0019. What the interface gets instead is the events themselves,
emitted as they arrive, which is what it would have read off the socket anyway.

A stream already running is stopped first. Two would double every event on the
screen, and the second would be for whoever signed in most recently while the
first still carried the person before them.
*/
func (application *App) watch(reached *client.Client) {
	application.stopWatching()

	lifetime := application.lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	ctx, stop := context.WithCancel(lifetime)

	stream := client.NewStream(reached, application.logger,
		func(event events.Event) { application.tell(EventTopic, event) },
		func(state client.State) { application.tell(StreamTopic, state) })

	application.mutex.Lock()
	application.watching = stop
	application.mutex.Unlock()

	go stream.Run(ctx)
}

/*
stopWatching ends the stream, if one is open.

Signing out, connecting somewhere else and deleting the account all end it, and
each of them is a moment where events for the person who was signed in must
stop arriving at a screen that is about to belong to somebody else.
*/
func (application *App) stopWatching() {
	application.mutex.Lock()
	stop := application.watching
	application.watching = nil
	application.mutex.Unlock()

	if stop != nil {
		stop()
	}
}
