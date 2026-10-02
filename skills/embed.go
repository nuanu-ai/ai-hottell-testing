// Package skills holds the agent skills the hottell binary lays out for Codex and Claude Code:
// hottell-coach talks over what gets in the way, session-retro runs the Deep 2.0 review of a session;
// both work through the hottell server and hottell-local.
package skills

import "embed"

// FS holds one directory per skill, each with its SKILL.md and the files it refers to.
//
//go:embed hottell-coach session-retro
var FS embed.FS
