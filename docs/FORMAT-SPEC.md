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

`internal/claim/claim.go::Parse` module paths yield a path claim for the
file part.

### 4.2 Suffix index

Plans routinely strip leading directories (`brief/brief.go` for
`internal/brief/brief.go`). When direct joins fail, each
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

## 10. Marker regions (sync)

`plansync sync` rewrites the lines strictly between these HTML comments:

```
<!-- plansync:begin KIND ARGS -->
…tool-owned body…
<!-- plansync:end -->
```

- `begin` line grammar: `^\s*<!--\s*plansync:begin\s+([\w-]+)((?:\s+[\w-]+=[^\s>]+)*)\s*-->`
- Kinds: `git-status` (required arg `repo=PATH`) and `heads`.
- `git-status` body: a fenced (` ``` `) dump of the repo's current
  `git status --porcelain=v1`, or `# clean (HEAD <short-hash>)` when clean.
  Do not wrap the marker in an outer fence — the body brings its own.
- `heads` body: `| repo | head |` table, one row per candidate repo, paths
  and hashes backticked (so the next audit re-verifies them and the table
  doubles as a repo hint).
- Unclosed markers, unknown kinds, and non-worktree `repo=` paths produce
  warnings and leave the region untouched.
- The region body is ordinary claim text: `audit` parses it like any other
  part of the document.

## 11. Sync edit selection

Policies and precedence (lower number wins on overlap):

| prio | edit | policies |
|---|---|---|
| 0 | marker region refresh | all |
| 1 | most-recent unmarked git-status block per repo | heuristic, report |
| 2 | stale head-table commit hash → current HEAD | heuristic, report |
| 3 | moved anchor re-point (`path:old` → `path:new`, or elided `:old` → `:new`) | all |

Only the *most recent* git-status block per repo is refreshed under the
heuristic policy: older blocks in doc-log style plans are history. Multiple
rewrites on one document line are composed; single-line rewrites inside a
range edit are dropped (the range owns the line). An empty marker region
receives an insertion edit (Start = End+1).

## 12. Known limitations

- Numeric gate claims are unverified without gate config.
- Anchor symbol extraction only reads the anchor's own line; symbols named on
  neighbouring lines are missed (deliberate: keeps false-`ok` risk low).
- `?? dir/` untracked-dir shorthand in plans matches only if written
  file-by-file or as the exact dir path git prints.
- Markdown links `[text](path)` are not parsed; only backticked spans are.
- Heuristic git-tree refresh rewrites the whole block, dropping
  parenthesised annotations (`(+59/-13 net: …)`); use markers to keep
  annotated history safe.
- Sync cannot fix: anchors whose symbols vanished, missing paths, numeric
  gate claims, checkboxes. It reports them in the post-apply re-audit.
- Documentation example noise: grammar samples in this spec and README
  (deliberately generic paths like dir/file.rs) and the `-` before-lines
  of pasted diff/audit transcripts reference generic or since-superseded
  state; audit reports them missing/moved. Treat them as illustrative, not
  claims — or keep samples unresolvable (fictional repos/paths) so they
  surface as `unknown` (info) instead.

## 13. Brief digest (M3)

`plansync brief` renders a session-start digest per plan: header, then the
audit's `stale`/`moved`/`missing` findings (same `status Lline kind message`
shape as audit, messages truncated to 120 chars), then remaining-work
sections, then open checkboxes.

### 13.1 Remaining-work sections

A section is an ATX heading (`^ {0,3}#{1,6}\s+text`, not inside a fenced
block) whose text matches, case-insensitively and word-bounded:

```
remaining | outstanding | unfinished | todos? | to-do
open (items?|questions?|work) | next (steps?|tasks?|agent)
resume order | left to do | not yet done | pending
```

The section body extends to the next heading of the same or higher level
(deeper headings are content). A heading whose text starts with a date
(`YYYY-MM-DD`, the shape `plansync log` writes) terminates the section and
is never a section candidate itself — dated headings are doc-log history.
Fenced blocks inside the body are kept as content; fenced pseudo-headings
do not count as headings. A matching heading nested inside an
already-extracted section is skipped (the outer excerpt already covers it).
Trailing blank lines are trimmed; the section records its heading line and
last body line for checkbox de-duplication.

### 13.2 Checkboxes

Open `- [ ]` items are counted; those inside an extracted section are not
repeated (the excerpt shows them). Those outside are listed with their
`- [ ]` prefix stripped, one digest line each.

