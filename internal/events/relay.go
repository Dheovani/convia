package events

/*
Relay carries events between the instances of one Convia deployment.

It exists because [Broker] is in-process and holds nothing, which is right for
what a stream promises and wrong for a deployment running more than one
instance: an event produced while serving a request on instance A reaches only
the subscribers connected to A. docs/events.md said so plainly and named this
as the thing that would fix it.

Every method is non-blocking and none returns an error, which is the same
contract [Broker.Publish] has and for the same reason. Announcing what happened
must not be able to slow down or fail the operation that happened, and a relay
that could would put a network round trip inside starting a call. What a relay
does when it cannot reach the other instances is therefore its own problem to
log and recover from — never the caller's.

The consequence is stated rather than hidden: an event a relay could not carry
is an event the subscribers of other instances do not see. That is acceptable
here and nowhere else. The live stream is best-effort by design and a client
rebuilds its picture by re-reading; anything a consumer must not miss belongs
in a webhook, which is a row with attempts behind it.
*/
type Relay interface {
	/*
		Broadcast hands an event to the other instances.

		It must return promptly whatever the state of the network, so an
		implementation queues rather than sends.
	*/
	Broadcast(event Event)

	/*
		Close stops carrying events.

		It is called once, by the composition root, as the process shuts down.
	*/
	Close() error
}
