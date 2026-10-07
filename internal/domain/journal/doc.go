// Package journal is the domain of the server's decision journal (docs/specs/deep-review,
// «Журнал решений»): one append-only journal with a hash chain, holding the lifecycle events
// of proposals — the port of ai-hottell@c3c8357:local/v2_lifecycle.py — and the records of
// the coach — the port of internal/hottell/local/coach/journal.go. Any edit of the history,
// a changed text, a dropped or a moved record, breaks the chain.
//
// Reasons in errors keep the substrings of the Python version, so its tests port one to one.
// The package uses the standard library, pkg/deepv2 and pkg/redact only.
package journal