### 13.3 Line budget

`--max-lines N` (default 40) is a total budget across plans; each plan gets
`remaining budget / plans left` (floor 3). Within a plan the order is
header → findings → unknown count → section excerpts → loose checkboxes;
over-budget lines are cut from the end and counted in one elision line
(`+K line(s) elided; raise --max-lines`). Each section excerpt is
additionally capped at 20 body lines (blank runs collapsed) with a
`(+K more line(s) in plan)` note. The footer reports the true line count,
which may exceed the cap by the elision lines, blank separators, and footer.

### 13.4 JSON

`--json` emits the unexcerpted digests: `[{file, repos, freshness, age_note,
findings, unknowns, sections: [{heading, line, end, body}], open_checkboxes,
open_boxes, done_boxes}]` — bodies are complete; apply your own caps.

### 13.5 Known limitations (brief)

- Only ATX headings (`## text`); setext (`text\n====`) headings are not
  recognised as section boundaries.
- The remaining-work vocabulary is intentionally conservative; roadmap-style
  plans whose work lives in unchecked boxes under non-matching headings fall
  back to the loose-checkbox listing.
- With many plans under a small cap, later plans may render header + elision
  only; raise `--max-lines` or brief the plans individually.

## 14. Log entries (M4)

`plansync log PLAN` appends one entry at the end of the plan, separated by a
blank line, with exactly one trailing newline. Preview by default; `--apply`
writes after saving `<plan>.bak` (unless `--no-backup`) and re-audits.

### 14.1 Default entry shape

```
### YYYY-MM-DD[ — subject]

**Repo:** `path`, branch `name`, HEAD `short-hash`; working tree:

```
 M src/a.rs
?? src/b.rs
```

**Diagnosis:** …
**Fixes:** …
**Test counts:** …
**Deploy transcript:** …
```

One Repo/git block per candidate repo (clean trees print
`… HEAD `hash`; working tree clean.` with no fence). Empty text fields are
omitted; the git state is always included. Every line is audit grammar: the
`**Repo:**` header is a repo hint (§8), the backticked branch name and HEAD
hash are branch/commit claims, and the fenced block is a git-tree claim — a
freshly written entry audits clean by construction.

### 14.2 Templates

A custom template replaces the built-in shape. Discovery order: `--template
path` (read errors are fatal), then `.plansync/log-template.md` in each
candidate repo root, then next to the plan; none found = built-in default.
Substitutions: `{{date}}` (defaults to today, `--date` to override),
`{{subject}}`, `{{diagnosis}}`, `{{fixes}}`, `{{tests}}`, `{{deploy}}`, and
`{{git}}` — the multi-line per-repo git state of §14.1, which should sit on
its own line since it brings its own fences. Unknown placeholders pass
through untouched; missing fields substitute empty strings.

### 14.3 Interaction with brief

Dated headings (`### YYYY-MM-DD …`) terminate remaining-work excerpts
(§13.1) and are never extracted as sections, so appended log history stays
out of the session-start digest.

## 15. Scaffolded plans (M5)

`plansync init PLAN.md` writes a continuation plan with this shape:

```
# Continuation plan — {title} ({date})

**Repo:** `path`, branch `name`, HEAD `short-hash}

One-paragraph brief placeholder.

---

## Status

Working tree of `path`:

<!-- plansync:begin git-status repo=path -->
(§10 body)
<!-- plansync:end -->

Heads at plan time:

<!-- plansync:begin heads -->
(§10 body)
<!-- plansync:end -->

---

## Remaining steps, in order

- [ ] describe the first concrete step
- [ ] …

## Gotchas / landmines

- Invariants, failure modes already diagnosed, and things NOT to redo.
```

Rules:

- One `**Repo:**` header per candidate repo; one git-status marker per
  repo; one heads marker. Marker bodies are generated by the same code
  `sync` uses (`syncer.MarkerBody`), so a fresh scaffold is a sync no-op.
- The gotchas heading deliberately avoids the remaining-work vocabulary
  (§13.1) so `brief` excerpts only the steps section.
- No candidate repos: a `TODO` repo header plus a note replaces the live
  state; no markers are emitted.
- `--title` defaults to the file base name (extension stripped, `-`/`_`
  become spaces); `--date` defaults to today. Parent directories are
  created; an existing file is never overwritten.
- `--apply` writes the previewed content and then runs audit + sync
  computation on it, printing both counts (a fresh scaffold should be 0/0).
