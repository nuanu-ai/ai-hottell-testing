package pubsub_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/pubsub"
	"git.alva.dev/alva/harness-telemetry/internal/application/settings"
	"git.alva.dev/alva/harness-telemetry/internal/application/settings/mock"
)

// wait is how long a test waits for what must happen.
const wait = 5 * time.Second

// newService returns the settings use cases on a mock repository that holds version 0 of
// nothing denied and accepts one save, and on notifier.
func newService(t *testing.T, notifier *pubsub.Notifier) *settings.Service {
	t.Helper()
	repo := mock.NewMockSettings(gomock.NewController(t))
	repo.EXPECT().Get(gomock.Any(), gomock.Any()).Return(json.RawMessage(`{}`), int64(0), nil).AnyTimes()
	repo.EXPECT().Save(gomock.Any(), gomock.Any(), gomock.Any(), int64(0)).Return(nil).AnyTimes()
	return settings.NewService(repo, notifier)
}

func receive(t *testing.T, events <-chan int64) int64 {
	t.Helper()
	select {
	case v, ok := <-events:
		if !ok {
			t.Fatal("channel closed, want an event")
		}
		return v
	case <-time.After(wait):
		t.Fatal("no event")
		return 0
	}
}

func waitClosed(t *testing.T, events <-chan int64) {
	t.Helper()
	select {
	case v, ok := <-events:
		if ok {
			t.Fatalf("got event %d, want the channel closed", v)
		}
	case <-time.After(wait):
		t.Fatal("channel not closed")
	}
}

func TestSubscriberGetsUpdate(t *testing.T) {
	t.Parallel()

	notifier := pubsub.NewNotifier()
	svc := newService(t, notifier)
	userID := uuid.New()
	events := svc.Subscribe(t.Context(), userID)

	version, err := svc.Update(context.Background(), userID, json.RawMessage(`{"backfill_history":true}`), 0)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got := receive(t, events); got != version {
		t.Fatalf("event = %d, want %d", got, version)
	}
}

func TestSubscriberCancelledGetsNoUpdate(t *testing.T) {
	t.Parallel()

	notifier := pubsub.NewNotifier()
	svc := newService(t, notifier)
	userID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	events := svc.Subscribe(ctx, userID)

	cancel()
	waitClosed(t, events)
	if _, err := svc.Update(context.Background(), userID, json.RawMessage(`{"backfill_history":true}`), 0); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if v, ok := <-events; ok {
		t.Fatalf("got event %d after cancel", v)
	}
}

func TestNotifierOtherUser(t *testing.T) {
	t.Parallel()

	notifier := pubsub.NewNotifier()
	userID := uuid.New()
	events := notifier.Subscribe(t.Context(), userID)

	notifier.Publish(uuid.New(), 1)
	notifier.Publish(userID, 2)
	if got := receive(t, events); got != 2 {
		t.Fatalf("event = %d, want 2", got)
	}
}

func TestNotifierEverySubscriber(t *testing.T) {
	t.Parallel()

	notifier := pubsub.NewNotifier()
	userID := uuid.New()
	first := notifier.Subscribe(t.Context(), userID)
	second := notifier.Subscribe(t.Context(), userID)

	notifier.Publish(userID, 1)
	for _, events := range []<-chan int64{first, second} {
		if got := receive(t, events); got != 1 {
			t.Fatalf("event = %d, want 1", got)
		}
	}
}

// A subscriber that does not read keeps only the latest version, and Publish never blocks.
func TestNotifierSlowSubscriberGetsLatest(t *testing.T) {
	t.Parallel()

	notifier := pubsub.NewNotifier()
	userID := uuid.New()
	events := notifier.Subscribe(t.Context(), userID)

	for _, v := range []int64{1, 3, 2} {
		notifier.Publish(userID, v)
	}
	if got := receive(t, events); got != 3 {
		t.Fatalf("event = %d, want 3", got)
	}
	select {
	case v := <-events:
		t.Fatalf("got a second event %d", v)
	default:
	}
}
