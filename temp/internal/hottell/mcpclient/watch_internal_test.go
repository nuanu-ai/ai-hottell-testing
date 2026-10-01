package mcpclient

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	t.Parallel()

	b := backoff{p: defaultPauses()}
	for _, want := range []time.Duration{1, 2, 4, 8, 16, 32, 60, 60} {
		want *= time.Second
		got := b.pause()
		if lo, hi := want*8/10, want*12/10; got < lo || got > hi {
			t.Errorf("pause = %v, want %v ±20%%", got, want)
		}
	}
	b.reset()
	if got := b.pause(); got > 1200*time.Millisecond {
		t.Errorf("pause after reset = %v, want about 1s", got)
	}
}
