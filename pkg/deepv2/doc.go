// Package deepv2 is the Go port of the Deep 2.0 report contract of ai-hottell@c3c8357
// (analytics/v2_contract.py and analytics/v2_skills.py): it checks the shape of a Deep
// report, a proposal registry and an independent review against a frozen session source,
// and derives the proposal group key. It checks provenance and state coherence only; whether
// the agent read the session right needs a separate review.
//
// Reasons in errors keep the substrings of the Python version, so its tests port one to one.
// The package uses the standard library and golang.org/x/text only (docs/specs/deep-review).
package deepv2
