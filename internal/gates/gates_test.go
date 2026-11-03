package gates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bytewheel/plansync/internal/claim"
	"bytewheel/plansync/internal/verify"
)

func shAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
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

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ConfigFile)
	write(t, p, "# gate config\n\n[[gate]]\nname = \"unit tests\"\ncommand = \"go test ./...\"\n\n[[gate]]\ncommand = \"echo 16 tests\"\n")
	gs, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 2 {
		t.Fatalf("want 2 gates, got %+v", gs)
	}
	if gs[0].Name != "unit tests" || gs[0].Command != "go test ./..." {
		t.Fatalf("gate 0 wrong: %+v", gs[0])
	}
	if gs[1].Name != "gate 2" {
		t.Fatalf("unnamed gate should default its name: %+v", gs[1])
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"stray field":     "name = \"x\"\n",
		"unknown field":   "[[gate]]\nfoo = \"bar\"\n",
		"missing command": "[[gate]]\nname = \"x\"\n",
		"unquoted value":  "[[gate]]\ncommand = bare\n",
		"bare header":     "[gate]\ncommand = \"x\"\n",
		"unknown escape":  "[[gate]]\ncommand = \"a\\tb\"\n",
	}
	for name, content := range cases {
		p := filepath.Join(dir, name)
		write(t, p, content)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: Load should fail", name)
		}
	}
}

func TestLoadEscapes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ConfigFile)
	write(t, p, "[[gate]]\nname = \"quoted \\\"name\\\"\"\ncommand = \"echo \\\"ok\\\" && true\"\n")
	gs, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if gs[0].Name != `quoted "name"` || gs[0].Command != `echo "ok" && true` {
		t.Fatalf("escape handling wrong: %+v", gs[0])
	}
}

func TestFind(t *testing.T) {
	repo := t.TempDir()
	planDir := t.TempDir()
	write(t, filepath.Join(repo, ConfigFile), "[[gate]]\ncommand = \"true\"\n")
	write(t, filepath.Join(planDir, ConfigFile), "[[gate]]\ncommand = \"true\"\n")
	write(t, filepath.Join(planDir, "explicit.toml"), "[[gate]]\ncommand = \"true\"\n")
	plan := filepath.Join(planDir, "PLAN.md")

	if p, ok, err := Find(filepath.Join(planDir, "explicit.toml"), plan, []string{repo}); !ok || err != nil || p != filepath.Join(planDir, "explicit.toml") {
		t.Fatalf("explicit wrong: %q %v %v", p, ok, err)
	}
	if _, _, err := Find(filepath.Join(planDir, "missing.toml"), plan, nil); err == nil {
		t.Fatal("missing explicit config must error")
	}
	if p, ok, _ := Find("", plan, []string{repo}); !ok || p != filepath.Join(repo, ConfigFile) {
		t.Fatalf("repo discovery wrong: %q %v", p, ok)
	}
	empty := t.TempDir()
	if p, ok, _ := Find("", plan, []string{empty}); !ok || p != filepath.Join(planDir, ConfigFile) {
		t.Fatalf("plan-dir discovery wrong: %q %v", p, ok)
	}
	if _, ok, _ := Find("", filepath.Join(t.TempDir(), "P.md"), []string{empty}); ok {
		t.Fatal("no config should report ok=false")
	}
}

func TestRunAll(t *testing.T) {
	shAvailable(t)
	dir := t.TempDir()
	gs := []Gate{
		{Name: "count", Command: "echo '16 tests, 0 failures'"},
		{Name: "broken", Command: "echo 'boom' >&2; exit 3"},
	}
	runs := RunAll(gs, dir)
	if len(runs) != 2 {
		t.Fatalf("want 2 runs, got %+v", runs)
	}
	if runs[0].Failed || !strings.Contains(runs[0].Output, "16 tests") {
		t.Fatalf("run 0 wrong: %+v", runs[0])
	}
	if !runs[1].Failed || !strings.Contains(runs[1].Error, "exit status") || !strings.Contains(runs[1].Output, "boom") {
		t.Fatalf("run 1 wrong: %+v", runs[1])
	}
	if runs[0].Dir != dir {
		t.Fatalf("run dir wrong: %q", runs[0].Dir)
	}
}

func TestVerifyCounts(t *testing.T) {
	base := verify.Result{Kind: claim.KindCount, Status: verify.StatusInfo, Line: 7, Message: "numeric claim not verified: 16 tests", Detail: "16 tests"}
	cases := []struct {
		name   string
		detail string
		output string
		want   verify.Status
	}{
		{"match", "16 tests", "ok\n16 tests, 0 failures\n", verify.StatusOK},
		{"shape-crossing match", "61 tests", "OK=61 FAIL=0\n", verify.StatusOK},
		{"mismatch", "16 tests", "running\n15 tests, 1 failures\n", verify.StatusStale},
		{"no counts", "16 tests", "no numbers here\n", verify.StatusInfo},
		{"multi-number ok-fail", "OK=61 FAIL=0", "OK=61 FAIL=0\n", verify.StatusOK},
	}
	for _, c := range cases {
		r := base
		r.Detail = c.detail
		out := VerifyCounts([]verify.Result{r}, []Run{{Gate: Gate{Name: "g", Command: "x"}, Output: c.output}})
		if out[0].Status != c.want {
			t.Errorf("%s: status = %s, want %s (%s)", c.name, out[0].Status, c.want, out[0].Message)
		}
		if c.want == verify.StatusStale && !strings.Contains(out[0].Message, "15 tests") {
			t.Errorf("%s: stale message should show actual: %s", c.name, out[0].Message)
		}
	}

	// Non-count results pass through untouched.
	other := verify.Result{Kind: claim.KindPath, Status: verify.StatusOK, Line: 3, Message: "exists"}
	out := VerifyCounts([]verify.Result{other}, []Run{{Output: "16 tests"}})
	if out[0].Status != verify.StatusOK || out[0].Message != "exists" {
		t.Fatalf("non-count result modified: %+v", out[0])
	}

	// No runs at all: claim stays info with the no-gates note.
	out = VerifyCounts([]verify.Result{base}, nil)
	if out[0].Status != verify.StatusInfo || !strings.Contains(out[0].Message, "not confirmed by gates") {
		t.Fatalf("no-run case wrong: %+v", out[0])
	}
}

func TestNumberRuns(t *testing.T) {
	if got := numberRuns("OK=61 FAIL=0; 16 tests"); strings.Join(got, ",") != "61,0,16" {
		t.Fatalf("numberRuns wrong: %v", got)
	}
	if got := numberRuns("none"); len(got) != 0 {
		t.Fatalf("numberRuns should be empty: %v", got)
	}
}
