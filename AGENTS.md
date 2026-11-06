# AGENTS.md — plansync: keeping plan documents truthful

plansync audits plan documentation (`PLAN.md`, `PLAN_NEXT_AGENT_*.md`,
`CONTINUATION_PLAN.md`, roadmaps, handoff notes) against the actual state of
the git repositories those plans describe, and syncs tool-owned regions back
to current state. Use the audit instead of re-deriving by hand which parts of
a plan still hold. `audit` is read-only; `sync` previews a diff and writes
only with `--apply` (plus a `.bak` backup).

Binary: `~/Documents/ByteWheel/plansync/bin/plansync`
(rebuild: `cd ~/Documents/ByteWheel/plansync && env -u GOROOT go build -o bin/plansync ./cmd/plansync`)

## Session start — before reading a plan

Run the audit first and read its report (typically 10–40 lines) instead of the
full plan (often 200–6,000 lines):

```sh
~/Documents/ByteWheel/plansync/bin/plansync audit <plan.md> --repo <repo> [--repo <repo2>]
```

For the shortest session-start signal, `brief` packages the audit findings,
the plan's remaining-work sections, and its open checkboxes in one
budget-capped digest:

```sh
~/Documents/ByteWheel/plansync/bin/plansync brief <plan.md> --repo <repo> [--repo <repo2>] [--max-lines 40]
```

Use `brief` when you only need "what is stale + what is left"; use `audit`
when you need the full claim-by-claim report.

The report tells you, before you read a word of prose:

- whether the "uncommitted work" the plan describes is still uncommitted — or
  already landed (and in exactly which commit, per file);
- which `file.rs:NNN` anchors moved, with the corrected line numbers;
- which claimed files no longer exist;
- whether claimed heads/branches moved on;
- how many commits each repo advanced since the plan was written;
- how many checkboxes remain open (with line numbers).

Then read only the plan sections the report implicates, plus the plan's own
"remaining work" section. Do not re-verify by hand what the report already
states — a `stale`/`moved`/`missing` line is established fact, not a hint.

For the omnical corpus the repos are not auto-discoverable from the plan dir
alone; pass them explicitly:

```sh
~/Documents/ByteWheel/plansync/bin/plansync audit ~/Documents/ByteWheel/omnical \
  --repo ~/router-dav/rustical --repo ~/router-dav
```

For a whole tree at once:

```sh
~/Documents/ByteWheel/plansync/bin/plansync audit ~/Documents/ByteWheel \
  --repo ~/router-dav/rustical --repo ~/router-dav
```

`--json` gives the same data machine-readably; `--fail` exits non-zero when
anything stale/moved/missing is found (CI-able); `--verbose` prints the `ok`
claims individually. `--gates` additionally runs the repo's `.plansync.toml`
gate commands and verifies the plan's numeric claims against their output
(see docs/FORMAT-SPEC.md §16). `--color always|never` overrides the
terminal auto-detection; `--fail-on` narrows which statuses trip `--fail`.

## Session end — before handing off

1. Run the audit again on the plan(s) you touched.
2. Fix every `stale`/`moved`/`missing` finding, either by hand or with
   `plansync sync` (below).
3. Record the session with `plansync log` (below) — it appends a dated
   doc-log entry with live git state, so the next agent's audit starts from
   recorded facts instead of re-derived guesses.
4. Re-run the audit until the plan is clean, then write the handoff note.
   A clean audit attached to the handoff is worth more than prose promising
   the plan is current.

### plansync log

```sh
~/Documents/ByteWheel/plansync/bin/plansync log <plan.md> --repo <repo> \
  [--subject s] [--diagnosis s] [--fixes s] [--tests s] [--deploy s] \
  [--date YYYY-MM-DD] [--template path] [--apply]
```

`log` appends a house-style dated entry (diagnosis → fixes → test counts →
deploy transcript) with the current git state of every candidate repo; empty
fields are omitted. It previews by default; `--apply` writes (`.bak` backup
unless `--no-backup`) and re-audits. The entry is written in audit grammar
(`**Repo:**` header, branch/HEAD mentions, fenced porcelain), so it keeps
verifying on every future audit. Template: `.plansync/log-template.md` from
a candidate repo or next to the plan, `--template` to override, built-in
default otherwise; placeholders `{{date}} {{subject}} {{diagnosis}} {{fixes}}
{{tests}} {{deploy}} {{git}}`.

### plansync init

```sh
~/Documents/ByteWheel/plansync/bin/plansync init <plan.md> --repo <repo> \
  [--title s] [--date YYYY-MM-DD] [--apply]
```

`init` scaffolds a new continuation plan pre-filled with live git state:
`**Repo:**` headers, `git-status` + `heads` marker regions (tool-owned —
`sync` keeps them current), a "Remaining steps, in order" section, and a
gotchas section. It previews by default, `--apply` writes (parent dirs
created; existing files are never overwritten). A fresh scaffold audits
clean and is a sync no-op by construction.

