package applications

import (
	"context"
	"testing"
)

/*
TestOneRequestAsksOnceWhetherATenantIsActive is `M25-014`: the answer is kept
for the request that asked, not for the next one, and not past a change of
status the request made itself.
*/
func TestOneRequestAsksOnceWhetherATenantIsActive(t *testing.T) {
	service, _ := newTestService(t)
	background := context.Background()

	application, err := service.Create(background, "Orbit")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	asking := func(ctx context.Context, want bool, situation string) {
		t.Helper()
		active, err := service.Active(ctx, application.ID)
		if err != nil {
			t.Fatalf("%s: Active() error = %v", situation, err)
		}
		if active != want {
			t.Errorf("%s: Active() = %v, want %v", situation, active, want)
		}
	}

	request := Remembering(background)
	asking(request, true, "first question")

	// Suspended by somebody else, in another request, while this one runs.
	if _, err := service.store.SetStatus(background, application.ID, StatusSuspended, now(), nil); err != nil {
		t.Fatalf("SetStatus() error = %v", err)
	}
	asking(request, true, "the same request, asking again")
	asking(Remembering(background), false, "the next request")
	asking(background, false, "a context nothing remembers in")

	// A request that changes the status itself sees the change.
	changing := Remembering(background)
	asking(changing, false, "before activating")
	if _, err := service.Activate(changing, application.ID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	asking(changing, true, "after activating")
	if _, err := service.Suspend(changing, application.ID); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	asking(changing, false, "after suspending")
	if _, err := service.Activate(changing, application.ID); err != nil {
		t.Fatalf("Activate() a second time error = %v", err)
	}
	asking(changing, true, "active again")
	if err := service.Delete(changing, application.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	asking(changing, false, "after deleting")

	gone := Remembering(background)
	asking(gone, false, "a deleted tenant")
}
