# plansync

Audit plan documentation against the state of the code repositories it
describes. Plans written for "the next agent" (`PLAN.md`, `PLAN_NEXT_AGENT_*.md`,
`CONTINUATION_PLAN.md`, handoff notes, roadmaps) embed claims about reality —
working-tree state, commit heads, `file.rs:123` anchors, file existence, test
counts. As code moves, those claims go stale, and the next agent burns tokens
re-deriving what is still true. plansync checks every claim mechanically and
emits a compact drift report instead.

```console
$ plansync audit ~/Documents/ByteWheel/omnical --repo ~/router-dav/rustical
== .../PLAN_NEXT_AGENT_403_FIX.md
   stale   L22   git-tree  working tree is CLEAN (HEAD e30c90d9) but plan claims 6 dirty
                          entries; crates/caldav/.../methods.rs landed in e30c90d9 postgres:
                          host the data store on PostgreSQL as well as SQLite; ...
   moved   L44   anchor    crates/ical/src/calendar_object.rs not at :79; nearest occurrence
                          of CalendarObject at :71  -> crates/ical/src/calendar_object.rs:71
   ok      1 branch, 15 path, 2 anchor
   info    plan dated 2026-09-18; ~/router-dav/rustical advanced 99 commits
   freshness: 86%
```

## Install

```sh
cd ~/Documents/ByteWheel/plansync
env -u GOROOT go build -o bin/plansync ./cmd/plansync
```

(If `GOROOT` points at the stripped `/programs/dev-lang/go/current` tree, unset
it; the working toolchain is `/usr/lib/go`.)

## Usage

```
plansync audit [plans...] [flags]     report claims that have gone stale
plansync sync  [plans...] [flags]     refresh tool-owned plan regions
plansync brief [plans...] [flags]     session-start digest: findings +
                                       remaining work + open checkboxes
plansync log   [plan] [flags]         append a dated doc-log entry built
                                       from flags + current git state
plansync init  <plan.md> [flags]      scaffold a continuation plan with
                                       live git state + marker regions

Flags:
  --repo path       candidate repository (repeatable)
  --json            machine-readable output
  --verbose         audit: also print ok claims individually
  --fail            audit: exit non-zero on stale/moved/missing findings
  --fail-on list    audit: statuses that trigger --fail, comma-separated
                    (default stale,moved,missing)
  --gates           audit: run .plansync.toml gate commands and verify the
                    plan's numeric claims against their output
  --gate-config p   audit: explicit gate config path (default: .plansync.toml
                    from a candidate repo or the plan dir)
  --color when      audit: auto (default) | always | never
  --policy p        sync: markers (default) | heuristic | report
  --apply           sync/log/init: write changes (default: preview only)
  --no-backup       sync/log: skip the .bak backup when applying
  --max-lines n     brief: cap the digest at n lines (default 40)
  --date d          log/init: date, YYYY-MM-DD (default today)
  --subject s       log: entry subject for the heading
  --diagnosis s     log: diagnosis text
  --fixes s         log: fixes text
  --tests s         log: test-count text
  --deploy s        log: deploy transcript text
  --template path   log: template file (default: .plansync/log-template.md
                    from a candidate repo or the plan dir; built-in otherwise)
  --title s         init: plan title (default: derived from the file name)
```

## What gets checked

| Claim | Parsed from | Checked against |
|---|---|---|
| git working-tree state | fenced ` M path` / `?? path` blocks (with `(+N/-M ...)` annotations) | `git status --porcelain` per entry; gone entries are annotated with the commit that landed them |
| commit hashes | backticked 7–40 hex; "Head" table columns | `git cat-file`, `git rev-parse HEAD` |
| branches | backticked names on lines mentioning "branch" | `git rev-parse refs/heads/…` |
| `file.rs:NNN` anchors | backticked (and elided `:NNN` continuation) refs; ranges `:A-B` | line bounds, then symbol context from the same plan line; moved anchors get a re-anchor suggestion |
| file paths | backticked paths (incl. `~/`, `/abs`, dir/file.rs::symbol) | direct join, then suffix match across an indexed repo (plans routinely strip leading dirs like `crates/frontend/public/templates/`) |
| checkboxes | `- [ ]` / `- [x]` | counted, open ones listed by line |
| test/gate counts | `N tests`, `OK=n FAIL=n`, `n/m` | `audit --gates` runs `.plansync.toml` commands and matches claimed counts against their output (below) |

Statuses: `ok` (claim holds), `stale` (contradicted), `moved` (anchor content
relocated, suggestion given), `missing` (claimed file gone), `unknown`
(unresolvable — other-host paths, bare filenames, remotes), `info`.

Freshness = ok / (ok + stale + moved + missing).

