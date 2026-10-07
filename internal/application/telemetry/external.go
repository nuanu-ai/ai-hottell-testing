// Package telemetry holds the use cases that read the telemetry the collector stores in
// ClickHouse and the ports they reach it through. Every port answers
// telemetry.ErrUnavailable of internal/domain/telemetry while the store cannot be read.
package telemetry
