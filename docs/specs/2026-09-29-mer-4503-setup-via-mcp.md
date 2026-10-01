# MER-4503 — Onboarding in one dialog: `setup` via MCP

Main: MER-4503 — reproduced: yes. On `camp` @ `3f62dc8`: `hottell install` without a token exits 1
with "токен обязателен" and does nothing else (`hottell/install.go:190-201`); no MCP tool sets the
token, installs hooks or sets the global scope (`hottell/mcp.go:16-21`, `otel.go:296-302`,
`scope.go:229`); `project_scope on` for a parent folder writes the `.hottell` marker (which already
covers new projects inside it for hooks, `scope.go:33-52`) and `<folder>/.claude/settings.local.json`
(`scope.go:112-156`), which a new project created inside that folder does not read — so native
OTel is not covered; with the
default `scope: marked` a fresh install sends nothing until a marker exists (`scope.go:86-87`,
`config.go:53`). Who meets it: a new user on a Mac at first install, who holds the token and must
know four separate mechanisms. Intake verdict: reshape — native OTel is global, only hooks are
scoped by folder; the spec must not promise per-folder native OTel.

## Required slots

- **Slot 1 — Path:** full cycle — per the package threshold: "secrets" (the token is entered and
  stored by the new flow). The gate judges the diff against: card MER-4503 (its intent paragraph,
  agreed with the owner on 2026-09-29) and this spec.
- **Slot 2 — Design review:** predicate not met — `surfaces.ui` is empty; the flow is a
  conversational/CLI surface, no screen or form is built. The macOS dialog is a system dialog.
- **Slot 3 — Evidence:** the profile declares no `completion.evidence_kind`; each §Done when item
  names its evidence. Phase proofs: the owner's word on the retelling; the plan's single gate pass
  with triage; the branch-gate verdict.
- **Slot 4 — Performance budget:** predicate not met — `surfaces.performance_sensitive` is empty.
- **Slot 5 — Decision record:** the rule "native OTel is always global, folder scope applies to
  hooks only" is a boundary later work inherits. The repository declares no `decisions.dir`; the
  decision is recorded here (§Decision) and on the card. Visibly incomplete by the letter of the
  slot, as in MER-4456.

## The problem

Today connecting a Mac takes about nine steps in three places: download the release with `gh`,
`shasum`, `chmod`, `xattr`; `hottell install -token-file`; `otel_configure`; `project_scope` or a
hand-made `.hottell`; `/hooks` trust in Codex; restart sessions. "Project" means three different
things (marker for hooks, per-project `settings.local.json` for Claude native OTel, global-only
for Codex native OTel). A user cannot connect without knowing the internals.

## Decision

1. **Two steps: a silent install, then `setup` in the agent.**
   - `hottell install` without a token becomes valid: it copies the binary to `~/.local/bin`,
     registers the MCP server in both agents and creates `config.json` if absent. It asks nothing,
     writes no token, installs no hooks, and ends by printing one line: open Claude Code or Codex
     and run setup. The existing `install -token-file | -token-stdin` keeps its current behaviour
     (non-interactive path for scripted rollout).
   - A bootstrap script `install.sh`, published as a release asset, is the "one command":
     `gh release download -R nuanu-ai/ai-hottell -p install.sh -O /tmp/hottell-install.sh
     --clobber && sh /tmp/hottell-install.sh` (no pipe, so a failed download runs nothing). The
     script carries the tag of the release it was published with, downloads that release's binary
     for `uname -m`, verifies it against `SHA256SUMS`, removes the quarantine attribute and runs
     `hottell install`. The repository is private, so `gh` with an
     authenticated account remains a prerequisite; the script checks for it and says so.
2. **`setup` is one MCP tool plus one MCP prompt.**
   - Tool `setup` with inputs `scope` (`all` | `folder`), `folder` (path, for `folder`),
     `send_prompts` (bool), `host_name` (string). **It has no token input.** The tool
     description instructs the agent to ask the user these four questions first (with the current
     values as defaults on a re-run) and then call it once.
   - Prompt `setup` carries the same instruction. Claude Code exposes it as `/hottell:setup`;
     in Codex, prompts as slash commands are not established, and the user asks in words. Explicit
     invocation of the `setup` tool is the supported entry point.
   - Server `Instructions` (MCP initialize) tell the agent: if `status` reports "not configured",
     offer setup. This is guidance to the model, not guaranteed behaviour.