## Gate configuration

Numeric claims (`16 tests`, `OK=61 FAIL=0`, …) are reported as unverified
unless `--gates` is given. Gates are shell commands declared in a
`.plansync.toml` at a candidate repo root (or next to the plan):

```toml
[[gate]]
name = "go test"
command = "go test ./..."

[[gate]]
name = "go vet"
command = "go vet ./..."
```

`audit --gates` runs them (in the config's directory, combined output
captured) and verifies each count claim: ok when the claimed numbers appear
in the output — verbatim ("16 tests" seen as "16 tests") or across shapes
("61 tests" is confirmed by "OK=61 FAIL=0") — stale with the actual numbers
when the output carries counts but not the claimed one, info when the output
has no count-shaped lines at all. A failing gate command is reported as a
`stale`-class finding (it contradicts "all gates green" claims) and trips
`--fail`.

Color: `--color always|never|auto` (default auto: colored when stdout is a
terminal). `--fail-on stale,moved,missing` (the default) narrows or widens
which statuses trip `--fail` — e.g. `--fail-on stale` for CI gate runs.

## Syncing plans back to current state

`sync` computes the edits a policy allows, previews a unified diff, and —
with `--apply` — writes them (after saving `<plan>.bak`), then re-audits and
reports the findings it could not fix:

```console
$ plansync sync PLAN_NEXT_AGENT_403_FIX.md --repo ~/router-dav/rustical --policy heuristic --apply
@@ L22-L27 @@ refresh most recent working-tree block to current state (heuristic)
-  M crates/caldav/src/calendar_object/methods.rs   (+59/-13 net: …)
+ # clean (HEAD e30c90d9)
@@ L44-L44 @@ re-anchor crates/ical/src/calendar_object.rs
- - Wired into `CalendarObject::import` (`crates/ical/src/calendar_object.rs:79`)
+ - Wired into `CalendarObject::import` (`crates/ical/src/calendar_object.rs:71`)
   applied 4 edit(s); backup at PLAN_NEXT_AGENT_403_FIX.md.bak
   re-audit: 0 finding(s) remain that sync does not fix automatically
```

| Policy | Rewrites |
|---|---|
| `markers` (default) | `plansync` marker regions (below) + symbol-verified re-anchoring of moved `path:line` refs |
| `heuristic` | markers, plus the most recent *unmarked* git-status block per repo (older blocks are doc-log history) and stale head-table commits |
| `report` | computes the heuristic edit set, never writes |

Marker regions are the tool's own and are rewritten wholesale:

```html
<!-- plansync:begin git-status repo=~/router-dav/rustical -->
<!-- plansync:begin heads -->
```

`git-status` bodies are fenced porcelain dumps (or `# clean (HEAD <hash>)`);
`heads` bodies are backticked repo/head tables. Re-anchoring never guesses:
it only re-points a `path:line` ref when the plan line names a symbol and
that symbol still exists in the file.

## Session-start brief

`brief` is the one-command digest for session start: audit findings first
(established facts), then the plan's remaining-work sections excerpted
verbatim, then any open checkboxes outside those sections — under a line
budget:

```console
$ plansync brief PLAN_NEXT_AGENT_403_FIX.md --repo ~/router-dav/rustical
== PLAN_NEXT_AGENT_403_FIX.md (freshness 86%; plan dated 2026-09-18; ~/router-dav/rustical advanced 99 commits)
   stale   L22   git-tree  working tree is CLEAN (HEAD e30c90d9) but plan claims 6 dirty entries; ...
   moved   L44   anchor    crates/ical/src/calendar_object.rs not at :79 ... -> crates/ical/src/calendar_object.rs:71
   work    L94   section  2. Remaining work, in order
      1. **Sanity re-run** (fast, confirms nothing drifted):
         ```bash
         cd ~/router-dav/rustical
         ...
   ...    +9 line(s) elided; raise --max-lines

briefed 1 plan(s) in 42 line(s) (cap 40)
```

Remaining-work sections are headings matching the remaining-work vocabulary
(remaining, next steps, next agent, open items/questions, todo, outstanding,
unfinished, resume order, pending, …); a section extends to the next heading
of the same or higher level. Open checkboxes already inside an excerpt are
not repeated. `--max-lines` (default 40) is a total budget split across
plans; findings outrank excerpts, which outrank loose checkboxes; everything
cut is counted in an elision line. Plans with no findings, sections, or open
checkboxes say so in one line — that is itself the answer.

## Appending doc-log entries

`log` records a session in the plan: a dated heading, live git state per
repo, and the four house sections (diagnosis → fixes → test counts → deploy
transcript). It previews by default and writes with `--apply` (`.bak`
backup, then a post-apply re-audit):

```console
$ plansync log PLAN.md --repo /opt/appliance \
    --subject "mirror 403 fix" \
    --diagnosis "floating UNTIL rejected by caldata" \
    --fixes "normalize_rrule_until on import" \
    --tests "appliance_ical 16, appliance_scheduling 61, 0 failures" \
    --deploy "deploy.sh run, services restarted" --apply
