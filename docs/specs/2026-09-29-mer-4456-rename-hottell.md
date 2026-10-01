# MER-4456 — Rename hookship → hottell

Main: MER-4456 — reproduced: yes. The tool's code carries the name `hookship` in 5 Go files
on `camp` @ `212ec87` (install_test.go, config.go, otlp.go, main.go, install.go), the module,
the directory, the README and examples. Intake verdict: keep.

## Required slots

- **Slot 1 — Path:** full cycle — by the owner's word (directive of 2026-09-29: every card runs
  the full agents-developer cycle), recorded here. The gate judges the diff against: card
  MER-4456 with sub-cards MER-4461 and MER-4462, and this spec.
- **Slot 2 — Design review:** predicate not met — no UI surface.
- **Slot 3 — Evidence:** the profile (added by this very stream) declares no
  `completion.evidence_kind`; evidence is named per item under §Done when: command outputs
  read by a fresh reader, and the PR link.
- **Slot 4 — Performance budget:** predicate not met.
- **Slot 5 — Decision record:** the name `hottell` is a boundary later work inherits. The repo
  declares no `decisions.dir`; the decision is recorded on MER-4461 ("owner's decision
  2026-09-29: `hottell`, unconditionally") and restated here. Visibly incomplete by the letter
  of the slot (no decisions directory exists); accepted as such — this spec plus the card are
  the record.

## Scope

1. **Repository profile.** Add `.agents/team-profile.yaml` to ai-hottell so the process skills
   resolve this repository natively: tracker `linear`, team MER, project `ai-hottell`
   (P-MER-14, id `de70f79d-b9cf-46f6-a9b9-a11308c60db6`), same phase labels as
   harness-telemetry; `integration: pull-request`, base `camp`, protected `camp`, merge method
   `squash`; gate `codex-gates:codex-review`, review `gate-only`; `small_change` wording copied
   from harness-telemetry. Also `AGENTS.md` gains one line pointing at the profile (the camp
   AGENTS.md keeps its own content).
2. **Rename in code.** Directory `hookship/` → `hottell/`; Go module `hookship` → `hottell`;
   binary and install target name; config dir `~/.config/hookship` → `~/.config/hottell`;
   state/spool dir `~/.local/state/hookship` → `~/.local/state/hottell`; temp-file suffixes
   `.hookship-tmp`; the ownership match string in `groupIsOurs` (`"hookship"` → `"hottell"`);
   hook commands written by install; usage texts; README and `examples/` (including hook
   command strings). The marker file and env killswitch named in later cards (T4) do not exist
   in code yet — nothing to rename there beyond card texts, which already say `hottell`.
3. **Migration of alva-mac** (MER-4462): extract the token from the old install
   (`~/.config/hookship/token`) before removal, run old `hookship uninstall`, run new
   `hottell install -token-file …`, verify. Touches machine configs outside the repository —
   executes only on the owner's word, planned as the stream's close-time step.

Out of scope: MCP server, scope marker, release pipeline (cards T1–T5); `otel-lab/`; the camp
project's root README/docs.

## Done when

1. **No stale name in the tree:** `git grep -i hookship` on the branch returns nothing outside
   `docs/specs/` (this spec and history may name it). Evidence: the grep output, read by a
   fresh reader at the gate.
2. **Toolchain green under the new name:** `go vet ./...` and `go test ./...` in `hottell/`
   pass; sandbox install/uninstall cycle (fake HOME) passes with new paths. Evidence: command
   outputs in the implementation report.
3. **PR into `camp`** opened from `mer-4456-rename-hottell`, branch-gate verdict triaged.
   Evidence: PR link, gate verdict record.
4. **alva-mac migrated** *(close-time, on the owner's word)*: `hottell status` green, agent
   configs reference `hottell` only, manual delivery check passes, no `hookship` leftovers in
   `~/.claude/settings.json`, `~/.codex/hooks.json`, `~/.config`, `~/.local`. Evidence: status
   and grep outputs at close.

## Notes

- `hookship uninstall` of the old binary removes old hooks entries, dirs and binary — the
  clean migration path; no manual config surgery expected.
- Version constant stays as-is (`0.2.0`); versioning is T5's scope (will reset to tag-driven
  0.0.x).
