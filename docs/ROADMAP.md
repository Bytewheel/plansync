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

## M2 — Sync

- [ ] `sync` subcommand with `--policy markers|heuristic|report`
      (default `markers`): rewrite marker-owned regions (git-status blocks,
      heads tables), re-anchor moved anchors, apply report-style patch output
      for everything else.
- [ ] Marker spec implementation (`<!-- plansync:begin … -->` regions).
- [ ] Heuristic mode: refresh unmarked-but-unambiguous blocks (exact
      porcelain shape, same entry set) with backup + diff before write.

## M3 — Brief

- [ ] `brief` subcommand: session-start digest = remaining-work sections
      extracted from the plan + audit results + open checkboxes, capped at N
      lines. The token-saving headline feature.

## M4 — Log

- [ ] `log` subcommand: append a house-style dated doc-log entry
      (diagnosis → fixes → test counts → deploy transcript) assembled from
      current git state and a per-repo template.

## M5 — Init

- [ ] `init` subcommand: scaffold a continuation plan with live git state,
      repo headers, and marker regions pre-filled.

## M6 — Integration

- [ ] opencode plugin (`plansync_*` tools) mirroring the Determinism plugin
      pattern, so agents can audit without shell access to the binary path.
- [ ] Gate configuration per repo (`.plansync.toml`): commands to re-run for
      count verification (M1 reports them as unverified).
- [ ] Optional color output; `--fail-on` granularity.