3. **The token never passes through the chat.** `setup` obtains it itself:
   - if a token file exists and the collector accepts it, it is kept (the user is told "token
     kept"); otherwise
   - hottell shows **one** macOS dialog per `setup` call with a hidden answer field (`osascript`,
     `display dialog … with hidden answer giving up after 45` — under Codex's 60 s default MCP tool
     timeout); the token is read from the dialog's output, never from arguments, never echoed,
     never included in the tool result or in any log line;
   - before saving, the candidate token (held in memory) is checked with a POST of an empty OTLP
     payload to `/v1/logs` of the canonical endpoint, redirects refused: 2xx without an OTLP
     partial-success rejection → saved to `~/.config/hottell/token` (dir 0700, file 0600);
     401/403 → nothing is saved and `setup` ends with "token rejected — run setup again"; any
     other status or a network error → nothing is saved or replaced, and `setup` ends naming the
     endpoint and the status. Cancel in the dialog ends `setup` with "cancelled", writing nothing.
     A saved token that fails validation because of the network does not open the dialog;
     `setup` ends with "collector unreachable". (The collector's contract was observed on
     2026-09-29: no token → 401, a valid token with an empty payload → 200.);
   - no GUI (dialog fails, e.g. an SSH session) → `setup` fails with a message telling the user to
     run `hottell token` in a terminal; `hottell token` reads the token from the terminal without
     echo, validates it the same way and saves it; then `setup` is run again.
4. **What `setup` configures, in one call, idempotently:**
   - config: `scope` (`all` → `all`; `folder` → `marked`), `send_prompts`, `host_name`;
   - for `folder`: the folder is stored in config (`setup_folder`) and an empty `.hottell`
     marker is created there if absent — an existing marker's content is never rewritten. The
     existing marker semantics apply: the nearest marker upward wins, so present and future
     projects inside are covered unless a nearer marker says otherwise, and other marked trees
     also send. Changing the folder on a re-run does not remove the old marker; `status` lists it;
   - hooks of both agents (the same merge `install -token-file` does today);
   - native OTel of both agents, **globally**: Claude `env` in `~/.claude/settings.json`, Codex
     `[otel]` in `~/.codex/config.toml`, using the existing `otel_configure` code; unlike today,
     `setup` first sets the endpoint to the canonical base (the one derived from the hook
     endpoint in config) and only then **overwrites** the `Authorization` header with the current
     token, so the token is never sent to a different destination; it sets `host.name` inside
     Claude's `OTEL_RESOURCE_ATTRIBUTES` while keeping the other attributes; `send_prompts` maps to
     `OTEL_LOG_USER_PROMPTS` / Codex `log_user_prompt`. The machine name reaches hottell's own
     events and Claude's native telemetry; Codex native telemetry keeps its own host name (no
     supported persistent mechanism is established);
   - a smoke event (`agent=setup`, `SetupCheck`) sent synchronously; its result is part of the
     tool result.
   Setup is a recoverable multi-file operation, not a transaction: on a failure the result names
   which steps were applied, and a re-run converges from any partial state. An identical re-run
   writes no configuration file; the smoke event is sent on every call.
   The tool result is a plain summary for the user: what is collected and from where ("tokens and
   cost: from everywhere on this Mac, both agents; detailed actions: only from <folder>"), prompts
   on/off, machine name, smoke delivered or not, and the remaining manual steps: in Codex run
   `/hooks` and trust hottell's hooks; start a fresh agent process (optionally with its resume
   command) — settings are pending until then.
5. **`status` becomes the single state report.** A new MCP tool `status` (and `hottell status`
   CLI gaining the same lines) reports: configured yes/no, token accepted / rejected / unknown
   (checked live; unreachable is "unknown", never "rejected"), scope and folder, prompts, machine
   name, hooks per agent, native OTel per agent, spool depth. "Configured" means on disk: token
   file present, hooks of both agents present, native OTel enabled in both agents; it does not
   claim the running agents have loaded it.
6. **Native OTel is always global; folder scope applies to hooks only** (Slot 5). `project_scope`
   keeps working as it does (it remains the way to switch a single project on or off), but
   `setup` does not use its per-project `settings.local.json` write.

## Consistency contract

From the design ask of 2026-09-29 (Codex, answer sha256 `d8f9ac1d…`), claims verified against the
code before entering. Class → guarantee → closer → where it is carried.

- **FC2 bootstrap fails silently** → a download failure never executes anything; one release is
  used throughout → the command is `gh release download -R nuanu-ai/ai-hottell -p install.sh -O
  <tmp> --clobber && sh <tmp>` (no pipe); the release workflow writes its tag into `install.sh`;
  the script maps `x86_64`→`amd64`, requires the asset's line in `SHA256SUMS` and stops on a
  mismatch. Authenticated `gh` is the trust boundary for the script itself → plan task for
  `install.sh`, Done when 7.
- **FC4 stray stdout breaks MCP** → MCP handlers call quiet functions; CLI printing stays in the CLI
  layer → a test runs `hottell mcp` as a subprocess and checks that every stdout line is JSON-RPC
  → Done when 6.
- **FC5 token acquisition outcomes** → accepted / rejected / cancelled / unavailable / timeout /
  unknown are distinct results; nothing is written before acceptance → token-source interface
  with a stub in tests → Done when 3, 4.
- **FC6 validation misreads the collector** → candidate in memory, redirects refused, partial
  success inspected, status table as in §Decision 3 → dedicated validator, httptest cases → Done
  when 3.
- **FC7 secrets outside the boundary** → every file setup writes that holds the token (token file,
  `~/.claude/settings.json`, `~/.codex/config.toml`) ends mode 0600 even if it existed with wider
  permissions; the token string is scrubbed from every returned text, error included → tests →
  Done when 3.
