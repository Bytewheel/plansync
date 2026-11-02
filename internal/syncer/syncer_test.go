package syncer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bytewheel/plansync/internal/claim"
	"bytewheel/plansync/internal/verify"
)

func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "-C", dir, "init", "-q"},
		{"git", "-C", dir, "config", "user.email", "t@example.com"},
		{"git", "-C", dir, "config", "user.name", "T"},
		{"git", "-C", dir, "commit", "--allow-empty", "-q", "-m", "init"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
}

func run(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setup builds a verifier + results for doc against repo dir.
func setup(t *testing.T, dir, doc string, policy Policy) ([]string, []Edit, []string) {
	t.Helper()
	lines := strings.Split(doc, "\n")
	claims := claim.Parse(doc)
	v := verify.New([]string{dir}, dir, doc)
	results := v.Verify(claims)
	edits, warnings := Compute(lines, v, claims, results, policy)
	return lines, edits, warnings
}

func TestMarkerGitStatusRefreshAndIdempotence(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "src/a.rs"), "one\n")
	write(t, filepath.Join(dir, "src/b.rs"), "two\n")
	run(t, "git", "-C", dir, "add", "-A")
	run(t, "git", "-C", dir, "commit", "-q", "-m", "add src")
	write(t, filepath.Join(dir, "src/a.rs"), "changed\n")
	write(t, filepath.Join(dir, "src/new.rs"), "new\n")

	doc := "# plan\n\n<!-- plansync:begin git-status repo=" + dir + " -->\n```\n M src/stale.rs\n```\n<!-- plansync:end -->\n"
	lines, edits, warnings := setup(t, dir, doc, PolicyMarkers)
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if len(edits) != 1 || edits[0].Kind != "marker-git-status" {
		t.Fatalf("want 1 marker-git-status edit, got %+v", edits)
	}
	body := strings.Join(edits[0].After, "\n")
	if !strings.Contains(body, "```") || !strings.Contains(body, "M src/a.rs") || !strings.Contains(body, "?? src/new.rs") {
		t.Fatalf("body should contain current porcelain: %q", body)
	}
	out, err := Apply(lines, edits)
	if err != nil {
		t.Fatal(err)
	}
	newDoc := strings.Join(out, "\n")
	if strings.Contains(newDoc, "src/stale.rs") {
		t.Fatalf("stale entry survived: %s", newDoc)
	}
	// Second compute on the synced doc must be a no-op.
	_, edits2, _ := setup(t, dir, newDoc, PolicyMarkers)
	if len(edits2) != 0 {
		t.Fatalf("sync not idempotent: %+v", edits2)
	}
}

func TestMarkerGitStatusClean(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	doc := "<!-- plansync:begin git-status repo=" + dir + " -->\n```\n M src/x.rs\n```\n<!-- plansync:end -->\n"
	_, edits, _ := setup(t, dir, doc, PolicyMarkers)
	if len(edits) != 1 {
		t.Fatalf("want 1 edit, got %+v", edits)
	}
	body := strings.Join(edits[0].After, "\n")
	if !strings.Contains(body, "# clean (HEAD ") {
		t.Fatalf("clean body wrong: %q", body)
	}
}

func TestMarkerHeads(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	doc := "<!-- plansync:begin heads -->\n| repo | head |\n|---|---|\n| old | `deadbee` |\n<!-- plansync:end -->\n"
	_, edits, warnings := setup(t, dir, doc, PolicyMarkers)
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if len(edits) != 1 || edits[0].Kind != "marker-heads" {
		t.Fatalf("want 1 marker-heads edit, got %+v", edits)
	}
	body := strings.Join(edits[0].After, "\n")
	if !strings.Contains(body, "| repo | head |") || !strings.Contains(body, "`") {
		t.Fatalf("heads body wrong: %q", body)
	}
}

func TestMarkerUnclosedAndUnknownKind(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	doc := "<!-- plansync:begin git-status repo=" + dir + " -->\n M src/x.rs\n"
	_, _, warnings := setup(t, dir, doc, PolicyMarkers)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no matching plansync:end") {
		t.Fatalf("warnings: %v", warnings)
	}
	doc2 := "<!-- plansync:begin bogus -->\nx\n<!-- plansync:end -->\n"
	_, _, warnings2 := setup(t, dir, doc2, PolicyMarkers)
	if len(warnings2) != 1 || !strings.Contains(warnings2[0], "unknown marker kind") {
		t.Fatalf("warnings2: %v", warnings2)
	}
}

func TestAnchorReanchor(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "src/lib.rs"), "package lib\n\nfn helper() {}\n\npub fn normalize_rrule_until(ics: &str) {}\n")
	run(t, "git", "-C", dir, "add", "-A")
	run(t, "git", "-C", dir, "commit", "-q", "-m", "lib")

	doc := "the wiring at `src/lib.rs:2` calls `normalize_rrule_until`\nand is tested at\n`:2` later.\n"
	lines, edits, _ := setup(t, dir, doc, PolicyMarkers)
	if len(edits) != 2 {
		t.Fatalf("want 2 anchor edits (full + elided), got %+v", edits)
	}
	out, err := Apply(lines, edits)
	if err != nil {
		t.Fatal(err)
	}
	newDoc := strings.Join(out, "\n")
	if !strings.Contains(newDoc, "`src/lib.rs:5`") {
		t.Fatalf("full anchor not re-pointed: %s", newDoc)
	}
	if !strings.Contains(newDoc, "`:5`") {
		t.Fatalf("elided anchor not re-pointed: %s", newDoc)
	}
}

