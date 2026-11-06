package claim

import (
	"testing"
)

func TestParseGitTree(t *testing.T) {
	doc := "Working tree state (uncommitted):\n\n```\n" +
		" M crates/caldav/src/calendar_object/methods.rs   (+59/-13 net: import closure)\n" +
		" M crates/ical/src/lib.rs\n" +
		"?? crates/ical/src/normalize.rs                   (NEW, 539 lines incl. 15 tests)\n" +
		"```\n"
	claims := Parse(doc)
	var trees int
	for _, c := range claims {
		if c.Kind != KindGitTree {
			t.Errorf("unexpected claim %v on line %d", c.Kind, c.Line)
		}
		trees++
		if len(c.Entries) != 3 {
			t.Fatalf("want 3 entries, got %d", len(c.Entries))
		}
		if c.Entries[0].XY != " M" || c.Entries[0].Path != "crates/caldav/src/calendar_object/methods.rs" {
			t.Errorf("entry 0 mismatch: %+v", c.Entries[0])
		}
		if c.Entries[0].Note != "(+59/-13 net: import closure)" {
			t.Errorf("note mismatch: %q", c.Entries[0].Note)
		}
		if c.Entries[2].XY != "??" || c.Entries[2].Path != "crates/ical/src/normalize.rs" {
			t.Errorf("entry 2 mismatch: %+v", c.Entries[2])
		}
	}
	if trees != 1 {
		t.Fatalf("want exactly 1 git-tree claim, got %d", trees)
	}
}

func TestParseGitTreeRename(t *testing.T) {
	doc := "```\nR  old/path.rs -> new/path.rs\n```\n"
	for _, c := range Parse(doc) {
		if c.Kind != KindGitTree {
			t.Fatalf("unexpected kind %v", c.Kind)
		}
		if len(c.Entries) != 1 || c.Entries[0].Path != "new/path.rs" {
			t.Fatalf("rename path mismatch: %+v", c.Entries)
		}
	}
}

func TestParseProseNotGitEntry(t *testing.T) {
	docs := []string{
		"A man walked into the repo\n",
		"git status calmoji/                             # review; ~330 KB committed\n",
		" M not-a-slash-path\n",
		"3 M and 4 N are constants\n",
		// Fenced prose whose first token happens to be a staged-only XY
		// spelling stays rejected (the in-fence bare-path carve-out is
		// for worktree/untracked codes only).
		"```\nM  hamlet\n```\n",
		"```\nA  tale\n```\n",
	}
	for _, doc := range docs {
		for _, c := range Parse(doc) {
			if c.Kind == KindGitTree {
				t.Errorf("false git-tree claim from %q: %+v", doc, c)
			}
		}
	}
}

func TestParseAnchors(t *testing.T) {
	doc := "Wired into `CalendarObject::import` (`crates/ical/src/calendar_object.rs:79`)\n" +
		"— the DAV PUT path, tested at\n` :1506`.\n" +
		"Range ref `crates/scheduling/src/scheduler.rs:167-181`.\n"
	claims := Parse(doc)
	var anchors []Claim
	for _, c := range claims {
		if c.Kind == KindAnchor {
			anchors = append(anchors, c)
		}
	}
	if len(anchors) != 3 {
		t.Fatalf("want 3 anchors, got %d: %+v", len(anchors), claims)
	}
	if anchors[0].Path != "crates/ical/src/calendar_object.rs" || anchors[0].Start != 79 {
		t.Errorf("anchor 0 mismatch: %+v", anchors[0])
	}
	if anchors[1].Path != "crates/ical/src/calendar_object.rs" || anchors[1].Start != 1506 {
		t.Errorf("elided anchor mismatch: %+v", anchors[1])
	}
	if anchors[2].Path != "crates/scheduling/src/scheduler.rs" || anchors[2].Start != 167 || anchors[2].End != 181 {
		t.Errorf("range anchor mismatch: %+v", anchors[2])
	}
	found := false
	for _, s := range anchors[0].Symbols {
		if s == "CalendarObject::import" || s == "CalendarObject" {
			found = true
		}
	}
	if !found {
		t.Errorf("symbols missing from anchor 0: %+v", anchors[0].Symbols)
	}
}

func TestParseAnchorGuards(t *testing.T) {
	docs := []string{
		"server at `https://0115d8cf.duckdns.org:8443/caldav/` is live\n",
		"port-forward to `192.168.1.21:443` configured\n",
		"see `https://github.com/lennart-k/rustical` and `http://x:80`\n",
	}
	for _, doc := range docs {
		for _, c := range Parse(doc) {
			if c.Kind == KindAnchor {
				t.Errorf("false anchor from %q: %+v", doc, c)
			}
		}
	}
}

