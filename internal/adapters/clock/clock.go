// Package clock tells the system time.
package clock

import (
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
)

var _ auth.Clock = System{}

// System is the system clock in UTC.
type System struct{}

// Now returns the current system time in UTC.
func (System) Now() time.Time {
	return time.Now().UTC()
}
