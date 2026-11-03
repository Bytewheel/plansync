# plansync — Roadmap

## M1 — Audit (DONE)

- [x] Claim parser: git-tree blocks, `path:line` anchors (incl. elided and
      ranges), paths, commits, branches, checkboxes, numeric counts.
- [x] Repository discovery: `--repo` flags, `**Repo:**`/`**Working tree:**`
      headers, handoff tables, plan worktree.
- [x] Verification: porcelain comparison with landing-commit attribution,
      head/branch checks, symbol-verified re-anchoring, suffix-index path
      resolution, checkbox/count reporting.
- [x] Reports: human (compact, grouped, freshness) and `--json`; `--fail`.
- [x] Unit tests for parser and verifier; corpus tuning against omnical,
      crabi, Determinism, uget, sentiments, opennic plans.

## M2 — Sync (DONE)

- [x] `sync` subcommand with `--policy markers|heuristic|report`
      (default `markers`): rewrite marker-owned regions (git-status blocks,
      heads tables), re-anchor moved anchors, preview diff by default,
      `--apply` writes with `.bak` backup and a post-apply re-audit.
- [x] Marker spec implementation (`<!-- plansync:begin … -->` regions).
- [x] Heuristic mode: refresh the most recent unmarked git-status block per
      repo and stale head-table commits, with backup + diff.

## M3 — Brief (DONE)

- [x] `brief` subcommand: session-start digest = remaining-work sections
      extracted from the plan + audit results + open checkboxes, capped at N
      lines (`--max-lines`, default 40). The token-saving headline feature.

## M4 — Log (DONE)

- [x] `log` subcommand: append a house-style dated doc-log entry
      (diagnosis → fixes → test counts → deploy transcript) assembled from
      current git state and a per-repo template
      (`.plansync/log-template.md`; built-in default otherwise). Preview by
      default; `--apply` writes with `.bak` backup and a post-apply
      re-audit; generated entries are audit-parseable and audit clean.

## M5 — Init (DONE)

- [x] `init` subcommand: scaffold a continuation plan with live git state,
      repo headers, and marker regions pre-filled (git-status + heads).
      Fresh scaffolds audit clean and are sync no-ops by construction;
      refuses to overwrite existing files.

## M6 — Integration (DONE)

- [x] opencode plugin (`plansync_*` tools: audit, brief, sync, log, init)
      mirroring the Determinism plugin pattern, so agents can audit without
      shell access to the binary path.
- [x] Gate configuration per repo (`.plansync.toml`): commands to re-run for
      count verification (`audit --gates`), with shape- and number-level
      matching of claimed counts against gate output.
- [x] Optional color output (`--color auto|always|never`); `--fail-on`
      granularity.
