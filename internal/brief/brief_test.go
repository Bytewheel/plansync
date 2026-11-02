package brief

import (
	"bytes"
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

func TestExtractSections(t *testing.T) {
	doc := `# plan

## 1. Context

Old prose.

## 2. Remaining work, in order

1. Fix the 403 on shared events.
2. Re-run gates.

### Task A — closeout

Details for task A.

## 3. What NOT to do

Avoid touching put_event.

### Open questions

- who owns the mirror?

## 4. Next steps

Ship it.
`
	secs := ExtractSections(doc)
	if len(secs) != 3 {
		t.Fatalf("want 3 sections, got %d: %+v", len(secs), secs)
	}
	if secs[0].Heading != "2. Remaining work, in order" || secs[0].Line != 7 {
		t.Fatalf("section 0 wrong: %+v", secs[0])
	}
	if secs[0].End < 13 {
		t.Fatalf("section 0 should extend into the Task A subsection, End=%d", secs[0].End)
	}
	if !strings.Contains(strings.Join(secs[0].Body, "\n"), "Task A — closeout") {
		t.Fatalf("section 0 body should contain the deeper heading: %q", secs[0].Body)
	}
	if secs[1].Heading != "Open questions" || secs[1].Line != 20 {
		t.Fatalf("section 1 wrong: %+v", secs[1])
	}
	if secs[2].Heading != "4. Next steps" || secs[2].Line != 24 {
		t.Fatalf("section 2 wrong: %+v", secs[2])
	}
}

func TestExtractSectionsKeepsFencedBodiesButSkipsFencedHeadings(t *testing.T) {
	doc := "# plan\n\n## Remaining steps\n\nstart\n\n```\n## not a heading\n```\n\n### Remaining: nested\n\ninner\n\n## done\n"
	secs := ExtractSections(doc)
	if len(secs) != 1 {
		t.Fatalf("fenced and nested matches must not create sections, got %d: %+v", len(secs), secs)
	}
	if secs[0].Heading != "Remaining steps" {
		t.Fatalf("heading wrong: %+v", secs[0])
	}
	joined := strings.Join(secs[0].Body, "\n")
	if !strings.Contains(joined, "## not a heading") {
		t.Fatalf("fenced content is body material and must be kept: %q", joined)
	}
	if !strings.Contains(joined, "inner") {
		t.Fatalf("body should include nested subsection content: %q", joined)
	}
}

func TestExtractSectionsNone(t *testing.T) {
	for _, doc := range []string{
		"# just a title\n\nprose\n",
		"## 6. What NOT to do\n\navoid\n",
		"```\n## Remaining steps\n```\n",
	} {
		if secs := ExtractSections(doc); len(secs) != 0 {
			t.Errorf("doc %q: want 0 sections, got %+v", doc, secs)
		}
	}
}

func TestCheckboxText(t *testing.T) {
	for in, want := range map[string]string{
		"- [ ] put_event stamping": "put_event stamping",
		"  * [x] done thing":       "done thing",
		"- [ ]":                    "",
		"plain line":               "plain line",
	} {
		if got := checkboxText(in); got != want {
			t.Errorf("checkboxText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildCountsAndDedup(t *testing.T) {
	doc := "# plan\n\n## Remaining steps\n\n- [ ] inside section\n\n## other\n\n- [ ] outside section\n- [x] done one\n"
	claims := claim.Parse(doc)
	results := []verify.Result{
		{Kind: claim.KindBranch, Status: verify.StatusOK, Line: 1, Message: "ok"},
		{Kind: claim.KindGitTree, Status: verify.StatusStale, Line: 3, Message: "tree clean"},
		{Kind: claim.KindAnchor, Status: verify.StatusMoved, Line: 4, Message: "moved away", Suggestion: "a.rs:9"},
		{Kind: claim.KindPath, Status: verify.StatusMissing, Line: 5, Message: "gone"},
		{Kind: claim.KindPath, Status: verify.StatusUnknown, Line: 6, Message: "elsewhere"},
		{Kind: claim.KindPath, Status: verify.StatusUnknown, Line: 7, Message: "elsewhere2"},
	}
	b := Build("p.md", nil, doc, claims, results, "plan dated 2026-01-02")
	if len(b.Findings) != 3 {
		t.Fatalf("want 3 findings, got %+v", b.Findings)
	}
	if b.Unknowns != 2 {
		t.Fatalf("want 2 unknowns, got %d", b.Unknowns)
	}
	if b.Freshness != 0.25 {
		t.Fatalf("freshness = %f, want 0.25", b.Freshness)
	}
	if b.OpenCount != 2 || b.DoneCount != 1 {
		t.Fatalf("counts wrong: open=%d done=%d", b.OpenCount, b.DoneCount)
	}
	if len(b.Open) != 1 || b.Open[0].Text != "outside section" {
		t.Fatalf("checkbox inside section must be deduped, got %+v", b.Open)
	}
	if len(b.Sections) != 1 || b.Sections[0].Heading != "Remaining steps" {
		t.Fatalf("sections wrong: %+v", b.Sections)
	}
	if b.AgeNote != "plan dated 2026-01-02" {
		t.Fatalf("age note wrong: %q", b.AgeNote)
	}
}

func TestRenderCapAndPriority(t *testing.T) {
	doc := "# plan\n\n## Remaining steps\n\n" + strings.Repeat("step line\n", 40) + "\n- [ ] a checkbox in the section\n\n## later\n\n- [ ] outside box\n"
	claims := claim.Parse(doc)
	results := []verify.Result{
		{Kind: claim.KindGitTree, Status: verify.StatusStale, Line: 1, Message: "tree clean but plan claims dirt"},
		{Kind: claim.KindAnchor, Status: verify.StatusMoved, Line: 2, Message: "symbol moved", Suggestion: "x.rs:9"},
	}
	b := Build("plan.md", nil, doc, claims, results, "")
	var buf bytes.Buffer
	Render(&buf, []Brief{b}, 12)
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 15 {
		t.Fatalf("render exceeded cap + elision/blank/footer slack: %d lines:\n%s", len(lines), out)
	}
	if !strings.Contains(out, "== plan.md (freshness") {
		t.Fatalf("header missing:\n%s", out)
	}
	if !strings.Contains(out, "stale") || !strings.Contains(out, "moved") {
		t.Fatalf("findings must survive trimming:\n%s", out)
	}
	if !strings.Contains(out, "work    L3") {
		t.Fatalf("section header line missing:\n%s", out)
	}
	if !strings.Contains(out, "elided; raise --max-lines") {
		t.Fatalf("elision line missing:\n%s", out)
	}
	if !strings.Contains(out, "briefed 1 plan(s) in") {
		t.Fatalf("footer missing:\n%s", out)
	}
}

func TestRenderSectionBodyCap(t *testing.T) {
	doc := "## Remaining steps\n\n" + strings.Repeat("step\n", 40)
	b := Build("plan.md", nil, doc, nil, nil, "")
	var buf bytes.Buffer
	Render(&buf, []Brief{b}, 200)
	out := buf.String()
	if !strings.Contains(out, "(+20 more line(s) in plan)") {
		t.Fatalf("section body cap note missing:\n%s", out)
	}
	if n := strings.Count(out, "\n      step"); n != 20 {
		t.Fatalf("want 20 body step lines, got %d:\n%s", n, out)
	}
}

func TestRenderCleanPlan(t *testing.T) {
	doc := "# plan\n\nprose only\n"
	b := Build("plan.md", nil, doc, nil, nil, "")
	var buf bytes.Buffer
	Render(&buf, []Brief{b}, 40)
	out := buf.String()
	if !strings.Contains(out, "no stale findings, no remaining-work sections, no open checkboxes") {
		t.Fatalf("clean digest line missing:\n%s", out)
	}
}

func TestRenderBudgetSharedAcrossPlans(t *testing.T) {
	var bs []Brief
	for i := 0; i < 4; i++ {
		doc := "## Remaining steps\n\n" + strings.Repeat("step\n", 10)
		bs = append(bs, Build("p"+string(rune('1'+i))+".md", nil, doc, nil, nil, ""))
	}
	var buf bytes.Buffer
	Render(&buf, bs, 12)
	out := buf.String()
	if !strings.Contains(out, "briefed 4 plan(s) in") {
		t.Fatalf("footer missing:\n%s", out)
	}
	for i := 1; i <= 4; i++ {
		if !strings.Contains(out, "== p"+string(rune('0'+i))+".md") {
			t.Fatalf("plan p%d header missing:\n%s", i, out)
		}
	}
}

func TestEndToEndWithRepo(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, filepath.Join(dir, "src/lib.rs"), "fn helper() {}\n\npub fn normalize_rrule(x: &str) {}\n")
	run(t, "git", "-C", dir, "add", "-A")
	run(t, "git", "-C", dir, "commit", "-q", "-m", "lib")

	doc := "# plan\n\nWorking tree:\n\n```\n M src/lib.rs\n```\n\nThe wiring lives in `src/lib.rs:9` via `normalize_rrule`.\n\n## Remaining steps\n\n1. Land the fix.\n\n## other\n\n- [ ] outside box\n"
	claims := claim.Parse(doc)
	v := verify.New([]string{dir}, dir, doc)
	results := v.Verify(claims)
	b := Build("plan.md", []string{dir}, doc, claims, results, "plan dated 2026-01-01")

	if len(b.Findings) != 2 {
		t.Fatalf("want 2 findings (stale tree + moved anchor), got %+v", b.Findings)
	}
	if string(b.Findings[0].Status) != string(verify.StatusStale) || string(b.Findings[1].Status) != string(verify.StatusMoved) {
		t.Fatalf("finding statuses wrong: %+v", b.Findings)
	}
	if len(b.Sections) != 1 || b.Sections[0].Heading != "Remaining steps" {
		t.Fatalf("sections wrong: %+v", b.Sections)
	}
	if len(b.Open) != 1 || b.Open[0].Text != "outside box" {
		t.Fatalf("open checkbox dedup wrong: %+v", b.Open)
	}
	var buf bytes.Buffer
	Render(&buf, []Brief{b}, 40)
	out := buf.String()
	for _, want := range []string{
		"== plan.md (freshness",
		"stale   L6    git-tree",
		"moved   L9    anchor",
		"work    L11   section  Remaining steps",
		"      1. Land the fix.",
		"open    L17   checkbox outside box",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest missing %q:\n%s", want, out)
		}
	}
}
