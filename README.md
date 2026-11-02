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

Flags:
  --repo path       candidate repository (repeatable)
  --json            machine-readable output
  --verbose         audit: also print ok claims individually
  --fail            audit: exit non-zero on stale/moved/missing findings
  --policy p        sync: markers (default) | heuristic | report
  --apply           sync: write changes (default: preview diff only)
  --no-backup       sync: skip the .bak backup when applying
  --max-lines n     brief: cap the digest at n lines (default 40)
```

## What gets checked

| Claim | Parsed from | Checked against |
|---|---|---|
| git working-tree state | fenced ` M path` / `?? path` blocks (with `(+N/-M ...)` annotations) | `git status --porcelain` per entry; gone entries are annotated with the commit that landed them |
| commit hashes | backticked 7–40 hex; "Head" table columns | `git cat-file`, `git rev-parse HEAD` |
| branches | backticked names on lines mentioning "branch" | `git rev-parse refs/heads/…` |
| `file.rs:NNN` anchors | backticked (and elided `:NNN` continuation) refs; ranges `:A-B` | line bounds, then symbol context from the same plan line; moved anchors get a re-anchor suggestion |
| file paths | backticked paths (incl. `~/`, `/abs`, `dir/file.rs::symbol`) | direct join, then suffix match across an indexed repo (plans routinely strip leading dirs like `crates/frontend/public/templates/`) |
| checkboxes | `- [ ]` / `- [x]` | counted, open ones listed by line |
| test/gate counts | `N tests`, `OK=n FAIL=n`, `n/m` | reported as unverified (M2: gate config) |

Statuses: `ok` (claim holds), `stale` (contradicted), `moved` (anchor content
relocated, suggestion given), `missing` (claimed file gone), `unknown`
(unresolvable — other-host paths, bare filenames, remotes), `info`.

Freshness = ok / (ok + stale + moved + missing).

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
internal/report    audit rendering (human + JSON)
docs/ROADMAP.md    milestones (M4 log, M5 init, M6 plugin)
AGENTS.md          instructions for AI agents working in this repo — and the
                    snippet to paste into any repo whose plans should be audited
```

See `docs/FORMAT-SPEC.md` for the exact grammar, and `AGENTS.md` for the
session-start / session-end workflow that saves the tokens.
