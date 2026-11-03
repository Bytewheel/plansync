package doclog

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

func TestGitStateDirtyAndClean(t *testing.T) {
	gitAvailable(t)
	dirty := dirtyRepo(t)
	clean := t.TempDir()
	initRepo(t, clean)

	v := verify.New([]string{dirty, clean}, dirty, "")
	lines := GitState(v)
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "**Repo:** `"+dirty+"`, branch `main`, HEAD `") {
		t.Fatalf("repo header line wrong:\n%s", text)
	}
	if !strings.Contains(text, "M src/a.rs") || !strings.Contains(text, "?? src/new.rs") {
		t.Fatalf("porcelain entries missing:\n%s", text)
	}
	if !strings.Contains(text, "working tree clean.") {
		t.Fatalf("clean repo should note clean tree:\n%s", text)
	}
	// The dirty block must be fenced so audit parses it as a git-tree claim.
	if strings.Count(text, "```") < 2 {
		t.Fatalf("dirty state must be fenced:\n%s", text)
	}
}

func TestBuildDefaultShape(t *testing.T) {
	gitAvailable(t)
	dir := dirtyRepo(t)
	v := verify.New([]string{dir}, dir, "")
	o := Options{
		Date:      "2026-11-02",
		Subject:   "mirror 403 fix",
		Diagnosis: "floating UNTIL rejected by caldata",
		Fixes:     "normalize_rrule_until on import",
		Tests:     "rustical_ical 16, rustical_scheduling 61, 0 failures",
		Deploy:    "deploy.sh run, services restarted",
	}
	entry := Build(o, v)
	text := strings.Join(entry, "\n")
	if !strings.HasPrefix(text, "### 2026-11-02 — mirror 403 fix\n") {
		t.Fatalf("heading wrong:\n%s", text)
	}
	for _, want := range []string{
		"**Diagnosis:** floating UNTIL rejected by caldata",
		"**Fixes:** normalize_rrule_until on import",
		"**Test counts:** rustical_ical 16, rustical_scheduling 61, 0 failures",
		"**Deploy transcript:** deploy.sh run, services restarted",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("section %q missing:\n%s", want, text)
		}
	}
	di, fi, te, de := strings.Index(text, "**Diagnosis:**"), strings.Index(text, "**Fixes:**"), strings.Index(text, "**Test counts:**"), strings.Index(text, "**Deploy transcript:**")
	if !(di < fi && fi < te && te < de) {
		t.Errorf("section order wrong (di=%d fi=%d te=%d de=%d):\n%s", di, fi, te, de, text)
	}
}

func TestBuildDefaultOmitsEmptyAndHeadingVariants(t *testing.T) {
	v := verify.New(nil, "", "")
	entry := Build(Options{Date: "2026-11-02", Subject: "s"}, v)
	text := strings.Join(entry, "\n")
	if !strings.HasPrefix(text, "### 2026-11-02 — s\n") {
		t.Fatalf("subject heading wrong:\n%s", text)
	}
	for _, label := range []string{"Diagnosis", "Fixes", "Test counts", "Deploy transcript"} {
		if strings.Contains(text, label) {
			t.Errorf("empty section %q must be omitted:\n%s", label, text)
		}
	}
	entry = Build(Options{Date: "2026-11-02"}, v)
	if !strings.HasPrefix(strings.Join(entry, "\n"), "### 2026-11-02\n") {
		t.Fatalf("bare-date heading wrong:\n%s", strings.Join(entry, "\n"))
	}
}

func TestBuildTemplate(t *testing.T) {
	gitAvailable(t)
	dir := dirtyRepo(t)
	v := verify.New([]string{dir}, dir, "")
	tmpl := "## {{date}} [{{subject}}]\n\n{{git}}\n\n- diagnosis: {{diagnosis}}\n- deploy: {{deploy}}\n"
	entry := Build(Options{
		Date:      "2026-11-02",
		Subject:   "handoff",
		Diagnosis: "drift",
		Deploy:    "none",
		Template:  tmpl,
	}, v)
	text := strings.Join(entry, "\n")
	if !strings.HasPrefix(text, "## 2026-11-02 [handoff]\n") {
		t.Fatalf("template heading wrong:\n%s", text)
	}
	if !strings.Contains(text, "- diagnosis: drift") || !strings.Contains(text, "- deploy: none") {
		t.Fatalf("template fields wrong:\n%s", text)
	}
	if !strings.Contains(text, "M src/a.rs") {
		t.Fatalf("{{git}} expansion missing:\n%s", text)
	}
	for _, ph := range []string{PhDate, PhSubject, PhDiagnosis, PhDeploy, PhGit} {
		if strings.Contains(text, ph) {
			t.Errorf("placeholder %s survived:\n%s", ph, text)
		}
	}
}

func TestBuildTemplateDateDefaultsToToday(t *testing.T) {
	v := verify.New(nil, "", "")
	entry := Build(Options{Template: "date {{date}}\n"}, v)
	if strings.Join(entry, "\n") != "date "+Today() {
		t.Fatalf("date default wrong: %q", strings.Join(entry, "\n"))
	}
}

func TestAppend(t *testing.T) {
	entry := []string{"### 2026-11-02 — x", "", "**Diagnosis:** d", ""}
	cases := []struct{ doc, want string }{
		{"# plan\n\nprose", "# plan\n\nprose\n\n### 2026-11-02 — x\n\n**Diagnosis:** d\n"},
		{"# plan\n\nprose\n", "# plan\n\nprose\n\n### 2026-11-02 — x\n\n**Diagnosis:** d\n"},
		{"# plan\n\nprose\n\n\n", "# plan\n\nprose\n\n### 2026-11-02 — x\n\n**Diagnosis:** d\n"},
		{"", "### 2026-11-02 — x\n\n**Diagnosis:** d\n"},
	}
	for _, c := range cases {
		if got := Append(c.doc, entry); got != c.want {
			t.Errorf("Append(%q) =\n%q\nwant\n%q", c.doc, got, c.want)
		}
	}
}

