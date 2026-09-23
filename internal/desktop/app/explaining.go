package app

import (
	"encoding/json"
	"errors"

	"convia/internal/desktop/client"
)

/*
Failure is a refusal as the interface reads one.

`Kind` is what to do about it, and it is here because the three cases need
different words on a screen. A refusal Convia explained carries a code the
interface branches on. An installation that did not answer has no explanation
worth showing — telling somebody their password was wrong when the network was
down would be a lie. And an address that is not a Convia is neither: it is a
typo, and the remedy is the address rather than anything else.
*/
type Failure struct {
	Kind      string `json:"kind"`
	Status    int    `json:"status,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// The kinds. They are the interface's vocabulary for where a failure came
// from, and they are not Convia's error codes: none of these is a thing an
// installation ever said.
const (
	KindRefused     = "refused"
	KindUnreachable = "unreachable"
	KindNotConvia   = "not_convia"
	KindFailed      = "failed"
)

/*
Explain renders an error the way the interface reads errors, for Wails to
reject a call with.

**It returns a string, and the string is JSON.** Wails builds the rejected
promise's Error from whatever this returns, and an object arrives on the other
side as the words "[object Object]" — losing the code, which is the one part of
a refusal that anything branches on. So the failure is rendered here and parsed
back there, deliberately, rather than being flattened into prose.
*/
func Explain(err error) any {
	rendered, marshalling := json.Marshal(failureOf(err))
	if marshalling != nil {
		// Nothing here can fail to render, and if it somehow did, the words
		// are better than an empty rejection.
		return err.Error()
	}
	return string(rendered)
}

func failureOf(err error) Failure {
	var refusal *client.Refusal
	if errors.As(err, &refusal) {
		return Failure{
			Kind:      KindRefused,
			Status:    refusal.Status,
			Code:      refusal.Code,
			Message:   refusal.Message,
			RequestID: refusal.RequestID,
		}
	}

	var unreachable *client.Unreachable
	if errors.As(err, &unreachable) {
		return Failure{Kind: KindUnreachable, Message: unreachable.Error()}
	}

	var wrong *client.NotConvia
	if errors.As(err, &wrong) {
		return Failure{Kind: KindNotConvia, Message: wrong.Because}
	}

	return Failure{Kind: KindFailed, Message: err.Error()}
}
