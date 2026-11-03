# plansync — organic test protocol

Unit tests and the six tuning corpora (omnical, crabi, Determinism, uget,
sentiments, opennic) prove the machinery. Organic testing proves it on plan
styles, repos, and drift it has never seen, with live agents as the users.
Targets: `~/Documents/ByteWheel/HOT`, `~/Documents/ByteWheel/rozelynx`,
`~/Documents/Carlton_Audio/repos/carlton-audio` — all carry active opencode
sessions, so every phase that touches their files waits for a stopping point.

| Leg | Plans | Uniquely exercises |
|---|---|---|
| HOT | `docs/00-scope.md` … `docs/11-artifact-manifest.md` (12 docs; pass explicitly — name discovery only matches `*plan*|*roadmap*|*todo*|*continuation*|*next_agent*`) | C/kmod corpus; numbered-doc style; Y2038 content dates; 25+ commits of drift; already shows real `missing` findings (`demo/` paths) |
| carlton-audio | `PLAN-blog-store.md`, `PLAN-store-continuation.md` (auto-discovered) | PHP/WordPress corpus; dirty mid-session tree; real `moved` anchor (`theme-setup.php:278` → `:291`) and `missing` artifact observed |
| rozelynx | none yet — the leg is `init` + first-session usage | Firefox-scale tree (suffix index; `moz-src`/`downloads` skipped); plan creation from live state |

## Stopping point

A repo is testable when its session is not actively working:

```sh
repo=/home/burningserenity/Documents/ByteWheel/HOT   # adjust per leg
# 1. no live opencode session inside it
for pid in $(pgrep -x opencode); do readlink /proc/$pid/cwd 2>/dev/null; done | grep -xF "$repo" || echo idle
# 2. tree and plans stable for a minute
git -C "$repo" status --porcelain > /tmp/a; sleep 60; git -C "$repo" status --porcelain > /tmp/b; diff /tmp/a /tmp/b
```

Both must hold before Phase 2. Phase 1 (read-only) may run any time, but
findings against a tree that is moving under the audit are meaningless —
prefer a stopping point there too. `audit`/`brief` never write; `--gates`
runs the repo's gate commands (build caches, side effects); `sync`/`log`/
`init` write only with `--apply`.

## Phase 0 — pre-flight (read-only, any time)

```sh
cd ~/Documents/ByteWheel/plansync
env -u GOROOT go test ./... && env -u GOROOT go vet ./...
env -u GOROOT go build -o bin/plansync ./cmd/plansync
./bin/plansync audit docs/ROADMAP.md --gates          # own gates: go test + go vet
```

Corpus regression harness — any parser/verifier change made during this test
must leave the six tuned corpora byte-identical (the 2026-11-02 pre-flight
fixes below passed exactly this):

```sh
BW=~/Documents/ByteWheel
for c in crabi Determinism uget sentiments opennic; do
  ./bin/plansync audit "$BW/$c" --color never
done
./bin/plansync audit "$BW/omnical" --repo ~/router-dav/rustical --repo ~/router-dav --color never
```

Snapshot before/after a change and diff (totals as of 2026-11-02: crabi
109 ok/60 missing; Determinism 16/4; uget 22 ok; sentiments 1/2; opennic
0/2 unknown; omnical 1093 ok/28 stale/20 moved/87 missing).

## Phase 1 — stopping point, read-only

```sh
# HOT: explicit paths (numbered docs are not name-discovered)
./bin/plansync audit ~/Documents/ByteWheel/HOT/docs/{0,1}*.md --repo ~/Documents/ByteWheel/HOT
./bin/plansync brief ~/Documents/ByteWheel/HOT/docs/02-patch-plan.md --repo ~/Documents/ByteWheel/HOT
# carlton-audio: directory arg auto-discovers both plans
./bin/plansync audit ~/Documents/Carlton_Audio/repos/carlton-audio --repo ~/Documents/Carlton_Audio/repos/carlton-audio
./bin/plansync brief ~/Documents/Carlton_Audio/repos/carlton-audio/PLAN-store-continuation.md --repo ~/Documents/Carlton_Audio/repos/carlton-audio
```

