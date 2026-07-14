# Repo Maintenance

This document records cleanup rules for docs, scripts, and tests so the repo
does not grow a new layer of stale operational notes after each milestone.

## Documentation Rules

- Keep root-level docs limited to entrypoints and canonical deployment/design
  documents.
- Put focused design documents under `doc/`.
- Put operational procedures under `ops/runbooks/`.
- Keep [doc/roadmap.md](roadmap.md) as the single active plan.
- Move superseded plans, milestone journals, and status snapshots into
  `doc/archive/`, add them to the archive index, and point them back to the
  active plan.
- Merge durable architectural principles into [doc/architecture.md](architecture.md).
- Do not create parallel active status trackers; update the roadmap or a durable
  design/operations document instead.

## Script Rules

Scripts stay when at least one of these is true:

- A deployment or runbook doc names the script as an operator entrypoint.
- A test pins the script contract.
- The script is a promotion gate or cleanup companion for a promoted gate.
- The script is used by CI or local smoke testing.

Scripts should be removed when they are one-off probes, obsolete wrappers around
newer gates, or no longer documented anywhere. If a script remains, keep its
usage block, `bash -n` compatibility, and `scripts/scripts_test.go` coverage in
sync.

Current script audit: the tracked shell scripts are still referenced by docs,
runbooks, or `scripts/scripts_test.go`; no script had enough evidence for safe
removal in the June 2026 cleanup pass.

## Test Rules

Do not delete tests because their filename lacks a same-named production file.
This repo intentionally has package-level tests, black-box HTTP tests, script
contract tests, dashboard/runbook tests, and integration-style fixtures.

Test cleanup should prefer:

- merging duplicate setup helpers;
- deleting only assertions whose behavior is covered by a stronger package or
  end-to-end test;
- splitting huge files only when it improves ownership and runtime clarity;
- keeping regression tests for promoted internet-scale gates.

Current test audit: the first pass found many intentionally package-level tests
and no test file that was provably unused. Large files such as
`internal/core/command_log_native_decider_test.go`,
`internal/core/core_test.go`, and the HTTP API scenario tests are future
dedupe candidates, not deletion candidates.
