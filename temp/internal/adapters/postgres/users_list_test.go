package postgres_test

import (
	"testing"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/adapters/postgres"
)

func TestUsers_List(t *testing.T) {
	t.Parallel()

	// Interleaved, so the order cannot come from insertion alone. Parallel tests add their
	// own users, so the check looks at these four among the rest.
	activeOld, invitedOld, activeNew, invitedNew := createActive(t), createInvited(t), createActive(t), createInvited(t)
	want := []uuid.UUID{invitedNew.ID, invitedOld.ID, activeNew.ID, activeOld.ID}

	got, err := postgres.NewUsers(testDB.Pool()).List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if !prev.HasPassword && cur.HasPassword {
			continue
		}
		if prev.HasPassword && !cur.HasPassword {
			t.Fatalf("List() puts invited %s after active %s", cur.ID, prev.ID)
		}
		if prev.CreatedAt.Before(cur.CreatedAt) {
			t.Fatalf("List() puts %s created at %v before %s created at %v", prev.ID, prev.CreatedAt, cur.ID, cur.CreatedAt)
		}
	}

	var ours []uuid.UUID
	for _, user := range got {
		for _, id := range want {
			if user.ID == id {
				ours = append(ours, id)
			}
		}
	}
	if len(ours) != len(want) {
		t.Fatalf("List() holds %d of the created users, want %d", len(ours), len(want))
	}
	for i := range want {
		if ours[i] != want[i] {
			t.Fatalf("List() orders the created users %v, want %v", ours, want)
		}
	}
}