func TestGeneratedEntryIsAuditParseable(t *testing.T) {
	gitAvailable(t)
	dir := dirtyRepo(t)
	v := verify.New([]string{dir}, dir, "")
	head, _ := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	headHash := strings.TrimSpace(string(head))

	entry := Build(Options{
		Date:      "2026-11-02",
		Subject:   "audit shape",
		Diagnosis: "d",
		Fixes:     "f",
		Tests:     "1 test",
		Deploy:    "none",
	}, v)
	doc := Append("# plan\n\nprose\n", entry)
	claims := claim.Parse(doc)

	var tree, commit, branch int
	for _, c := range claims {
		switch c.Kind {
		case claim.KindGitTree:
			tree++
		case claim.KindCommit:
			commit++
			if c.Hash != headHash {
				t.Errorf("commit claim hash %q, want HEAD %q", c.Hash, headHash)
			}
		case claim.KindBranch:
			branch++
			if c.Name != "main" {
				t.Errorf("branch claim name %q, want main", c.Name)
			}
		}
	}
	if tree != 1 {
		t.Errorf("want 1 git-tree claim, got %d", tree)
	}
	if commit < 1 {
		t.Errorf("want a commit claim for the HEAD hash, got %d", commit)
	}
	if branch < 1 {
		t.Errorf("want a branch claim, got %d", branch)
	}

	// And the entry must audit clean: no stale/moved/missing findings.
	results := v.Verify(claims)
	for _, r := range results {
		switch r.Status {
		case verify.StatusStale, verify.StatusMoved, verify.StatusMissing:
			t.Errorf("generated entry does not audit clean: %s L%d %s %s", r.Status, r.Line, r.Kind, r.Message)
		}
	}
}

func TestBuildTemplateOmitsEmptyLabels(t *testing.T) {
	v := verify.New(nil, "", "")
	tmpl := "## {{date}}\n\n**Diagnosis:** {{diagnosis}}\n\n**Fixes:** {{fixes}}\n\nkept: plain line\n"
	entry := Build(Options{Date: "2026-11-02", Diagnosis: "d", Template: tmpl}, v)
	text := strings.Join(entry, "\n")
	if !strings.Contains(text, "**Diagnosis:** d") {
		t.Fatalf("filled label must render:\n%s", text)
	}
	if strings.Contains(text, "Fixes") {
		t.Fatalf("empty label must be dropped:\n%s", text)
	}
	if !strings.Contains(text, "kept: plain line") {
		t.Fatalf("other lines must survive:\n%s", text)
	}
	if strings.HasSuffix(text, "\n\n") || strings.Contains(text, "\n\n\n") {
		t.Fatalf("dropped labels must not leave blank runs:\n%q", text)
	}
}

func TestFindTemplate(t *testing.T) {
	gitAvailable(t)
	repo := t.TempDir()
	initRepo(t, repo)
	planDir := t.TempDir()
	write(t, filepath.Join(repo, TemplateFile), "repo template {{date}}\n")
	write(t, filepath.Join(planDir, TemplateFile), "plan template {{date}}\n")
	write(t, filepath.Join(planDir, "custom.md"), "custom template\n")

	// Explicit wins and errors on missing files.
	tmpl, src, err := FindTemplate(filepath.Join(planDir, "custom.md"), filepath.Join(planDir, "PLAN.md"), []string{repo})
	if err != nil || tmpl != "custom template\n" || src != filepath.Join(planDir, "custom.md") {
		t.Fatalf("explicit template wrong: %q %q %v", tmpl, src, err)
	}
	if _, _, err := FindTemplate(filepath.Join(planDir, "nope.md"), "", nil); err == nil {
		t.Fatal("missing explicit template must error")
	}
	// Repo template found before the plan-dir one.
	tmpl, src, err = FindTemplate("", filepath.Join(planDir, "PLAN.md"), []string{repo})
	if err != nil || tmpl != "repo template {{date}}\n" || src != filepath.Join(repo, TemplateFile) {
		t.Fatalf("repo discovery wrong: %q %q %v", tmpl, src, err)
	}
	// Plan dir used when the repo has none.
	empty := t.TempDir()
	initRepo(t, empty)
	tmpl, src, err = FindTemplate("", filepath.Join(planDir, "PLAN.md"), []string{empty})
	if err != nil || tmpl != "plan template {{date}}\n" || src != filepath.Join(planDir, TemplateFile) {
		t.Fatalf("plan-dir discovery wrong: %q %q %v", tmpl, src, err)
	}
	// Nothing anywhere -> built-in default.
	tmpl, src, err = FindTemplate("", filepath.Join(t.TempDir(), "PLAN.md"), []string{empty})
	if err != nil || tmpl != "" || src != "" {
		t.Fatalf("no-template case wrong: %q %q %v", tmpl, src, err)
	}
}

func TestRepoHintsParsesGeneratedHeader(t *testing.T) {
	gitAvailable(t)
	dir := dirtyRepo(t)
	v := verify.New([]string{dir}, dir, "")
	doc := Append("", Build(Options{Date: "2026-11-02", Subject: "s"}, v))
	hints := claim.RepoHints(doc)
	if len(hints) == 0 || !strings.HasPrefix(hints[0], dir) && hints[0] != dir {
		t.Fatalf("RepoHints did not pick up the generated **Repo:** header: %v", hints)
	}
}