### plansync sync

```sh
~/Documents/ByteWheel/plansync/bin/plansync sync <plan.md> --repo <repo> [--policy p] [--apply]
```

`sync` previews a diff by default and writes only with `--apply` (which makes
a `<plan>.bak` backup unless `--no-backup`). After applying it re-audits and
reports how many findings remain that it cannot fix automatically.

Policies:

| Policy | Rewrites |
|---|---|
| `markers` (default) | plansync marker regions + symbol-verified re-anchoring of moved `path:line` refs |
| `heuristic` | markers, plus the most recent *unmarked* git-status block per repo (older blocks are doc-log history) and stale head-table commits |
| `report` | computes the heuristic edit set but never writes |

Re-anchoring only fires when the plan line names the symbol (`put_event` next
to `methods.rs:59`) and that symbol is still in the file — it re-points, it
never guesses. Anchors whose symbols are gone, missing paths, and numeric
gate claims are always left for the human; the post-apply re-audit lists
them.

## Reading the report

| Status | Meaning | Action |
|---|---|---|
| `ok` | claim verified against the repo | trust it; skip that plan section |
| `stale` | contradicted (e.g. tree clean, head moved, symbols gone) | re-derive the fact; update the plan |
| `moved` | anchor's symbol found elsewhere in the file | use the suggested `path:line` |
| `missing` | claimed file not found (direct or suffix match) | find the replacement; update the plan |
| `unknown` | unresolvable: other-host paths (`/usr/...` on the router), bare filenames, remote names | ignore unless you know better |
| `info` | checkboxes / numeric gate claims (not verified — re-run gates to confirm numbers) | run the gates if the numbers matter |

## Writing syncable plans

The audit only sees claims it can parse. When writing or updating plans, keep
claims in the shapes the grammar reads (full spec in `docs/FORMAT-SPEC.md`):

- Put git-state in fenced blocks exactly as `git status --porcelain` prints
  them, optionally with a parenthesised annotation:

  ```text
   M crates/caldav/src/calendar_object/methods.rs   (+59/-13 net: import closure)
  ?? crates/ical/src/normalize.rs                   (NEW, 539 lines)
  ```

- Declare the repo up front: `**Repo:** `path/to/repo`, branch `branch-name``
  (also `**Working tree:**`, `**Code repo:**`, a `**Repos:**` bullet list, or
  a `| Server | /abs/path | … |` handoff table).
- Reference code as backticked `path/file.rs:LINE` (or `:A-B`), and mention
  the symbol name on the same line — that is what enables verified re-anchoring:

  ```markdown
  | import wiring | `crates/ical/src/calendar_object.rs:79` |
  | stamping | `put_event` + `stamp_default_organizer` in `methods.rs:59` |
  ```

  The second row verifies and re-anchors; the first can only bounds-check.
- Name test files with full paths (`internal/claim/claim_test.go`), not
  bare names.
- Keep `- [ ]` / `- [x]` checkboxes on their own lines for counting.

### Marker blocks

Wrap regions the tool should own in marker comments; `plansync sync`
rewrites everything between the markers in place (do not hand-edit inside
them), and `plansync audit` verifies the refreshed claims like any other:

````html
<!-- plansync:begin git-status repo=~/router-dav/rustical -->
```
 M src/foo.rs
```
<!-- plansync:end -->

<!-- plansync:begin heads -->
| repo | head |
|---|---|
| `~/router-dav/rustical` | `e30c90d9` |
<!-- plansync:end -->
````

Marker kinds: `git-status repo=PATH` (body: a fenced `git status --porcelain`
dump, or `# clean (HEAD <hash>)`) and `heads` (body: a backticked repo/head
table over the candidate repos). Do not wrap markers in an outer fence — the
generated body brings its own where needed.

## Embedding in other repos

To make agents working inside a plan-heavy repo run this automatically, add to
that repo's `AGENTS.md` (or .opencode/instructions):

```markdown
## Plan hygiene
Before reading any PLAN*.md / CONTINUATION_PLAN.md / NEXT_AGENT_*.md, run:
  ~/Documents/ByteWheel/plansync/bin/plansync audit <plan> --repo <repo...>
and read the report instead of the whole plan. Trust stale/moved/missing lines
as established fact. Before handing off, fix all findings and re-audit.
Repos: ~/router-dav/rustical, ~/router-dav (adjust per repo).
```

## Development

```sh
cd ~/Documents/ByteWheel/plansync
env -u GOROOT go test ./...     # parser + verifier unit tests
env -u GOROOT go vet ./...
```

The repo's own gates live in `.plansync.toml` (go test, go vet); run
`./bin/plansync audit docs/ROADMAP.md --gates` to exercise them. The
opencode plugin (`opencode/plansync.ts`) type-checks with
`cd opencode && npx tsc -p tsconfig.json`.

House style follows Determinism: stdlib-only Go, no external deps, flags
interspersed with positionals, package docs in `docs/`.