== PLAN.md
### 2026-11-02 — mirror 403 fix

**Repo:** `/opt/appliance`, branch `main`, HEAD `cafe1234`; working tree:

```
 M appliance/src/mirror.rs
```

**Diagnosis:** floating UNTIL rejected by caldata

**Fixes:** normalize_rrule_until on import

**Test counts:** appliance_ical 16, appliance_scheduling 61, 0 failures

**Deploy transcript:** deploy.sh run, services restarted
   appended 17 line(s) to PLAN.md; backup at PLAN.md.bak
   re-audit: 0 finding(s) (includes pre-existing plan history)
```

The generated entry is audit grammar on purpose — the `**Repo:**` header is
a repo hint, the branch/HEAD mentions and the fenced porcelain block are
claims — so the next `audit`/`brief` re-verifies what the entry says. A
custom template (`.plansync/log-template.md` in a candidate repo, next to
the plan, or `--template path`) substitutes `{{date}}`, `{{subject}}`,
`{{diagnosis}}`, `{{fixes}}`, `{{tests}}`, `{{deploy}}`, and `{{git}}`
(the multi-line per-repo git state; keep it on its own line). Dated
headings also terminate remaining-work excerpts in `brief`, keeping log
history out of the work digest.

## Scaffolding continuation plans

`init` starts a new plan from live state instead of a blank page — repo
headers, tool-owned marker regions, a remaining-work section, and a
gotchas section, all pre-filled:

```console
$ plansync init CONTINUATION_PLAN.md --repo /opt/appliance --title "mirror finish" --apply
== CONTINUATION_PLAN.md
# Continuation plan — mirror finish (2026-11-02)

**Repo:** `/opt/appliance`, branch `main`, HEAD `cafe1234`
...
## Status

Working tree of `/opt/appliance`:

<!-- plansync:begin git-status repo=/opt/appliance -->
```
 M appliance/src/mirror.rs
```
<!-- plansync:end -->
...
   created CONTINUATION_PLAN.md (41 line(s))
   audit: 0 finding(s); sync: 0 edit(s) — a fresh scaffold should be 0/0
```

The marker bodies come from the same generator `sync` uses, so a fresh
scaffold is a sync no-op; the headers and fenced state are audit grammar,
so it audits 100% clean; `brief` hooks the "Remaining steps, in order"
section from the start. `init` refuses to overwrite an existing file (no
`--force`), creates parent directories, and derives the title from the
file name when `--title` is omitted. With no discoverable repo it emits
TODO placeholders instead of live state.

## opencode plugin

`opencode/plansync.ts` exposes the CLI as custom tools (`plansync_audit`,
`plansync_brief`, `plansync_sync`, `plansync_log`, `plansync_init`) so
agents can audit plans without shell access to the binary path. The binary
is located via `PLANSYNC_BIN`, then `bin/plansync`, and auto-built from
source if missing. Register it with:

```json
{ "plugin": ["file:///home/burningserenity/Documents/ByteWheel/plansync/opencode/plansync.ts"] }
```

(Type-check: `cd opencode && npm install && npx tsc -p tsconfig.json`.)

## Repository discovery

In order: `--repo` flags, the plan's own header hints (`**Repo:**`, `**Working
tree:**`, `| Server | /path |` handoff-table rows), then the plan's git
worktree. For the omnical corpus that means:

```sh
plansync audit ~/Documents/ByteWheel/omnical --repo ~/router-dav/rustical --repo ~/router-dav
```

## Layout

```
cmd/plansync       CLI
internal/claim     plan → claims parser (see docs/FORMAT-SPEC.md)
internal/verify    git/file verification, suffix index, re-anchoring
internal/syncer    edit computation, marker regions, diff, apply
internal/brief     session-start digest (findings + remaining work + checkboxes)
internal/doclog    dated doc-log entry assembly (git state + template)
internal/scaffold  continuation-plan scaffolding (live markers + headers)
internal/gates     .plansync.toml gate runner + count verification
internal/report    audit rendering (human + JSON, color)
opencode/          opencode plugin (plansync_* tools)
docs/ROADMAP.md    milestones
AGENTS.md          instructions for AI agents working in this repo — and the
                    snippet to paste into any repo whose plans should be audited
```

See `docs/FORMAT-SPEC.md` for the exact grammar, and `AGENTS.md` for the
session-start / session-end workflow that saves the tokens.
