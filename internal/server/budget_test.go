package server

import (
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"convia/internal/ratelimit"
)

/*
TestOnlyGuessingIsCountedAcrossInstances keeps the shared budget to the two it
exists for. Asking Redis on every tenant request would put a network round trip
on the path whose whole purpose is to be cheaper than the work it guards; asking
it for neither would leave a guessed password bounded by how many instances
there are.
*/
func TestOnlyGuessingIsCountedAcrossInstances(t *testing.T) {
	var asked []string
	dependencies := testDependencies()
	dependencies.SharedBudget = func(name string, _ int, _ time.Duration, fallback *ratelimit.Limiter) ratelimit.Budget {
		asked = append(asked, name)
		return ratelimit.Local(fallback)
	}

	New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), dependencies)

	slices.Sort(asked)
	if !slices.Equal(asked, []string{"registration", "sign-in"}) {
		t.Errorf("shared budgets asked for %v, want registration and sign-in", asked)
	}
}
