// Command plansync audits plan documentation against the state of the code
// repositories it describes, reporting claims that have gone stale.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"bytewheel/plansync/internal/claim"
	"bytewheel/plansync/internal/report"
	"bytewheel/plansync/internal/verify"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `plansync - audit plan documentation against repository state

Usage:
  plansync audit [plans...] [flags]

Arguments are plan files or directories (default: .). Directories are walked
for markdown files whose names look like plans (plan, roadmap, todo,
continuation, next_agent). Repositories are discovered from the plan's own
**Repo:** / **Working tree:** headers, handoff tables, and the plan's git
worktree; --repo adds more.

Flags:
  --repo path   candidate repository (repeatable)
  --json        machine-readable output
  --verbose     also print ok claims individually
  --fail        exit non-zero when any stale/moved/missing claim is found
`)
}

type opts struct {
	repos   []string
	json    bool
	verbose bool
	fail    bool
}

func run(args []string) error {
	flags, pos, err := splitFlags(args)
	if err != nil {
		return err
	}
	var o opts
	fs := newFlagSet(&o)
	if err := fs.Parse(flags); err != nil {
		usage()
		return err
	}
	if len(pos) == 0 || pos[0] != "audit" {
		usage()
		return fmt.Errorf("missing audit subcommand")
	}
	targets := pos[1:]
	if len(targets) == 0 {
		targets = []string{"."}
	}
	var plans []string
	for _, t := range targets {
		ps, err := collectPlans(t)
		if err != nil {
			return err
		}
		plans = append(plans, ps...)
	}
	if len(plans) == 0 {
		return fmt.Errorf("no plan files found")
	}
	var reps []report.Report
	anyBad := false
	for _, p := range plans {
		rep, bad, err := audit(p, o)
		if err != nil {
			return err
		}
		reps = append(reps, rep)
		anyBad = anyBad || bad
	}
	if o.json {
		if err := report.JSON(reps, os.Stdout); err != nil {
			return err
		}
	} else {
		report.Render(reps, o.verbose, os.Stdout)
	}
	if o.fail && anyBad {
		os.Exit(1)
	}
	return nil
}

func audit(path string, o opts) (report.Report, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report.Report{}, false, err
	}
	doc := string(data)
	claims := claim.Parse(doc)
	repos := verify.DetectRepos(o.repos, path, claim.RepoHints(doc))
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	v := verify.New(repos, filepath.Dir(abs), doc)
	results := v.Verify(claims)
	ageNote := ageNote(doc, v)
	bad := false
	for _, r := range results {
		switch r.Status {
		case verify.StatusStale, verify.StatusMoved, verify.StatusMissing:
			bad = true
		}
	}
	return report.Build(path, repos, results, ageNote), bad, nil
}

var dateRe = regexp.MustCompile(`20\d{2}-\d{2}-\d{2}`)

func ageNote(doc string, v *verify.Verifier) string {
	head := doc
	if len(head) > 4000 {
		head = head[:4000]
	}
	d := dateRe.FindString(head)
	if d == "" {
		return ""
	}
	since := v.CommitsSince(d)
	if len(since) == 0 {
		return "plan dated " + d
	}
	var parts []string
	total := 0
	for _, r := range v.Repos() {
		if n, ok := since[r]; ok && n > 0 {
			parts = append(parts, fmt.Sprintf("%s advanced %d commits", shortHome(r), n))
			total += n
		}
	}
	if len(parts) == 0 {
		return "plan dated " + d + "; no repo advanced since"
	}
	return "plan dated " + d + "; " + strings.Join(parts, "; ")
}

func shortHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

var planNameRe = regexp.MustCompile(`(?i)(plan|roadmap|todo|continuation|next_agent)`)

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "target": true, ".ruff_cache": true,
	"__pycache__": true, ".cache": true, "vendor": true, "moz-src": true,
	"dictionaries": true, "localization": true, "hyphenation": true,
	"gradle": true, "build": true, "build-debug": true, "build-default": true,
	"build-min": true, "Testing": true, "out": true, "backups": true,
	".hg": true, ".svn": true,
}

func collectPlans(target string) ([]string, error) {
	fi, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return []string{target}, nil
	}
	var plans []string
	err = filepath.WalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != target && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if depth(target, path) >= 4 {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		if planNameRe.MatchString(name) {
			plans = append(plans, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return plans, nil
}

func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 0
	}
	if rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

func newFlagSet(o *opts) *flagSet {
	return newFlagSetInner(o)
}

type flagSet struct {
	inner *flag.FlagSet
}

func newFlagSetInner(o *opts) *flagSet {
	fs := &flagSet{inner: flag.NewFlagSet("plansync", flag.ContinueOnError)}
	fs.inner.SetOutput(os.Stderr)
	fs.inner.Var(&stringSlice{o: &o.repos}, "repo", "")
	fs.inner.BoolVar(&o.json, "json", false, "")
	fs.inner.BoolVar(&o.verbose, "verbose", false, "")
	fs.inner.BoolVar(&o.fail, "fail", false, "")
	return fs
}

func (f *flagSet) Parse(args []string) error { return f.inner.Parse(args) }

type stringSlice struct {
	o *[]string
}

func (s *stringSlice) String() string { return strings.Join(*s.o, ",") }

func (s *stringSlice) Set(v string) error {
	*s.o = append(*s.o, v)
	return nil
}

func splitFlags(args []string) (flags, pos []string, err error) {
	valueFlags := map[string]bool{
		"-repo": true, "--repo": true,
	}
	i := 0
	for i < len(args) {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			if strings.Contains(a, "=") {
				flags = append(flags, a)
				i++
				continue
			}
			flags = append(flags, a)
			if valueFlags[a] {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("flag %s requires a value", a)
				}
				flags = append(flags, args[i+1])
				i += 2
				continue
			}
			i++
			continue
		}
		pos = append(pos, a)
		i++
	}
	return flags, pos, nil
}
