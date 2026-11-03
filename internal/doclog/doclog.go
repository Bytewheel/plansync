// Package doclog assembles dated doc-log entries for plan documents from
// session facts (diagnosis, fixes, test counts, deploy transcript) and
// current git state, using a built-in house-style template or a per-repo
// template file.
package doclog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"bytewheel/plansync/internal/verify"
)

// Options carries the session facts a doc-log entry records. Date defaults
// to today; empty text fields are omitted from the default template.
type Options struct {
	Date      string
	Subject   string
	Diagnosis string
	Fixes     string
	Tests     string
	Deploy    string
	Template  string // raw template; empty = built-in default
}

// Placeholders recognized in templates. {{git}} expands to the multi-line
// per-repo git state and should sit on its own line.
const (
	PhDate      = "{{date}}"
	PhSubject   = "{{subject}}"
	PhDiagnosis = "{{diagnosis}}"
	PhFixes     = "{{fixes}}"
	PhTests     = "{{tests}}"
	PhDeploy    = "{{deploy}}"
	PhGit       = "{{git}}"
)

// TemplateFile is the per-repo template location, looked up in each
// candidate repo root and then next to the plan.
const TemplateFile = ".plansync/log-template.md"

// Today returns the default entry date (YYYY-MM-DD).
func Today() string { return time.Now().Format("2006-01-02") }

// GitState renders the per-repo git state block: a `**Repo:**` header line
// with branch and HEAD, followed by a fenced porcelain dump (or a clean
// note). The shapes are audit grammar on purpose — the next `audit` (and
// `brief`) re-verifies what the entry claims.
func GitState(v *verify.Verifier) []string {
	var out []string
	for _, r := range v.Repos() {
		label := "**Repo:** `" + shortHome(r) + "`"
		if branch := v.Branch(r); branch != "" {
			label += ", branch `" + branch + "`"
		}
		if head := v.HeadShort(r); head != "" && head != "?" {
			label += ", HEAD `" + head + "`"
		}
		if pl := v.PorcelainLines(r); len(pl) > 0 {
			out = append(out, label+"; working tree:", "", "```")
			out = append(out, pl...)
			out = append(out, "```")
		} else {
			out = append(out, label+"; working tree clean.")
		}
		out = append(out, "")
	}
	return out
}

// Build returns the entry lines for the given options and git state.
func Build(o Options, v *verify.Verifier) []string {
	if o.Date == "" {
		o.Date = Today()
	}
	if o.Template != "" {
		return renderTemplate(o, v)
	}
	heading := "### " + o.Date
	if o.Subject != "" {
		heading += " — " + o.Subject
	}
	var b []string
	b = append(b, heading, "")
	b = append(b, GitState(v)...)
	for _, sec := range []struct{ label, text string }{
		{"Diagnosis", o.Diagnosis},
		{"Fixes", o.Fixes},
		{"Test counts", o.Tests},
		{"Deploy transcript", o.Deploy},
	} {
		if sec.text == "" {
			continue
		}
		b = append(b, "**"+sec.label+":** "+strings.TrimRight(sec.text, "\n"), "")
	}
	return b
}

var emptyLabelRe = regexp.MustCompile(`^\s*\*\*[^*]+:\*\*\s*$`)

func renderTemplate(o Options, v *verify.Verifier) []string {
	t := strings.ReplaceAll(o.Template, "\r\n", "\n")
	git := strings.Join(GitState(v), "\n")
	for _, r := range []struct{ ph, val string }{
		{PhGit, git},
		{PhDate, o.Date},
		{PhSubject, o.Subject},
		{PhDiagnosis, o.Diagnosis},
		{PhFixes, o.Fixes},
		{PhTests, o.Tests},
		{PhDeploy, o.Deploy},
	} {
		t = strings.ReplaceAll(t, r.ph, r.val)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(t, "\n"), "\n") {
		// A house-style label whose value substituted to empty is dropped,
		// matching the built-in template's omit-empty behaviour; a blank
		// line left stranded by the drop is collapsed.
		if emptyLabelRe.MatchString(l) {
			continue
		}
		if strings.TrimSpace(l) == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// FindTemplate returns the raw template and its source path. An explicit
// path wins (read errors propagate); otherwise TemplateFile is looked up in
// each candidate repo root, then next to the plan; "" means the built-in
// default applies.
func FindTemplate(explicit, planPath string, repos []string) (string, string, error) {
	if explicit != "" {
		data, err := os.ReadFile(explicit)
		if err != nil {
			return "", "", err
		}
		return string(data), explicit, nil
	}
	var dirs []string
	dirs = append(dirs, repos...)
	if planPath != "" {
		dirs = append(dirs, filepath.Dir(planPath))
	}
	for _, d := range dirs {
		p := filepath.Join(d, TemplateFile)
		if data, err := os.ReadFile(p); err == nil {
			return string(data), p, nil
		}
	}
	return "", "", nil
}

// Append returns doc with the entry appended as a new trailing section,
// separated by one blank line, with exactly one trailing newline.
func Append(doc string, entry []string) string {
	e := strings.TrimRight(strings.Join(entry, "\n"), "\n")
	base := strings.TrimRight(doc, "\n")
	if base == "" {
		return e + "\n"
	}
	return base + "\n\n" + e + "\n"
}

func shortHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
