package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

/*
provider stands in for LiveKit's room service, so that a run can find out what
Convia does when the media provider's control API degrades -- `M25-009` --
without measuring media at all. Nothing connects to a room here: what is
answered is the four calls Convia makes, which is what a slow or failing
provider costs the control plane.

A fault is applied for a stretch of the load, and its kinds are what a provider
does when it is in trouble:

	slow  answers every call after ten seconds, past Convia's timeout
	down  answers every call 503, as LiveKit does while it restarts
	hang  never answers, as a provider behind a partition does
*/
type provider struct {
	fault string
	from  atomic.Int64 // Unix nanoseconds; zero is never
	until atomic.Int64

	mutex sync.Mutex
	asked map[string]int
}

// providerFaults is what -media-fault accepts.
var providerFaults = map[string]bool{"": true, "slow": true, "down": true, "hang": true}

func newProvider(fault string) *provider {
	return &provider{fault: fault, asked: map[string]int{}}
}

// schedule applies the fault from at, for length.
func (stand *provider) schedule(at time.Time, length time.Duration) {
	stand.until.Store(at.Add(length).UnixNano())
	stand.from.Store(at.UnixNano())
}

func (stand *provider) faulty(now time.Time) bool {
	from := stand.from.Load()
	return stand.fault != "" && from != 0 && now.UnixNano() >= from && now.UnixNano() < stand.until.Load()
}

func (stand *provider) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	method := strings.TrimPrefix(request.URL.Path, "/twirp/livekit.RoomService/")
	stand.mutex.Lock()
	stand.asked[method]++
	stand.mutex.Unlock()

	if stand.faulty(time.Now()) {
		switch stand.fault {
		case "slow":
			select {
			case <-request.Context().Done():
				return
			case <-time.After(10 * time.Second):
			}
		case "down":
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = response.Write([]byte(`{"code":"unavailable","msg":"the media provider is restarting"}`))
			return
		case "hang":
			<-request.Context().Done()
			return
		}
	}

	var body struct {
		Name     string `json:"name"`
		Identity string `json:"identity"`
	}
	_ = json.NewDecoder(request.Body).Decode(&body)

	answer := map[string]any{}
	switch method {
	case "CreateRoom":
		answer["name"] = body.Name
	case "GetParticipant":
		answer["identity"] = body.Identity
		answer["state"] = "ACTIVE"
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(answer)
}
