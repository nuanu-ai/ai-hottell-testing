package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// fakeRecords counts DeleteExpired calls on calls and answers each with err.
type fakeRecords struct {
	calls chan struct{}
	err   error
}

func (f *fakeRecords) DeleteExpired(context.Context) (int, error) {
	f.calls <- struct{}{}
	return 1, f.err
}

func TestCleanExpired(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sessionsErr, ceremoniesErr error
	}{
		"deletes succeed": {},
		// A failed run is logged and the next one still happens.
		"deletes fail": {sessionsErr: errors.New("database down"), ceremoniesErr: errors.New("database down")},
		// A failed table does not keep the other from being cleaned.
		"one table fails": {sessionsErr: errors.New("database down")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sessions := &fakeRecords{calls: make(chan struct{}), err: tc.sessionsErr}
			ceremonies := &fakeRecords{calls: make(chan struct{}), err: tc.ceremoniesErr}
			logger := slog.New(slog.DiscardHandler)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				cleanExpired(ctx, map[string]expiredRecords{"sessions": sessions, "ceremonies": ceremonies},
					time.Millisecond, logger)
			}()

			// The first run happens at start, the second after the interval; each cleans both tables
			// in no fixed order.
			var sessionCalls, ceremonyCalls int
			for sessionCalls < 2 || ceremonyCalls < 2 {
				select {
				case <-sessions.calls:
					sessionCalls++
				case <-ceremonies.calls:
					ceremonyCalls++
				case <-time.After(5 * time.Second):
					t.Fatalf("DeleteExpired() calls: sessions %d, ceremonies %d, want at least 2 each",
						sessionCalls, ceremonyCalls)
				}
			}

			cancel()
			// Drain a run that may have started before the cancellation.
			for {
				select {
				case <-sessions.calls:
					continue
				case <-ceremonies.calls:
					continue
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("cleanExpired() did not return after the context was canceled")
				}
				break
			}
		})
	}
}