func TestCommitRefreshPolicyGating(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "src/a.rs"), "x\n")
	run(t, "git", "-C", dir, "add", "-A")
	run(t, "git", "-C", dir, "commit", "-q", "-m", "second")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD~1").Output()
	if err != nil {
		t.Fatal(err)
	}
	oldHead := strings.TrimSpace(string(out))
	doc := "The head of the repo is `" + oldHead + "`.\n"

	_, markerEdits, _ := setup(t, dir, doc, PolicyMarkers)
	for _, e := range markerEdits {
		if e.Kind == "commit" {
			t.Fatalf("markers policy must not rewrite unmarked heads: %+v", e)
		}
	}
	lines, edits, _ := setup(t, dir, doc, PolicyHeuristic)
	var commitEdits int
	for _, e := range edits {
		if e.Kind == "commit" {
			commitEdits++
		}
	}
	if commitEdits != 1 {
		t.Fatalf("want 1 commit edit under heuristic, got %+v", edits)
	}
	outLines, err := Apply(lines, edits)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if !strings.Contains(strings.Join(outLines, "\n"), strings.TrimSpace(string(cur))) {
		t.Fatalf("head not refreshed: %v", outLines)
	}
}

func TestHeuristicLastBlockOnly(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "src/a.rs"), "x\n")
	run(t, "git", "-C", dir, "add", "-A")
	run(t, "git", "-C", dir, "commit", "-q", "-m", "add")

	doc := "# history\n\n```\n M src/a.rs\n```\n\nmiddle prose\n\n```\n M src/a.rs\n```\n"
	_, edits, _ := setup(t, dir, doc, PolicyHeuristic)
	var treeEdits []Edit
	for _, e := range edits {
		if e.Kind == "git-tree" {
			treeEdits = append(treeEdits, e)
		}
	}
	if len(treeEdits) != 1 {
		t.Fatalf("want exactly 1 git-tree edit (last block), got %+v", edits)
	}
	if treeEdits[0].Start != 10 {
		t.Fatalf("should start at the second block (line 10), got %d", treeEdits[0].Start)
	}
	if !strings.Contains(strings.Join(treeEdits[0].After, "\n"), "# clean") {
		t.Fatalf("clean tree should produce clean body: %+v", treeEdits[0])
	}
	// Markers policy must not touch unmarked blocks.
	_, markerEdits, _ := setup(t, dir, doc, PolicyMarkers)
	for _, e := range markerEdits {
		if e.Kind == "git-tree" {
			t.Fatalf("markers policy must not rewrite unmarked blocks: %+v", e)
		}
	}
}

func TestAnchorInsideMarkerDropped(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "src/lib.rs"), "package lib\n\nfn helper() {}\n\npub fn normalize_rrule_until(x: &str) {}\n")
	run(t, "git", "-C", dir, "add", "-A")
	run(t, "git", "-C", dir, "commit", "-q", "-m", "lib")

	doc := "<!-- plansync:begin git-status repo=" + dir + " -->\n```\n M src/x.rs\n```\n<!-- plansync:end -->\n\nthe wiring at `src/lib.rs:2` calls `normalize_rrule_until`\n"
	lines, edits, _ := setup(t, dir, doc, PolicyMarkers)
	var kinds []string
	for _, e := range edits {
		kinds = append(kinds, e.Kind)
	}
	if len(edits) != 2 {
		t.Fatalf("want marker + anchor edits, got %v", kinds)
	}
	// No overlap after apply.
	if _, err := Apply(lines, edits); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
}

func TestApplyInsertionAndMultipleEdits(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	edits := []Edit{
		{Start: 2, End: 2, Kind: "x", After: []string{"B1", "B2"}},
		{Start: 5, End: 4, Kind: "ins", After: []string{"E-below"}},
	}
	out, err := Apply(lines, edits)
	if err != nil {
		t.Fatal(err)
	}
	want := "a\nB1\nB2\nc\nd\nE-below\ne"
	if got := strings.Join(out, "\n"); got != want {
		t.Fatalf("apply mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestParsePolicy(t *testing.T) {
	for _, s := range []string{"markers", "heuristic", "report"} {
		if _, err := ParsePolicy(s); err != nil {
			t.Errorf("ParsePolicy(%q): %v", s, err)
		}
	}
	if _, err := ParsePolicy("yolo"); err == nil {
		t.Error("ParsePolicy(yolo) should fail")
	}
}

func TestRenderDiffShape(t *testing.T) {
	lines := []string{"one", "two", "three", "four"}
	edits := []Edit{{Start: 2, End: 3, Kind: "test", Reason: "why", After: []string{"TWO"}}}
	got := RenderDiff("p.md", lines, edits)
	for _, want := range []string{"== p.md", "@@ L2-L3 @@", "- two", "- three", "+ TWO", "  one", "  four"} {
		if !strings.Contains(got, want) {
			t.Errorf("diff missing %q:\n%s", want, got)
		}
	}
}