func TestParseNumericDirPaths(t *testing.T) {
	// Digit-led directory components (docs/04-validation.md) must stay
	// path claims; version-pair tokens (http/1.1, ns/2, llvm-19/20/21,
	// glibc/2.34) must not.
	doc := "see `docs/04-validation.md`, `docs/00-scope.md`, `v2/302.html`\n" +
		"versions `http/1.1`, `ns/2`, `llvm-19/20/21`, `omnical-smoke/1.0`, `glibc/2.34`\n"
	want := map[string]bool{
		"docs/04-validation.md": false,
		"docs/00-scope.md":      false,
		"v2/302.html":           false,
	}
	for _, c := range Parse(doc) {
		if c.Kind != KindPath {
			continue
		}
		if _, ok := want[c.Path]; !ok {
			t.Errorf("unexpected path claim %q", c.Path)
			continue
		}
		want[c.Path] = true
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("path claim missing: %q", p)
		}
	}
}

func TestParseCommitsBranchesPaths(t *testing.T) {
	doc := "**Repo:** `~/router-dav/rustical`, branch `omnical-scheduling`\n" +
		"| Server | `/home/user/router-dav/rustical` | `Bytewheel/Omnical-Server` | `35049a53` |\n" +
		"see `crates/ical/src/normalize.rs` and `scripts/build-rust.sh`\n"
	claims := Parse(doc)
	var gotBranch, gotCommit, gotPaths int
	for _, c := range claims {
		switch c.Kind {
		case KindBranch:
			gotBranch++
			if c.Name != "omnical-scheduling" {
				t.Errorf("branch mismatch: %+v", c)
			}
		case KindCommit:
			gotCommit++
			if c.Hash != "35049a53" {
				t.Errorf("commit mismatch: %+v", c)
			}
		case KindPath:
			gotPaths++
		}
	}
	if gotBranch != 1 {
		t.Errorf("want 1 branch claim, got %d", gotBranch)
	}
	if gotCommit != 1 {
		t.Errorf("want 1 commit claim, got %d", gotCommit)
	}
	if gotPaths < 2 {
		t.Errorf("want >=2 path claims, got %d", gotPaths)
	}
}

func TestParseCheckboxAndCounts(t *testing.T) {
	doc := "- [x] chat parser done\n- [ ] still open\n\nGates: OK=623 FAIL=23; 827 workspace tests; 98/98 pinned.\n"
	claims := Parse(doc)
	var boxes, counts int
	var open int
	for _, c := range claims {
		if c.Kind == KindCheckbox {
			boxes++
			if !c.Checked {
				open++
			}
		}
		if c.Kind == KindCount {
			counts++
		}
	}
	if boxes != 2 || open != 1 {
		t.Errorf("checkbox mismatch: %d boxes, %d open", boxes, open)
	}
	if counts < 2 {
		t.Errorf("want >=2 count claims, got %d", counts)
	}
}

func TestRepoHints(t *testing.T) {
	doc := "**Repo:** `~/router-dav/rustical`, branch `omnical-scheduling`\n" +
		"**Working tree:** `~/router-dav` (check `git status` first)\n" +
		"**Work branch:** `custom-server`\n" +
		"| Server | `/home/user/router-dav/rustical` | remote | head |\n" +
		"| Plan | `/home/user/Documents/omnical` | remote | `e3fc0be` |\n"
	hints := RepoHints(doc)
	want := []string{"~/router-dav/rustical", "~/router-dav", "/home/user/router-dav/rustical", "/home/user/Documents/omnical"}
	if len(hints) != len(want) {
		t.Fatalf("hints mismatch: %v", hints)
	}
	for i, w := range want {
		if hints[i] != w {
			t.Errorf("hint %d: want %q got %q", i, w, hints[i])
		}
	}
}

func TestRepoHintsCodeRepoLabel(t *testing.T) {
	// The omnical corpus declares repos inside bulleted metadata lists.
	doc := "- **Date:** 2026-10-30\n" +
		"- **Project:** `omnical`\n" +
		"- **Code repo:** `~/router-dav` (submodule `rustical/`, branch `main` of\n" +
		"  `Bytewheel/Omnical-Server`). This repo holds the plan only.\n"
	hints := RepoHints(doc)
	if len(hints) != 1 || hints[0] != "~/router-dav" {
		t.Fatalf("want [~/router-dav], got %v", hints)
	}
}

