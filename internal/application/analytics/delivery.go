package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// The states of a person's sending.
const (
	// DeliveryOK: records arrive and the binary reports no trouble.
	DeliveryOK = "ok"
	// DeliveryProblem: the binary reports a failed send or a refused key.
	DeliveryProblem = "problem"
	// DeliveryStale: the binary has not reported for deliveryStaleAfter.
	DeliveryStale = "stale"
	// DeliveryNotConfigured: hottell does not send: no collector key in use, or it is revoked.
	DeliveryNotConfigured = "not_configured"
)

// The reasons of a state.
const (
	reasonNotConnected = "hottell не подключён"
	reasonKeyRevoked   = "ключ коллектора отозван"
	reasonStale        = "hottell давно не сообщал статус"
	problemRefusedKey  = "сервер не принял ключ коллектора — отправка остановлена"
	problemLastError   = "последняя отправка не удалась: "
)

const (
	// deliveryStaleAfter is how old the binary's last report may be before the state is stale;
	// the binary reports every minute.
	deliveryStaleAfter = 5 * time.Minute
	// receivedLookback is how far back the last received records are looked for.
	receivedLookback = 30 * day
	// problemTextMax is the length of the binary's error text in a problem.
	problemTextMax = 300
)

// Delivery is how sending works for a person, by what the server knows: the keys' last use, the
// last records taken by agent and source, the stored settings version, and the queue, the
// applied settings version and the problems of the binary's last report.
type Delivery struct {
	State               string             `json:"state"`
	Reason              string             `json:"reason,omitempty"`
	CheckedAt           time.Time          `json:"checked_at"`
	LastReceived        []LastReceivedItem `json:"last_received"`
	IngestKeyLastUsedAt *time.Time         `json:"ingest_key_last_used_at,omitempty"`
	MCPKeyLastUsedAt    *time.Time         `json:"mcp_key_last_used_at,omitempty"`
	SettingsVersion     *int64             `json:"settings_version,omitempty"`
	// AppliedSettingsVersion is the settings version the binary last reported applying; nil
	// without a report or when the binary could not read its settings cache.
	AppliedSettingsVersion *int64         `json:"applied_settings_version,omitempty"`
	Queue                  *DeliveryQueue `json:"queue,omitempty"`
	Problems               []string       `json:"problems,omitempty"`
}

// LastReceivedItem is when the store last took a record of an agent from a source: hooks,
// transcripts or otel.
type LastReceivedItem struct {
	Agent  string    `json:"agent"`
	Source string    `json:"source"`
	At     time.Time `json:"at"`
}

// DeliveryQueue is the binary's queue as it last reported it; a count is nil when it could not
// read it.
type DeliveryQueue struct {
	Records *int64 `json:"records,omitempty"`
	Bytes   *int64 `json:"bytes,omitempty"`
}

// DeliveryPorts are the reads of the delivery status.
type DeliveryPorts struct {
	Keys     Keys
	Received Received
	Settings Settings
	Reports  DeliveryReports
}

// Option sets an optional part of the Service.
type Option func(*Service)

// WithDelivery gives the Service the reads of the delivery status.
func WithDelivery(p DeliveryPorts) Option {
	return func(s *Service) { s.delivery = p }
}

// errNoDelivery: the Service was built without WithDelivery.
var errNoDelivery = errors.New("analytics service has no delivery ports")

// Delivery tells how sending works for the person userID. Not configured when the collector key
// is revoked, or when it was never used and no record arrived; else stale when the binary's last
// report is older than deliveryStaleAfter, a problem when that report names a failed send or a
// refused key, ok otherwise.
func (s *Service) Delivery(ctx context.Context, userID uuid.UUID) (Delivery, error) {
	p := s.delivery
	if p.Keys == nil || p.Received == nil || p.Settings == nil || p.Reports == nil {
		return Delivery{}, errNoDelivery
	}
	now := s.clock.Now()
	d := Delivery{CheckedAt: now.UTC(), LastReceived: []LastReceivedItem{}}

	ingest, hasIngest, err := latestKey(ctx, p.Keys, userID, domain.AccessKeyKindIngest)
	if err != nil {
		return Delivery{}, err
	}
	mcp, hasMCP, err := latestKey(ctx, p.Keys, userID, domain.AccessKeyKindMCP)
	if err != nil {
		return Delivery{}, err
	}
	if hasIngest {
		d.IngestKeyLastUsedAt = ingest.LastUsedAt
	}
	if hasMCP {
		d.MCPKeyLastUsedAt = mcp.LastUsedAt
	}
	received, err := p.Received.LastReceived(ctx, userID, now.Add(-receivedLookback))
	if err != nil {
		return Delivery{}, fmt.Errorf("read the last received records: %w", err)
	}
	for _, r := range received {
		d.LastReceived = append(d.LastReceived, LastReceivedItem{Agent: r.Agent, Source: r.Source, At: r.At.UTC()})
	}
	_, version, err := p.Settings.Get(ctx, userID)
	if err != nil {
		return Delivery{}, fmt.Errorf("read the settings version: %w", err)
	}
	d.SettingsVersion = &version
	report, reported, err := p.Reports.Get(ctx, userID)
	if err != nil {
		return Delivery{}, fmt.Errorf("read the delivery report: %w", err)
	}
	if reported {
		d.Queue = &DeliveryQueue{Records: report.QueueRecords, Bytes: report.QueueBytes}
		d.AppliedSettingsVersion = report.SettingsVersion
		if report.Unauthorized {
			d.Problems = append(d.Problems, problemRefusedKey)
		}
		if report.LastError != "" {
			d.Problems = append(d.Problems, problemLastError+Clean(report.LastError, problemTextMax))
		}
	}

	switch {
	case hasIngest && ingest.RevokedAt != nil:
		d.State, d.Reason = DeliveryNotConfigured, reasonKeyRevoked
	case (!hasIngest || ingest.LastUsedAt == nil) && len(received) == 0:
		d.State, d.Reason = DeliveryNotConfigured, reasonNotConnected
	case reported && now.Sub(report.ReportedAt) > deliveryStaleAfter:
		d.State, d.Reason = DeliveryStale, reasonStale
	case len(d.Problems) > 0:
		d.State, d.Reason = DeliveryProblem, d.Problems[0]
	default:
		d.State = DeliveryOK
	}
	return d, nil
}

// latestKey is Keys.Latest with false for a person who never had a key of kind.
func latestKey(ctx context.Context, keys Keys, userID uuid.UUID, kind domain.AccessKeyKind) (domain.AccessKey, bool, error) {
	key, err := keys.Latest(ctx, userID, kind)
	if errors.Is(err, domain.ErrAccessKeyNotFound) {
		return domain.AccessKey{}, false, nil
	}
	if err != nil {
		return domain.AccessKey{}, false, fmt.Errorf("read the %s key: %w", kind, err)
	}
	return key, true, nil
}
