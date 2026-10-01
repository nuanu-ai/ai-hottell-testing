package clock_test

import (
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/clock"
)

func TestSystemNow(t *testing.T) {
	t.Parallel()

	before := time.Now()
	got := clock.System{}.Now()
	after := time.Now()

	if got.Location() != time.UTC {
		t.Fatalf("Now() location = %v, want UTC", got.Location())
	}
	if got.Before(before) || got.After(after) {
		t.Fatalf("Now() = %v, want between %v and %v", got, before, after)
	}
}
