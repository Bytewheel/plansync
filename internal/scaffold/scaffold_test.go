package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bytewheel/plansync/internal/brief"
	"bytewheel/plansync/internal/claim"
	"bytewheel/plansync/internal/syncer"
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
		{"git", "-C", dir, "init", "-q", "-b", "main"},
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

func dirtyRepo(t *testing.T) string {
	t.Helper()
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "src/a.rs"), "one\n")
	run(t, "git", "-C", dir, "add", "-A")
	run(t, "git", "-C", dir, "commit", "-q", "-m", "add")
	write(t, filepath.Join(dir, "src/a.rs"), "changed\n")
	write(t, filepath.Join(dir, "src/new.rs"), "new\n")
	return dir
}

func TestDefaultTitle(t *testing.T) {
	for in, want := range map[string]string{
		"PLAN_NEXT_AGENT_403_FIX.md": "PLAN NEXT AGENT 403 FIX",
		"docs/continuation_plan.md":  "continuation plan",
		"/tmp/x/My-Plan.md":          "My — Plan",
		"bare.md":                    "bare",
	} {
		if got := DefaultTitle(in); got != want {
			t.Errorf("DefaultTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildShape(t *testing.T) {
	dir := dirtyRepo(t)
	v := verify.New([]string{dir}, dir, "")
	lines := Build(Options{Title: "mirror finish", Date: "2026-11-02"}, v)
	doc := strings.Join(lines, "\n")
	for _, want := range []string{
		"# Continuation plan — mirror finish (2026-11-02)",
		"**Repo:** `" + dir + "`, branch `main`, HEAD `",
		"## Status",
		"<!-- plansync:begin git-status repo=" + dir + " -->",
		" M src/a.rs",
		"?? src/new.rs",
		"<!-- plansync:end -->",
		"<!-- plansync:begin heads -->",
		"| repo | head |",
		"## Remaining steps, in order",
		"- [ ] describe the first concrete step",
		"## Gotchas / landmines",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("scaffold missing %q:\n%s", want, doc)
		}
	}
}

func TestBuildCleanTree(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	v := verify.New([]string{dir}, dir, "")
	doc := strings.Join(Build(Options{Title: "x", Date: "2026-11-02"}, v), "\n")
	if !strings.Contains(doc, "# clean (HEAD ") {
		t.Fatalf("clean tree should produce a clean marker body:\n%s", doc)
	}
}

func TestBuildNoRepos(t *testing.T) {
	v := verify.New(nil, "", "")
	doc := strings.Join(Build(Options{Title: "x", Date: "2026-11-02"}, v), "\n")
	if !strings.Contains(doc, "**Repo:** `TODO/repo/path`") {
		t.Fatalf("no-repo scaffold should carry a TODO header:\n%s", doc)
	}
	if strings.Contains(doc, "plansync:begin") {
		t.Fatalf("no-repo scaffold should not emit live markers:\n%s", doc)
	}
}

func TestScaffoldAuditsCleanAndSyncIsNoOp(t *testing.T) {
	dir := dirtyRepo(t)
	v := verify.New([]string{dir}, dir, "")
	doc := strings.Join(Build(Options{Title: "x", Date: "2026-11-02"}, v), "\n")
	claims := claim.Parse(doc)
	results := v.Verify(claims)
	for _, r := range results {
		switch r.Status {
		case verify.StatusStale, verify.StatusMoved, verify.StatusMissing:
			t.Errorf("scaffold does not audit clean: %s L%d %s %s", r.Status, r.Line, r.Kind, r.Message)
		}
	}
	lines := strings.Split(doc, "\n")
	edits, warnings := syncer.Compute(lines, v, claims, results, syncer.PolicyMarkers)
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if len(edits) != 0 {
		t.Fatalf("freshly scaffolded plan must be a sync no-op, got %+v", edits)
	}
}

func TestScaffoldBriefHook(t *testing.T) {
	dir := dirtyRepo(t)
	v := verify.New([]string{dir}, dir, "")
	doc := strings.Join(Build(Options{Title: "x", Date: "2026-11-02"}, v), "\n")
	claims := claim.Parse(doc)
	b := brief.Build("plan.md", []string{dir}, doc, claims, v.Verify(claims), "")
	if len(b.Sections) != 1 || b.Sections[0].Heading != "Remaining steps, in order" {
		t.Fatalf("brief should hook the remaining-work section: %+v", b.Sections)
	}
	if b.OpenCount == 0 {
		t.Fatalf("scaffold checkbox not counted: %+v", b)
	}
}

func TestWriteRefusesOverwriteAndCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "PLAN.md")
	if err := Write(nested, []string{"# x", ""}); err != nil {
		t.Fatalf("write to nested dir failed: %v", err)
	}
	if data, err := os.ReadFile(nested); err != nil || string(data) != "# x\n" {
		t.Fatalf("written content wrong: %q %v", string(data), err)
	}
	err := Write(nested, []string{"# y"})
	if err == nil {
		t.Fatal("second write must refuse")
	}
	if _, ok := err.(*ExistsError); !ok {
		t.Fatalf("want *ExistsError, got %T (%v)", err, err)
	}
}
