# plansync — Format Specification

This document specifies the claim grammar plansync parses from plan markdown
and the verification semantics applied to each claim kind. The parser is
line-oriented (like Determinism's chat-text parser); it makes a single pass
with fence tracking and per-line span extraction.

## 1. Claim kinds

| Kind | Produces | Parsed from |
|---|---|---|
| `git-tree` | one claim per block of consecutive entries | porcelain-status lines (§2) |
| `anchor` | one claim per reference | backticked `path:line` refs (§3) |
| `path` | one claim per reference | backticked path-like spans (§4) |
| `commit` | one claim per hash | backticked 7–40 hex (§5) |
| `branch` | one claim per name | backticked name on a line mentioning "branch" (§6) |
| `checkbox` | one claim per box | `- [ ]` / `- [x]` list items |
| `count` | one claim per match | numeric gate claims (§7) |

## 2. git-tree blocks

A line is a git entry when it matches:

```
^ {0,3}XY SP PATH REST
XY  ∈ { [MADRCU][MDA? ], ??, [ ][MDA?] }   (porcelain v1 two-char codes)
```

Rules:

- Inside fenced code blocks (``` or ~~~): PATH need only contain `/` or `.`.
- Outside fences: PATH must contain `/` (prose guard — "A man walked…" must
  not parse).
- PATH may be double-quoted (paths with spaces).
- `R  old -> new` rename syntax: the new path is the tracked path.
- REST starting with `(`: the first parenthesised group is kept as the entry
  note (`(+59/-13 net: …)`).
- Consecutive entry lines accumulate into one `git-tree` claim; any other
  non-blank line ends the block.

Verification: the repo is chosen by majority vote of entries that resolve;
`git status --porcelain=v1 -z` provides the actual set. Entries absent from
the working tree are reported with `git log -1 -- path` (the commit that
landed them), grouped by commit. Entries present with a different XY are
reported as status changes. Actual-but-unclaimed entries are listed.

## 3. anchors

Backticked spans matching `PATH:LINE` or `PATH:A-B` (PATH must be pathish,
§4). An elided form `:LINE` (or `:A-B`) refers to the most recent anchor path
on the same or preceding two lines ("tested at\n`:1506`").

Symbol context: other backticked identifier spans on the same line are kept as
candidate symbols — identifiers are spans of `[A-Za-z_][A-Za-z0-9_:]{3,}`
containing `_`, camelCase, or `::`. `A::B` also contributes parts `A`, `B`
(keyword list filtered). Prose without those shapes contributes nothing.

Verification order:

1. file resolves (repo join, plan-dir join, then suffix index, §4.2);
2. any symbol matched at `line ± 1` (or range `A-1..B+1`) → `ok`;
3. else every symbol searched file-wide; rarest symbol's first hit → `moved`
   with suggestion `path:firstHit`;
4. symbols absent from the file → `stale`;
5. no symbols → bounds check only.

## 4. paths

A backticked span is pathish when it has no whitespace, no `:`, none of
`{}*<>[]?…`, is not URL/proto/IPv4/CIDR/version-shaped, and is either:

- `~/…` — checked literally against the filesystem;
- `/abs/…` — must start with a known filesystem root (`/home/`, `/tmp/`,
  `/usr/`, `/etc/`, …); other leading-slash spans are treated as URL routes
  and ignored;
- relative — must contain a `.` (extension); dotless `a/b` pairs are remote
  refs / package categories, not files.

`path/file.rs::symbol` module paths yield a path claim for the file part.

### 4.2 Suffix index

Plans routinely strip leading directories (`pages/x.html` for
`crates/frontend/public/templates/pages/x.html`). When direct joins fail, each
candidate repo's file tree (`.git`, `target`, `node_modules`, dot-dirs
skipped) is indexed once and the shortest relative path ending in `/claim` is
used; results are reported as "exists at … (suffix match)".

## 5. commits

Backticked `[0-9a-f]{7,40}`. Verified per repo with `git cat-file -t`; if the
hash is (a prefix of) HEAD → `ok`. If the claim line (or up to 3 lines above,
for handoff tables whose "Head" is a header row) mentions "head" → `stale`
when it is not HEAD. Otherwise an informational "exists, HEAD is now X".

## 6. branches

Backticked span on a line containing "branch" (case-insensitive); the span
must be dot/slash/colon-free and non-hex. Verified against local
`refs/heads/` in each candidate repo.

## 7. counts

`\bOK=(\d+)\s+FAIL=(\d+)\b`, `\b(\d+)\s+tests?\b`, `\b(\d+)\s+failures?\b`,
`\b(\d+)\s+errors?\b`, `\b(\d+)/(\d+)\b`. Reported as informational; M2 adds
gate configuration to re-run and compare.

## 8. Repository hints

`RepoHints` scans the whole document for:

- `**Repo:** `path`` / `**Repository:**` / `**Working tree:** `path`` —
  bold-labelled headers (asterisks tolerated on either side of the colon);
- handoff table rows whose first cell is Server/Repo/Path/Plan/Packaging/
  Project — the first backticked `/abs` or `~/` span in the row.

Hints are expanded (`~` → home), then walked up to their `.git` root.
Candidate order: `--repo` flags, document hints, plan's own worktree.

## 9. Output contract

Human: one `status Lline kind message` line per non-ok claim, aggregates for
oks/unknowns/checkboxes/counts, an age line (first `\d{4}-\d{2}-\d{2}` in the
document head + `git rev-list --count HEAD --since=…` per repo), and
freshness = ok / (ok + stale + moved + missing).

JSON (`--json`): array of `{file, repos, results: [{kind, status, line,
path?, repo?, message, suggestion?}], age_note, freshness}`.

## 10. Known limitations

- No rewrite capability yet (M2 `sync`); audit is strictly read-only.
- Numeric gate claims are unverified without gate config.
- Anchor symbol extraction only reads the anchor's own line; symbols named on
  neighbouring lines are missed (deliberate: keeps false-`ok` risk low).
- `?? dir/` untracked-dir shorthand in plans matches only if written
  file-by-file or as the exact dir path git prints.
- Markdown links `[text](path)` are not parsed; only backticked spans are.