- **FC8 config surgery** → a split or otherwise unsupported `[otel]` family in the Codex TOML is
  refused without mutation, with a message naming the file → test → Done when 2.
- **FC9 partial state** → recoverable operation, see §Decision 4 → a test that fails a middle step
  and re-runs → Done when 2.
- **FC10 scope differs from the choice** → folder persisted, marker never rewritten, other markers
  and nearer markers stated in the result → tests → Done when 2, 5. Prompts-off controls the
  prompt fields of newly captured events only; it is not content redaction and does not rewrite
  queued events.
- **FC11 native exporters diverge** → canonical endpoint before header; Codex host name not
  promised → tests → Done when 2.
- **FC12/FC13 overstated state** → pending activation stated; status states as in §Decision 5;
  smoke success means the collector accepted that one log request (partial success inspected),
  not that native metrics or ClickHouse ingestion work → tests → Done when 5.
- **FC1** is already closed on camp (upward marker search, `scope.go:33-52`, `TestHookScope`);
  the problem statement was corrected.
- **FC3** entry point: see §Decision 2.

## Done when

1. **Silent install:** in a sandbox `HOME`, `hottell install` without flags exits 0, registers MCP
   in both agents' configs, writes no token and no hooks, and prints the "run setup" line; a
   second run changes nothing. Evidence: a Go test over a temp `HOME`, red run then green run.
2. **`setup` applies everything in one call:** with a stub collector (httptest) and a stubbed
   token source, one `setup` call in a temp `HOME` leaves: token file 0600, config with the four
   values, marker in the folder (for `folder`), hooks in both agents, Claude env and Codex `[otel]`
   enabled with the current token in the header and `host.name` set, and the result reports smoke
   delivered. A second call with the same inputs changes no file. Evidence: Go tests, red then
   green. A split `[otel]` family is refused without mutation; a failure injected in a middle
   step leaves a result naming the applied steps, and a re-run converges.
3. **Token handling:** tests prove that (a) a rejected, cancelled or unverifiable token is not
   saved and does not replace a saved one, and the dialog is shown at most once per call; (b) a
   kept valid token is not re-asked, and a network failure on a kept token does not open the
   dialog; (c) redirects and OTLP partial-success rejections are not taken as acceptance; (d) the
   token string appears in neither the tool result, its errors, nor the debug log; (e) every file
   holding the token ends mode 0600, including one that existed with wider permissions; (f) the
   `setup` tool schema has no token field.
   Evidence: Go tests, red then green.
4. **Dialog fallback:** when the dialog cannot run, `setup` fails with the `hottell token`
   instruction and writes nothing; `hottell token` with a token on a non-terminal stdin validates
   and saves it. Evidence: Go tests.
5. **`status`:** the MCP tool and the CLI report "not configured" before `setup` and "configured"
   after it in the sandbox, with the fields of §Decision 5. Evidence: Go tests.
6. **MCP surface:** an in-process MCP client lists the `setup` tool, the `setup` prompt and the
   `status` tool, and the server's initialize result carries the instructions. Evidence: Go test.
7. **Bootstrap script:** `install.sh` passes `sh -n` and `shellcheck`, refuses without `gh`, and
   fails on a checksum mismatch; the release workflow publishes it as an asset. Evidence: the
   checks' output; the workflow diff read at the gate.
8. **Toolchain green:** `go vet ./...` and `go test ./...` in `hottell/` pass. Evidence: command
   output.
9. **README:** the "install from release" and "rollout" sections describe the two-step flow first;
   the token-file path stays documented as the scripted alternative; the false claim that install
   does not touch the token is fixed. Evidence: diff read at the gate.
10. **PR into `camp`** from `eklepov/mer-4503-setup-via-mcp`, branch-gate verdict triaged.
    Evidence: PR link and verdict record.
11. *(close-time, on the owner's word)* **Live run on this Mac:** release cut, `install.sh` run,
    `setup` invoked from Claude Code, token entered in the dialog, events from this session
    visible in ClickHouse under the chosen machine name. Evidence: the query and its output at
    close.

## Non-goals

- Linux and Windows (the dialog and the bootstrap script are macOS-only).
- Filtering native OTel (tokens, cost) by folder — refused by the owner's choice "cost from
  everywhere"; a local OTLP relay would be the way and is a separate card if ever needed.
- Server-side identity (per-user tokens), a machine identifier (`host.id`) — discussed and
  deferred by the owner on 2026-09-29.
- Bypassing Codex's hook trust (`/hooks`) — Codex's protection; setup only tells the user.
- MCP elicitation for any input — client support in Claude Code and Codex is unverified, and form
  data would pass through the client UI; the agent asks the non-secret questions itself.
- Changing `project_scope`, `otel_configure` or `otel_status` behaviour beyond the code `setup`
  reuses.

## Open points

- `install.sh` via `gh release download … -O -` assumes `gh` supports `-O -` (stdout) for a
  release asset; to be confirmed at planning, otherwise download to a temp file.
- Whether Codex surfaces MCP prompts as slash commands — if not, in Codex the user starts setup by
  asking in words; the tool description and server instructions cover that.
