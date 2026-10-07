// Package registry rebuilds a user's proposal registry from the user's published Deep reports
// and decision journal (docs/specs/deep-review, «Реестр как проекция»): Merge groups the
// candidates by group_key, Project replays the journal onto each proposal, the coach's records
// included. The registry is a projection: rebuilt from the same reports and journal, it is the
// same.
package registry
