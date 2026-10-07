package deliverystatus

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// Service keeps the delivery status each user's hottell binary reports and reads it back
// for the send line of the interface.
type Service struct {
	statuses Statuses
	clock    Clock
}

// NewService returns a Service on its ports.
func NewService(statuses Statuses, clock Clock) *Service {
	return &Service{statuses: statuses, clock: clock}
}

// Report keeps report, its texts made fit to keep, as the delivery status of the user with
// userID in place of the previous one, taken now; it returns the status as kept.
// domain.ErrUserNotFound when there is no such user.
func (s *Service) Report(
	ctx context.Context, userID uuid.UUID, report domain.DeliveryReport,
) (domain.DeliveryStatus, error) {
	status := domain.DeliveryStatus{DeliveryReport: domain.NewDeliveryReport(report), ReportedAt: s.clock.Now().UTC()}
	if err := s.statuses.Save(ctx, userID, status); err != nil {
		return domain.DeliveryStatus{}, fmt.Errorf("save delivery status: %w", err)
	}
	return status, nil
}

// Latest returns the last delivery status of the user with userID, and false when their
// binary has never reported one.
func (s *Service) Latest(ctx context.Context, userID uuid.UUID) (domain.DeliveryStatus, bool, error) {
	status, ok, err := s.statuses.Get(ctx, userID)
	if err != nil {
		return domain.DeliveryStatus{}, false, fmt.Errorf("get delivery status: %w", err)
	}
	return status, ok, nil
}
