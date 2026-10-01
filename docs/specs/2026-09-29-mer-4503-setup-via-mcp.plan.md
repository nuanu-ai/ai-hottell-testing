# MER-4503 — plan

Spec: `docs/specs/2026-09-29-mer-4503-setup-via-mcp.md` (blob `0c46463`, commit `15405ec`).
Base: `origin/camp` @ `3f62dc8`. All code in `hottell/` (package `main`, go-sdk v1.8.0).

Owner rulings (live channel, 2026-09-29): "no review stops, do it fast, a quick prototype" —
recorded as an autonomous grant from the plan through the branch gate and a draft PR; the edge
(merge into `camp`, a release tag, the live run on this Mac) keeps its own word. Scope addition
under the same word: `uninstall` disables native OTel of both agents (design ask FC14; reuses
`otel_configure`'s disable path).

Test-first everywhere: each task's tests are written first and seen failing for the expected
reason, then the code. Every task ends with `cd hottell && go vet ./... && go test ./...` green.

## Waves

- Wave 1 (parallel, disjoint files): T1, T5.
- Wave 2 (sequential): T2, then T4.
- Wave 3: T3, then T6.

## Tasks

**T1 — Token validator and token sources.** New `token.go`, `token_test.go`; `main.go` gains the
`token` subcommand only.
- `validateToken(ctx, logsURL, token string) tokenOutcome` — candidate in memory; POST
  `{"resourceLogs":[]}`; redirects refused (`CheckRedirect` returns `http.ErrUseLastResponse`,
  3xx → unknown); 2xx with no `partialSuccess.rejectedLogRecords > 0` → accepted; 401/403 →
  rejected; anything else or a network error → unknown (with status/err text, token scrubbed).
- `tokenSource` interface `Ask(ctx) (string, acquireOutcome)`; outcomes accepted / cancelled /
  unavailable / timeout / invalid. `dialogSource` runs `/usr/bin/osascript -e 'text returned of
  (display dialog "…" default answer "" with hidden answer giving up after 45)'` with
  `exec.CommandContext`; exit with `-128` in stderr → cancelled; `gave up` → timeout; other
  failure → unavailable; empty or >4 KiB answer → invalid. The token never appears in argv.
- `hottell token`: reads one line from stdin — echo off via `stty -echo` only when stdin is a
  terminal, restored on every exit; validates; saves to the token file 0600 (dir 0700, chmod
  existing); prints only the outcome.
- `writeSecretFile(path, data)` helper: unique temp file in the same dir, 0600, rename, then
  `chmod 0600` on the target.
- Check: `go test -run 'Token|Secret' ./...` covers every outcome with httptest and a fake
  `osascript` via an overridable command path.

**T5 — Bootstrap script and release.** New `hottell/install.sh`; `.github/workflows/release.yml`.
- `install.sh` (POSIX sh, `set -eu`): `TAG=__HOTTELL_TAG__` placeholder; refuses without `gh`
  or `gh auth status`; maps `uname -m` (`arm64`→`arm64`, `x86_64`→`amd64`, else refuse); downloads
  `hottell-darwin-$ARCH` and `SHA256SUMS` of `$TAG` into a `mktemp -d` dir; requires the asset's
  line in `SHA256SUMS`; `shasum -a 256 -c`; `chmod +x`; `xattr -d com.apple.quarantine` (ignore
  absence); runs `./hottell-darwin-$ARCH install`; removes the temp dir on exit.
- `release.yml`: substitutes the tag into a copy of `install.sh` and publishes it as an asset
  next to the binaries (not included in `SHA256SUMS` — the script is authenticated by `gh`).
- Check: `sh -n hottell/install.sh`; `shellcheck hottell/install.sh` (if installed, else say so);
  a test script with a fake `gh` on `PATH` shows refusal without `gh` and failure on a checksum
  mismatch.

**T2 — Setup core.** New `setup.go`, `setup_test.go`; small edits in `otel.go`, `config.go`,
`tomledit.go`.
- `Config.SetupFolder string json:"setup_folder,omitempty"`.
- `runSetup(ctx, p paths, in setupInput, src tokenSource) setupResult` — quiet (no stdout).
  `setupInput{Scope, Folder, SendPrompts, HostName}`; validation: scope ∈ {all, folder}; folder
  required for `folder`, absolute after `~` expansion, an existing directory.
  Steps in order, each recorded in `result.Applied` when it changed something:
  1. token: keep a saved token if `validateToken` says accepted; saved token + unknown → stop
     "collector unreachable" (no dialog); none or rejected → one `src.Ask`; validate; save only on
     accepted; any other outcome stops with its message, nothing written.
  2. config.json: scope (`all`/`marked`), send_prompts, host_name, setup_folder — written only if
     changed (`writeSecretFile`-style atomic write, 0600).
  3. marker: for `folder`, create an empty `.hottell` if absent; never rewrite an existing one.
  4. hooks: `mergeConfigFile` for both agents (quiet).
  5. native OTel: Claude — set `OTEL_EXPORTER_OTLP_ENDPOINT` to the canonical base (config
     endpoint minus `/v1/logs`) then `OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer <token>`,
     `host.name=<name>` inside `OTEL_RESOURCE_ATTRIBUTES` preserving other pairs, prompts key;
     Codex — refuse a split `[otel]` family (the family's tables not contiguous) without mutation,
     else render with the canonical base, current token, prompts. Both files chmod 0600 after
     write. Identical inputs → byte-identical files, no write.
  6. smoke: `SetupCheck` event via the validator's HTTP path; outcome in the result.
  `setupResult` carries: applied steps, failed step + message (token scrubbed), what is collected
  from where (the sentence from spec §Decision 4), prompts, machine name, other markers found
  upward/downward are not searched — only the old `setup_folder` marker if it differs, smoke
  outcome, manual steps.
- Check: tests in a temp HOME with httptest collector and a stub `tokenSource`: fresh setup;
  identical re-run writes nothing (mtimes unchanged); token rotation updates both agents' headers;
  rejected/cancelled/unknown paths write nothing; split TOML refused; injected failure at step 5
  (Codex config path is a directory) reports applied steps 1–4 and a re-run after fixing converges;
  every token-bearing file 0600 (pre-created 0644); token absent from the result JSON.

**T4 — Silent install and uninstall.** `install.go`, `install_test.go`, `main.go` flag help text.
- `runInstall` without token flags: binary copy, MCP registration, config if absent; no token, no
  hooks; last line "open Claude Code or Codex and run setup (in Claude Code: /hottell:setup)".
  All printing stays in the CLI function; the token-flag path unchanged.
- `runUninstall`: before removing dirs, disables native OTel of both agents through the existing
  `otel_configure` disable path (`Enabled=false`), then as today.
- Check: install without flags in a temp HOME writes no token and no hooks and registers MCP;
  second run no change; uninstall removes Claude OTel keys and Codex `[otel]` family.

**T3 — MCP surface and status.** `mcp.go`, new `status.go`, `status_test.go`, `mcp_test.go`;
`install.go` `runStatus` renders from the same core.
- `collectStatus(ctx, p) statusReport` (quiet): configured-on-disk; token accepted / rejected /
  unknown / absent (live via `validateToken`); scope, setup_folder, prompts, host name; hooks per
  agent; native OTel per agent; spool depth; `pending` note.
- MCP: tool `setup` (input schema without any token field; description tells the agent to ask the
  four questions, show current values from `status` as defaults, then call once), tool `status`,
  prompt `setup` (same instruction text), `ServerOptions.Instructions` ("if status says not
  configured, offer setup"). Tool errors scrubbed of the token.
- Check: in-process client lists tools `setup`, `status`, prompt `setup`, and sees the
  instructions; schema of `setup` has no property matching `token`; a subprocess test builds the
  binary, runs `hottell mcp` with a temp HOME, sends initialize + tools/call status, and asserts
  every stdout line parses as JSON-RPC.

**T6 — README.** `hottell/README.md`: install-from-release and rollout sections lead with the
two-step flow (`install.sh` command, then `/hottell:setup` / ask in Codex); token-file path as the
scripted alternative; remove the claim that install leaves the token untouched; MCP tools table
gains `setup` and `status`; uninstall section notes native OTel is disabled.
- Check: diff read at the branch gate.

## Close-time (edge, owner's word)

Merge into `camp`; tag a release; on this Mac run the published command, `/hottell:setup`, enter
the token in the dialog, and query ClickHouse for this session's events under the chosen machine
name (spec Done when 11).

## Plan-gate relocation (one pass, 2026-09-29, verdict changes_requested, no P0)

Every finding relocates into a check the implementation proves:

- **T1:** the AppleScript keeps the dialog record and checks `gave up` before returning text;
  timeout is reported through a separate non-secret outcome; tests cover timeout with an empty
  and with a populated field (fake `osascript`).
- **T2:** before any mutation, the Codex config is preflighted for every `otel` representation
  other than contiguous `[otel]` / `[otel.*]` table headers — inline table (`otel = {…}`), dotted
  keys (`otel.x = …`), quoted headers (`["otel"]`), split families — and refused with unchanged
  bytes; the inline-table example is a test.
- **T4:** uninstall removes **every** `[otel]` / `[otel.*]` table in the Codex config, not only
  the first contiguous block (test with a split family), and refuses (reporting, not deleting)
  the unsupported forms of T2; project-local Claude settings written by `project_scope` are not
  searched — uninstall's output and the README say that `project_scope off` must be run in such
  projects first.
- **T5:** verification selects the downloaded asset's exact line from `SHA256SUMS` and checks only
  it; a success-path test uses a two-architecture manifest with one downloaded binary;
  `shellcheck` runs in the release workflow (CI), and locally when installed.
