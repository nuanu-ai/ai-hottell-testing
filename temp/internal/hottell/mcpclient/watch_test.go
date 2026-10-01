package mcpclient_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/mcpclient"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/state"
)

const waitFor = 15 * time.Second

// watch runs c.Watch until the test ends and returns the caches onChange got.
func watch(t *testing.T, c *mcpclient.Client) <-chan state.SettingsCache {
	t.Helper()
	changes := make(chan state.SettingsCache, 16)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- c.Watch(ctx, func(cache state.SettingsCache) { changes <- cache })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Watch = %v, want context.Canceled", err)
		}
	})
	return changes
}

func nextChange(t *testing.T, changes <-chan state.SettingsCache, version int64) {
	t.Helper()
	select {
	case cache := <-changes:
		if cache.Version != version {
			t.Fatalf("onChange got version %d, want %d", cache.Version, version)
		}
	case <-time.After(waitFor):
		t.Fatalf("no onChange with version %d", version)
	}
}

func noChange(t *testing.T, changes <-chan state.SettingsCache) {
	t.Helper()
	select {
	case cache := <-changes:
		t.Fatalf("onChange got version %d, want none", cache.Version)
	case <-time.After(300 * time.Millisecond):
	}
}

// eventually polls cond until it holds or waitFor passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func notify(t *testing.T, ts *testServer) {
	t.Helper()
	err := ts.mcp.ResourceUpdated(t.Context(), &mcp.ResourceUpdatedNotificationParams{URI: settingsURI})
	if err != nil {
		t.Fatalf("ResourceUpdated: %v", err)
	}
}

func TestWatchNotification(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	c, paths := newClient(t, claudeConfig(t, ts.URL, goodKey))
	changes := watch(t, c)

	// The first read after subscribing reports the settings, and the token is fetched.
	nextChange(t, changes, 12)
	if ts.subscribes.Load() != 1 {
		t.Errorf("subscribes = %d, want 1", ts.subscribes.Load())
	}
	if creds, err := paths.ReadCredentials(); err != nil || creds.CollectorToken != testToken {
		t.Errorf("credentials = %+v, %v; want the collector token", creds, err)
	}

	// A notification without a new version reads the resource but reports nothing.
	notify(t, ts)
	noChange(t, changes)

	ts.settings.Store(settingsV13)
	notify(t, ts)
	nextChange(t, changes, 13)
	if saved, err := paths.ReadSettings(); err != nil || string(saved.Document) != settingsV13 {
		t.Errorf("settings cache = %s, %v; want the version 13 document", saved.Document, err)
	}
}

func TestWatchReconnects(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	c, paths := newClient(t, claudeConfig(t, ts.URL, goodKey))
	c.SetMaxRetries(1)
	c.SetPauses(50*time.Millisecond, 200*time.Millisecond, time.Hour, time.Hour)
	changes := watch(t, c)
	nextChange(t, changes, 12)

	// The network goes, the settings change meanwhile, and no notification reaches the
	// binary: it reads them after reconnecting.
	ts.drop.Store(true)
	ts.CloseClientConnections()
	eventually(t, "the status tells the network is lost", func() bool {
		st := readStatus(t, paths)
		return st.Problem != nil && st.Problem.Reason == mcpclient.ReasonUnreachable
	})
	ts.settings.Store(settingsV13)
	ts.drop.Store(false)

	nextChange(t, changes, 13)
	if got := ts.subscribes.Load(); got < 2 {
		t.Errorf("subscribes = %d, want a new subscription after reconnecting", got)
	}
	eventually(t, "the status is clear again", func() bool { return readStatus(t, paths).Problem == nil })

	// The new subscription delivers notifications.
	ts.settings.Store(`{"version":14}`)
	notify(t, ts)
	nextChange(t, changes, 14)
}

func TestWatchRevokedKey(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	sources := claudeConfig(t, ts.URL, goodKey)
	c, paths := newClient(t, sources)
	c.SetMaxRetries(1)
	// A 401 waits its own pause, never the short reconnect one.
	c.SetPauses(time.Hour, time.Hour, 100*time.Millisecond, time.Hour)
	changes := watch(t, c)
	nextChange(t, changes, 12)

	// The user revokes the key and issues a new one; the agent's config still has the old.
	ts.key.Store(newKey)
	ts.CloseClientConnections()
	eventually(t, "the status tells the key is revoked", func() bool {
		st := readStatus(t, paths)
		return st.Problem != nil && st.Problem.Reason == mcpclient.ReasonUnauthorized
	})

	// The new key lands in the agent's config: the next attempt reads it, fetches the
	// collector token of the new key and reads the settings.
	ts.settings.Store(settingsV13)
	// The config is replaced by a rename, so that a reconnect never reads it half written.
	fresh := claudeConfig(t, ts.URL, newKey)
	if err := os.Rename(fresh[0].Path, sources[0].Path); err != nil {
		t.Fatal(err)
	}
	nextChange(t, changes, 13)
	if creds, err := paths.ReadCredentials(); err != nil || creds.CollectorToken != newKeyToken {
		t.Errorf("credentials = %+v, %v; want the token fetched with the new key", creds, err)
	}
	if st := readStatus(t, paths); st.Problem != nil {
		t.Errorf("status = %+v, want the problem cleared", st)
	}
}