func TestRepoHintsBulletList(t *testing.T) {
	doc := "**Repos:**\n" +
		"- `~/router-dav` — Omnical-Code (CI, packaging, deploy)\n" +
		"- `~/router-dav/rustical` — Omnical-Server, the code (landed as `e30c90d9`,\n" +
		"  branch `main`, tree clean)\n" +
		"- this repo — plans (no path backticks)\n" +
		"\n" +
		"Prose after the list. A stray `- `/not/a/repo` bullet with no label\n" +
		"above it must not be collected.\n"
	hints := RepoHints(doc)
	want := []string{"~/router-dav", "~/router-dav/rustical"}
	if len(hints) != len(want) {
		t.Fatalf("hints mismatch: %v", hints)
	}
	for i, w := range want {
		if hints[i] != w {
			t.Errorf("hint %d: want %q got %q", i, w, hints[i])
		}
	}
}

func TestRepoHintsBulletListWithProseLabel(t *testing.T) {
	doc := "**Repos** (same trio as `PLAN_NEXT_AGENT_APHY_IMPORT_RENDER.md`):\n" +
		"- `~/router-dav/rustical` — Omnical-Server, all the code. Head at writing: `43af1493`, tree clean.\n" +
		"- `~/router-dav` — Omnical-Code (CI, packaging, deploy).\n" +
		"- this repo — plans.\n" +
		"\n" +
		"---\n"
	hints := RepoHints(doc)
	want := []string{"~/router-dav/rustical", "~/router-dav"}
	if len(hints) != len(want) {
		t.Fatalf("hints mismatch: %v", hints)
	}
	for i, w := range want {
		if hints[i] != w {
			t.Errorf("hint %d: want %q got %q", i, w, hints[i])
		}
	}
}

func TestRepoHintsSingularLabelDoesNotStartList(t *testing.T) {
	doc := "**Working tree:** `~/router-dav` clean at `abc1234`\n" +
		"\n" +
		"- `/etc/passwd` — unrelated bullet list after a singular label\n"
	hints := RepoHints(doc)
	if len(hints) != 1 || hints[0] != "~/router-dav" {
		t.Fatalf("want [~/router-dav], got %v", hints)
	}
}

func TestSymbolsIgnoreProse(t *testing.T) {
	line := "| import wiring | `crates/ical/src/calendar_object.rs:79` |"
	syms := symbols(line, "crates/ical/src/calendar_object.rs")
	if len(syms) != 0 {
		t.Errorf("prose leaked into symbols: %v", syms)
	}
	line2 := "| `put_event` (import closure) + `stamp_default_organizer` | `methods.rs:59` |"
	syms2 := symbols(line2, "methods.rs")
	if len(syms2) != 2 {
		t.Errorf("want 2 symbols, got %v", syms2)
	}
}

func TestFencedCommandsProduceNoClaims(t *testing.T) {
	doc := "```sh\nexport SSH_AUTH_SOCK=/home/user/.ssh/agent/s.9A30FBhmm9.agent\nexport GIT_SSH_COMMAND=\"ssh -o BatchMode=yes -i /home/user/.ssh/id_burningserenity -o IdentitiesOnly=yes\"\n```\n"
	for _, c := range Parse(doc) {
		t.Errorf("fenced command produced claim: %+v", c)
	}
}

func TestParseFencedSubmoduleEntry(t *testing.T) {
	// A dirty submodule is a bare directory name (" M rustical") — the
	// line must join the SAME claim as the entries around it (a rejected
	// line would split the block into two broken claims), and untracked
	// root files ("?? notes") claim the same way.
	doc := "```\n M omni-plugins/src/web.rs\n M rustical\n?? omni-plugin-currentrms/tests/targeting.rs\n```\n"
	claims := Parse(doc)
	if len(claims) != 1 {
		t.Fatalf("want exactly 1 git-tree claim, got %d: %+v", len(claims), claims)
	}
	c := claims[0]
	if c.Kind != KindGitTree {
		t.Fatalf("unexpected kind %v", c.Kind)
	}
	if len(c.Entries) != 3 {
		t.Fatalf("want 3 entries (submodule included), got %d: %+v", len(c.Entries), c.Entries)
	}
	if c.Entries[1].XY != " M" || c.Entries[1].Path != "rustical" {
		t.Errorf("submodule entry mismatch: %+v", c.Entries[1])
	}
}
