// Package scaffold generates continuation plans pre-filled with live git
// state: repo headers, plansync marker regions (git-status, heads), a
// remaining-work section, and a notes section. A scaffolded plan audits
// clean and is a sync no-op by construction.
package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"bytewheel/plansync/internal/syncer"
	"bytewheel/plansync/internal/verify"
)

// Options carries the scaffold inputs. Title defaults to the plan file's
// base name without extension; Date defaults to today.
type Options struct {
	Title string
	Date  string
}

// Today returns the default plan date (YYYY-MM-DD).
func Today() string { return time.Now().Format("2006-01-02") }

// DefaultTitle derives a title from the plan path: base name without
// extension, underscores turned into spaces.
func DefaultTitle(planPath string) string {
	base := filepath.Base(planPath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimSuffix(base, ".md")
	base = strings.NewReplacer("_", " ", "-", " — ").Replace(base)
	return strings.Join(strings.Fields(base), " ")
}

// Build returns the scaffolded plan lines for the verifier's repos. With no
// candidate repos it still produces a usable plan with TODO placeholders.
func Build(o Options, v *verify.Verifier) []string {
	if o.Date == "" {
		o.Date = Today()
	}
	var b []string
	title := o.Title
	if title == "" {
		title = "Untitled"
	}
	b = append(b, "# Continuation plan — "+title+" ("+o.Date+")", "")

	repos := v.Repos()
	if len(repos) == 0 {
		b = append(b,
			"**Repo:** `TODO/repo/path`, branch `TODO`",
			"",
			"(plansync found no git repository: pass --repo or run inside a",
			"worktree, then fill in the header and marker regions below.)",
			"")
	} else {
		for _, r := range repos {
			label := "**Repo:** `" + shortHome(r) + "`"
			if branch := v.Branch(r); branch != "" {
				label += ", branch `" + branch + "`"
			}
			if head := v.HeadShort(r); head != "" && head != "?" {
				label += ", HEAD `" + head + "`"
			}
			b = append(b, label)
		}
		b = append(b, "")
	}

	b = append(b,
		"One-paragraph brief: what this plan continues, and the task for the",
		"next agent. State only what is established; leave the rest to the",
		"sections below.",
		"",
		"---",
		"",
		"## Status",
		"")
	for _, r := range repos {
		b = append(b, "Working tree of `"+shortHome(r)+"`:", "")
		b = append(b, markerRegion("git-status", map[string]string{"repo": shortHome(r)}, v)...)
		b = append(b, "")
	}
	if len(repos) > 0 {
		b = append(b, "Heads at plan time:", "")
		b = append(b, markerRegion("heads", nil, v)...)
		b = append(b, "")
	}

	b = append(b,
		"---",
		"",
		"## Remaining steps, in order",
		"",
		"- [ ] describe the first concrete step",
		"- [ ] …",
		"",
		"## Gotchas / landmines",
		"",
		"- Invariants, failure modes already diagnosed, and things NOT to redo.",
		"")
	return b
}

func markerRegion(kind string, args map[string]string, v *verify.Verifier) []string {
	body, warn := syncer.MarkerBody(kind, args, v)
	if warn != "" {
		return []string{"<!-- plansync:begin " + kind + " -->", "<!-- TODO: " + warn + " -->", "<!-- plansync:end -->"}
	}
	out := []string{"<!-- plansync:begin " + kind + regionArgs(args) + " -->"}
	out = append(out, body...)
	return append(out, "<!-- plansync:end -->")
}

func regionArgs(args map[string]string) string {
	if args == nil {
		return ""
	}
	var parts []string
	for k, val := range args {
		parts = append(parts, k+"="+val)
	}
	// single-arg regions only, in practice
	return " " + strings.Join(parts, " ")
}

// Write saves the plan, creating parent directories as needed. It refuses
// to overwrite an existing file.
func Write(path string, lines []string) error {
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return &ExistsError{Path: path}
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	content := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

// ExistsError reports that the target plan file already exists.
type ExistsError struct{ Path string }

func (e *ExistsError) Error() string {
	return e.Path + " already exists; plansync init refuses to overwrite"
}

func shortHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
