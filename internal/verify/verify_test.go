package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bytewheel/plansync/internal/claim"
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

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyGitTreeStaleAndOK(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "crates/a.rs"), "fn alpha() {}\nfn beta() {}\n")
	write(t, filepath.Join(dir, "crates/b.rs"), "fn gamma() {}\n")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "-C", dir, "add", "-A")
	run("git", "-C", dir, "commit", "-q", "-m", "add crates")

	doc := "```\n M crates/a.rs\n?? crates/new.rs\n```\n"
	v := New([]string{dir}, dir, doc)
	results := v.Verify(claim.Parse(doc))
	if len(results) != 1 || results[0].Status != StatusStale {
		t.Fatalf("expected stale git-tree, got %+v", results)
	}
	if !strings.Contains(results[0].Message, "dirty entries") {
		t.Errorf("message should mention gone entries: %s", results[0].Message)
	}

	write(t, filepath.Join(dir, "crates/new.rs"), "new\n")
	doc2 := "```\n?? crates/new.rs\n```\n"
	v2 := New([]string{dir}, dir, doc2)
	r2 := v2.Verify(claim.Parse(doc2))
	if len(r2) != 1 || r2[0].Status != StatusOK {
		t.Fatalf("expected ok git-tree, got %+v", r2)
	}
}

func TestVerifyGitTreeReportsUncommittedWork(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "src/x.go"), "package x\n")
	doc := "```\n M src/x.go\n```\n"
	v := New([]string{dir}, dir, doc)
	r := v.Verify(claim.Parse(doc))
	if len(r) != 1 || r[0].Status != StatusStale {
		t.Fatalf("expected stale (file not yet tracked), got %+v", r)
	}
}

func TestVerifyAnchorMovedAndOK(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "src/lib.rs"), "package lib\n\nfn helper() {}\n\npub fn normalize_rrule_until(ics: &str) {}\n")

	doc := "the import wiring at `src/lib.rs:2` calls `normalize_rrule_until`\n"
	v := New([]string{dir}, dir, doc)
	r := v.Verify(claim.Parse(doc))
	var anchor *Result
	for i := range r {
		if r[i].Kind == "anchor" {
			anchor = &r[i]
		}
	}
	if anchor == nil {
		t.Fatalf("no anchor result: %+v", r)
	}
	if anchor.Status != StatusMoved {
		t.Fatalf("expected moved, got %s (%s)", anchor.Status, anchor.Message)
	}
	if anchor.Suggestion != "src/lib.rs:5" {
		t.Errorf("suggestion mismatch: %q", anchor.Suggestion)
	}

	doc2 := "the wiring at `src/lib.rs:5` calls `normalize_rrule_until`\n"
	v2 := New([]string{dir}, dir, doc2)
	for _, res := range v2.Verify(claim.Parse(doc2)) {
		if res.Kind == "anchor" && res.Status != StatusOK {
			t.Errorf("expected ok anchor, got %s: %s", res.Status, res.Message)
		}
	}
}

func TestVerifyAnchorMissingFile(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	doc := "see `src/gone.rs:10` for `the_thing`\n"
	v := New([]string{dir}, dir, doc)
	for _, r := range v.Verify(claim.Parse(doc)) {
		if r.Kind == "anchor" && r.Status != StatusUnknown {
			t.Errorf("expected unknown, got %s", r.Status)
		}
	}
}

func TestVerifyCommitAndBranch(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(out))
	doc := "committed in `" + head[:8] + "` on branch `main` (| head |)\n"
	v := New([]string{dir}, dir, doc)
	var sawCommit, sawBranch bool
	for _, r := range v.Verify(claim.Parse(doc)) {
		if r.Kind == "commit" {
			sawCommit = true
			if r.Status != StatusOK && r.Status != StatusInfo {
				t.Errorf("commit status %s: %s", r.Status, r.Message)
			}
		}
		if r.Kind == "branch" {
			sawBranch = true
			if r.Status != StatusOK {
				t.Errorf("branch status %s: %s", r.Status, r.Message)
			}
		}
	}
	if !sawCommit || !sawBranch {
		t.Fatalf("missing claims: commit=%v branch=%v", sawCommit, sawBranch)
	}
}

func TestVerifyPathExternal(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	tmp := t.TempDir()
	ext := filepath.Join(tmp, "notes.txt")
	write(t, ext, "hi\n")
	doc := "see `" + ext + "` and `nope/missing.rs`\n"
	v := New([]string{dir}, dir, doc)
	results := v.Verify(claim.Parse(doc))
	var missing int
	for _, r := range results {
		if r.Kind == "path" && r.Status == StatusMissing {
			missing++
		}
	}
	if missing != 1 {
		t.Errorf("expected 1 missing path, got %d: %+v", missing, results)
	}
}

func TestDetectReposFromHints(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "PLAN.md"), "# plan\n")
	hints := []string{dir}
	repos := DetectRepos(nil, filepath.Join(dir, "PLAN.md"), hints)
	if len(repos) != 1 || repos[0] != dir {
		t.Fatalf("detect mismatch: %v", repos)
	}
}
