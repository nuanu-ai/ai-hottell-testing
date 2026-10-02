package mcpclient

import (
	"context"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/hottell/queue"
	"git.alva.dev/alva/harness-telemetry/internal/hottell/sender"
)

// toolDeliveryStatus is the tool the binary reports its sending with.
const toolDeliveryStatus = "report_delivery_status"

// DeliveryReport is the input of report_delivery_status (mcp.md): how the sending goes.
type DeliveryReport struct {
	// QueueRecords and QueueBytes are the queue; null when it could not be read.
	QueueRecords *int64 `json:"queue_records"`
	QueueBytes   *int64 `json:"queue_bytes"`
	// LastSuccessAt is when the service last took a request; nil before the first.
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	// LastError is the last request the service did not take; "" after a success.
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
	// Unauthorized: the service refused the collector token and sending stopped.
	Unauthorized bool `json:"unauthorized"`
	// SettingsVersion is the version of the settings applied, 0 without settings; null
	// when the settings cache could not be read.
	SettingsVersion *int64 `json:"settings_version"`
	BinaryVersion   string `json:"binary_version"`
}

// NewDeliveryReport builds the report from the sender's status, the queue's counts, the
// version of the settings applied and the binary's version. Counts that could not be
// read are nil and go out as null.
func NewDeliveryReport(status sender.Status, stats *queue.Stats, settingsVersion *int64, version string) DeliveryReport {
	report := DeliveryReport{
		Unauthorized:    status.Unauthorized != "",
		SettingsVersion: settingsVersion,
		BinaryVersion:   version,
	}
	if stats != nil {
		records, bytes := int64(stats.Queued), stats.QueuedBytes
		report.QueueRecords, report.QueueBytes = &records, &bytes
	}
	if !status.LastSuccess.IsZero() {
		at := status.LastSuccess.UTC()
		report.LastSuccessAt = &at
	}
	if status.LastError != nil {
		at := status.LastError.At.UTC()
		report.LastError, report.LastErrorAt = status.LastError.Reason, &at
	}
	return report
}

// ReportDeliveryStatus tells the service how the sending goes with report_delivery_status
// and returns when the service took the report. Its outcome is not recorded in the status:
// a failed report is the daemon's log line, not a problem hottell status shows, and a
// successful one does not clear a problem of the subscription.
func (c *Client) ReportDeliveryStatus(ctx context.Context, report DeliveryReport) (time.Time, error) {
	var out struct {
		ReportedAt time.Time `json:"reported_at"`
	}
	if err := c.callToolUnrecorded(ctx, toolDeliveryStatus, report, &out); err != nil {
		return time.Time{}, err
	}
	return out.ReportedAt, nil
}
