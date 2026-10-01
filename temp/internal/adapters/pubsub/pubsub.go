// Package pubsub tells the subscribers of a user, within this process, that the settings
// of the user changed: one instance of the service is enough.
package pubsub

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/application/settings"
)

var _ settings.Notifier = (*Notifier)(nil)

// Notifier delivers settings versions to the subscribers of each user.
type Notifier struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan int64]struct{}
}

// NewNotifier returns a Notifier with no subscribers.
func NewNotifier() *Notifier {
	return &Notifier{subs: make(map[uuid.UUID]map[chan int64]struct{})}
}

// Publish hands version to every subscriber of the user with userID without blocking: a
// subscriber that has not read the previous version keeps the greater of the two.
func (n *Notifier) Publish(userID uuid.UUID, version int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.subs[userID] {
		// Only Publish sends and it holds the lock, so after the drain the buffer has room.
		select {
		case ch <- version:
		default:
			select {
			case pending := <-ch:
				version = max(version, pending)
			default:
			}
			ch <- version
		}
	}
}

// Subscribe returns a channel that gets the versions published for the user with userID
// from now on; the channel is closed and forgotten once ctx is done.
func (n *Notifier) Subscribe(ctx context.Context, userID uuid.UUID) <-chan int64 {
	ch := make(chan int64, 1)
	n.mu.Lock()
	if n.subs[userID] == nil {
		n.subs[userID] = make(map[chan int64]struct{})
	}
	n.subs[userID][ch] = struct{}{}
	n.mu.Unlock()

	go func() {
		<-ctx.Done()
		n.mu.Lock()
		defer n.mu.Unlock()
		delete(n.subs[userID], ch)
		if len(n.subs[userID]) == 0 {
			delete(n.subs, userID)
		}
		close(ch)
	}()
	return ch
}
