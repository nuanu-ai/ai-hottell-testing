package domain

import (
	"strings"
	"time"
)

// MaxDeliveryErrorLength is how many characters of the last send error are kept; a longer
// one is cut.
const MaxDeliveryErrorLength = 500

// DeliveryReport is what the hottell binary tells of its sending (mcp.md,
// report_delivery_status): the queue, the last success and failure of the sender, whether
// the service refused the collector token, and the settings version and binary version
// it runs.
type DeliveryReport struct {
	// QueueRecords and QueueBytes are the binary's queue; nil when it could not read it.
	QueueRecords *int64
	QueueBytes   *int64
	// LastSuccessAt is when the service last took a request; nil before the first.
	LastSuccessAt *time.Time
	// LastError is the last request the service did not take, "" after a success.
	LastError string
	// LastErrorAt is when LastError happened, as reported; nil when not reported.
	LastErrorAt *time.Time
	// Unauthorized: the service refused the collector token and sending stopped.
	Unauthorized bool
	// SettingsVersion is the version of the settings the binary applies, 0 when it has
	// none; nil when it could not read its settings cache.
	SettingsVersion *int64
	BinaryVersion   string
}

// NewDeliveryReport returns report fit to keep: its texts without NUL characters, which a
// text column refuses, and LastError cut to MaxDeliveryErrorLength characters.
func NewDeliveryReport(report DeliveryReport) DeliveryReport {
	lastError := withoutNUL(report.LastError)
	if runes := []rune(lastError); len(runes) > MaxDeliveryErrorLength {
		lastError = string(runes[:MaxDeliveryErrorLength])
	}
	report.LastError = lastError
	report.BinaryVersion = withoutNUL(report.BinaryVersion)
	return report
}

func withoutNUL(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}

// DeliveryStatus is the last DeliveryReport of a user and when the service took it.
type DeliveryStatus struct {
	DeliveryReport

	ReportedAt time.Time
}
