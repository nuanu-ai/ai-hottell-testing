// Package hottell holds the code of the hottell binary, which collects telemetry of
// Claude Code and Codex on the user's Mac and sends it to the service.
//
// The binary does not belong to the service: nothing under internal/hottell may import
// the server's domain, application, adapters or delivery packages.
package hottell