Spot-check protocol (per repo): pick one `stale`/`moved`/`missing` finding and
one `ok` claim; verify by hand —

```sh
git -C "$repo" status --porcelain          # git-tree claims
git -C "$repo" log --oneline -1 -- <path>  # "landed in <commit>" attributions
sed -n '<line>p' <file>                    # anchor/symbol claims
```

A finding that fails the hand check is a plansync bug — fix it (with a unit
test) before continuing, re-running the corpus harness.

## Phase 2 — stopping point, writes

One plan per repo, in this order; keep every `--apply` behind a reviewed
preview:

1. `sync --policy report` (computes heuristic edits, writes nothing) → read
   every hunk. Reject anything touching prose outside marker regions or the
   most-recent git-status block.
2. `sync --policy markers --apply` (or `heuristic` where step 1 was clean) →
   confirm `<plan>.bak` exists and equals the pre-image, diff only intended
   regions, post-apply re-audit improved.
3. `log --apply` with the real facts of the session that just stopped
   (subject/diagnosis/fixes/tests) → appended entry must re-audit clean.
4. rozelynx leg: `init CONTINUATION_PLAN.md --repo ~/Documents/ByteWheel/rozelynx --apply`
   → fresh scaffold must audit 0 findings and be a sync no-op.

Leave the repo no dirtier than the test requires; plan edits are ordinary
commits in those repos (their sessions will see them — that is the point).

## Phase 3 — agent integration (the actual organic test)

At each stopping point, embed the usage so the *next* session runs it
unprompted:

1. Paste the "Embedding in other repos" snippet from `AGENTS.md` into the
   repo's `AGENTS.md` (or `.opencode/instructions`), adjusting the repo list.
2. Optionally register the plugin in that repo's opencode config:
   `{ "plugin": ["file:///home/burningserenity/Documents/ByteWheel/plansync/opencode/plansync.ts"] }`
3. Success: the next session there runs `brief` at start and `log` at end on
   its own, and its reports check out against reality.

## Pass criteria (fill per leg)

| Check | HOT | carlton | rozelynx |
|---|---|---|---|
| no panic in any subcommand | | | |
| spot-checked findings 100% correct | | | |
| coverage: backticked `dir/file.ext` refs with content after digits parse | | | |
| sync: `.bak` == pre-image, diff within policy, re-audit improved | | | |
| log: entry re-audits clean | | | |
| init: scaffold 0 findings, sync no-op | — | — | |
| brief under `--max-lines`, findings + remaining work present | | | |
| audit wall time < 5 s per repo | | | |
| next session used it unprompted (Phase 3) | | | |

## Findings log

- **2026-11-02 pre-flight** (read-only smoke against live trees — Phase 0/1
  hybrid, safe because `audit` never writes):
  - `protoRe` over-match: `(?i)^[a-z][a-z0-9+.-]*/\d` ate `docs/04-validation.md`
    as a URI scheme, so HOT's numbered-doc refs parsed to zero claims. Fixed:
    end-anchored `/\d+([./]\d+)*$` keeps version pairs (`http/1.1`, `ns/2`,
    `llvm-19/20/21`, `glibc/2.34`) out, paths in. Unit test
    `TestParseNumericDirPaths`.
  - `ageNote` took the first date in the doc head — on Y2038 content that is
    `2038-01-19`, hiding 25 commits of drift behind "no repo advanced since".
    Fixed: first date ≤ today. Unit test `TestAgeNote`.
  - `indexSkipDirs` now skips `downloads` (rozelynx's 1.6 GB extracted
    floorp tarballs risked false suffix-resolution "ok"s).
  - All three verified byte-identical on the six tuning corpora + carlton.
  - Open: numbered docs (`docs/NN-*.md`) are not name-discovered — explicit
    paths are the documented workaround; a `--plans`-style manifest or a
    `NN-*` name pattern is a candidate follow-up.
  - Baselines at fix time: HOT 12 plans → 141 ok 3 missing 38 unknown;
    carlton-audio 2 plans → 62 ok 1 moved 1 missing 26 unknown.
