package redis

import (
	"fmt"
	"time"

	"convia/internal/presence"
)

/*
This file reads what the scripts return.

Redis replies are untyped arrays, so every field is checked rather than
asserted. A reply that does not have the shape this package wrote the script to
produce is a bug in this package or a Redis answering something else entirely,
and either way guessing at it would put invented presence in front of an
application.
*/

// lapsedEntry is one deadline that passed, as the sweep found it.
type lapsedEntry struct {
	applicationID string
	userID        string
	expired       presence.Device
	remaining     []presence.Device
}

// readAssertion reads the reply of the assert script.
func readAssertion(reply any) (time.Time, string, []presence.Device, error) {
	parts, err := elements(reply, 3, "assert")
	if err != nil {
		return time.Time{}, "", nil, err
	}

	now, err := moment(parts[0], "assert")
	if err != nil {
		return time.Time{}, "", nil, err
	}

	outcome, ok := parts[1].(string)
	if !ok {
		return time.Time{}, "", nil, fmt.Errorf("the assert script reported %T rather than an outcome", parts[1])
	}

	standing, err := devices(parts[2], "assert")
	if err != nil {
		return time.Time{}, "", nil, err
	}
	return now, outcome, standing, nil
}

// readSnapshot reads the reply of the clear script.
func readSnapshot(reply any) (time.Time, []presence.Device, error) {
	parts, err := elements(reply, 2, "clear")
	if err != nil {
		return time.Time{}, nil, err
	}

	now, err := moment(parts[0], "clear")
	if err != nil {
		return time.Time{}, nil, err
	}

	standing, err := devices(parts[1], "clear")
	if err != nil {
		return time.Time{}, nil, err
	}
	return now, standing, nil
}

// readLapses reads the reply of the lapse script.
func readLapses(reply any) (time.Time, []lapsedEntry, error) {
	parts, err := elements(reply, 2, "lapse")
	if err != nil {
		return time.Time{}, nil, err
	}

	now, err := moment(parts[0], "lapse")
	if err != nil {
		return time.Time{}, nil, err
	}

	rows, ok := parts[1].([]any)
	if !ok {
		return time.Time{}, nil, fmt.Errorf("the lapse script reported %T rather than a list", parts[1])
	}

	entries := make([]lapsedEntry, 0, len(rows))
	for _, row := range rows {
		fields, err := elements(row, 5, "lapse")
		if err != nil {
			return time.Time{}, nil, err
		}

		applicationID, ok := fields[0].(string)
		userID, alsoOK := fields[1].(string)
		deviceID, stillOK := fields[2].(string)
		value, finallyOK := fields[3].(string)
		if !ok || !alsoOK || !stillOK || !finallyOK {
			return time.Time{}, nil, fmt.Errorf("the lapse script reported a claim in an unexpected shape")
		}

		expired, readable := decode(deviceID, value)
		if !readable {
			/*
				A deadline whose claim this version cannot read is dropped
				rather than announced. It has already been removed from the
				index, so it will not be looked at again, and reporting a
				transition from a state this instance does not understand would
				be inventing one.
			*/
			continue
		}

		remaining, err := devices(fields[4], "lapse")
		if err != nil {
			return time.Time{}, nil, err
		}

		entries = append(entries, lapsedEntry{
			applicationID: applicationID,
			userID:        userID,
			expired:       expired,
			remaining:     remaining,
		})
	}
	return now, entries, nil
}

// elements checks that a reply is a list of the expected length.
func elements(reply any, want int, script string) ([]any, error) {
	parts, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("the %s script reported %T rather than a list", script, reply)
	}
	if len(parts) != want {
		return nil, fmt.Errorf("the %s script reported %d values rather than %d", script, len(parts), want)
	}
	return parts, nil
}

// moment reads the instant Redis reported, which is the only clock presence
// consults.
func moment(value any, script string) (time.Time, error) {
	milliseconds, ok := value.(int64)
	if !ok {
		return time.Time{}, fmt.Errorf("the %s script reported %T rather than a time", script, value)
	}
	return time.UnixMilli(milliseconds).UTC(), nil
}

/*
devices reads a flat field-and-value list into claims.

Claims this version cannot read are skipped, for the reason the package
documentation gives: the namespace is versioned so a future encoding can run
beside this one, and an instance that does not understand a value must report
the person as it would report anybody nothing is being said about.
*/
func devices(value any, script string) ([]presence.Device, error) {
	flat, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("the %s script reported %T rather than a list of claims", script, value)
	}
	if len(flat)%2 != 0 {
		return nil, fmt.Errorf("the %s script reported %d values for pairs of them", script, len(flat))
	}

	claims := make([]presence.Device, 0, len(flat)/2)
	for index := 0; index < len(flat); index += 2 {
		id, ok := flat[index].(string)
		stored, alsoOK := flat[index+1].(string)
		if !ok || !alsoOK {
			return nil, fmt.Errorf("the %s script reported a claim in an unexpected shape", script)
		}
		if device, readable := decode(id, stored); readable {
			claims = append(claims, device)
		}
	}
	return claims, nil
}
