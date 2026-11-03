// Command plansync audits plan documentation against the state of the code
// repositories it describes, and syncs tool-owned plan regions back to
// current repository state.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"bytewheel/plansync/internal/brief"
	"bytewheel/plansync/internal/claim"
	"bytewheel/plansync/internal/doclog"
	"bytewheel/plansync/internal/gates"
	"bytewheel/plansync/internal/report"
	"bytewheel/plansync/internal/scaffold"
	"bytewheel/plansync/internal/syncer"
	"bytewheel/plansync/internal/verify"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `plansync - audit and sync plan documentation against repository state

Usage:
  plansync audit [plans...] [flags]     report claims that have gone stale
  plansync sync  [plans...] [flags]     refresh tool-owned plan regions
  plansync brief [plans...] [flags]     session-start digest: findings +
                                        remaining work + open checkboxes
  plansync log   [plan] [flags]         append a dated doc-log entry built
                                        from flags + current git state
  plansync init  <plan.md> [flags]      scaffold a continuation plan with
                                        live git state + marker regions

Arguments are plan files or directories (default: .). Directories are walked
for markdown files whose names look like plans (plan, roadmap, todo,
continuation, next_agent). Repositories are discovered from the plan's own
**Repo:** / **Working tree:** headers, handoff tables, and the plan's git
worktree; --repo adds more.

Flags:
  --repo path       candidate repository (repeatable)
  --json            machine-readable output
  --verbose         audit: also print ok claims individually
  --fail            audit: exit non-zero on stale/moved/missing findings
  --fail-on list    audit: statuses that trigger --fail, comma-separated
                    (default stale,moved,missing; also moved, unknown, info,
                    ok)
  --gates           audit: run .plansync.toml gate commands and verify the
                    plan's numeric claims against their output
  --gate-config p   audit: explicit gate config path (default: .plansync.toml
                    from a candidate repo or the plan dir)
  --color when      audit: auto (default) | always | never
  --policy p        sync: markers (default) | heuristic | report
  --apply           sync/log/init: write changes (default: preview only)
  --no-backup       sync/log: skip the .bak backup when applying
  --max-lines n     brief: cap the digest at n lines (default 40)
  --date d          log: entry date, YYYY-MM-DD (default today)
  --subject s       log: entry subject for the heading
  --diagnosis s     log: diagnosis text
  --fixes s         log: fixes text
  --tests s         log: test-count text
  --deploy s        log: deploy transcript text
  --template path   log: template file (default: .plansync/log-template.md
                    from a candidate repo or the plan dir; built-in otherwise)
  --title s         init: plan title (default: derived from the file name)

Sync policies:
  markers     rewrite plansync marker regions and re-anchor moved
              file:line anchors (symbol-verified) only
  heuristic   markers, plus refreshing the most recent unmarked
              git-status block per repo and stale head-table commits
  report      compute the heuristic edit set but never write
`)
}

type opts struct {
	repos      []string
	json       bool
	verbose    bool
	fail       bool
	policy     string
	apply      bool
	noBackup   bool
	maxLines   int
	date       string
	subject    string
	diagnosis  string
	fixes      string
	tests      string
	deploy     string
	template   string
	title      string
	gatesOn    bool
	gateConfig string
	color      string
	failOn     string
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
	if len(pos) == 0 {
		usage()
		return fmt.Errorf("missing subcommand")
	}
	sub, targets := pos[0], pos[1:]
	switch sub {
	case "audit", "sync", "brief", "log", "init":
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", sub)
	}
	// init targets a file that does not exist yet; it bypasses collectPlans.
	if sub == "init" {
		if len(targets) != 1 {
			return fmt.Errorf("init scaffolds exactly one plan file (got %d)", len(targets))
		}
		return runInit(targets[0], o)
	}
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
	if sub == "audit" {
		return runAudit(plans, o)
	}
	if sub == "brief" {
		return runBrief(plans, o)
	}
	if sub == "log" {
		return runLog(plans, o)
	}
	return runSync(plans, o)
}

type loadedPlan struct {
	doc    string
	claims []claim.Claim
	repos  []string
	v      *verify.Verifier
	abs    string
}

func loadPlan(path string, repoFlags []string) (loadedPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return loadedPlan{}, err
	}
	doc := string(data)
	claims := claim.Parse(doc)
	repos := verify.DetectRepos(repoFlags, path, claim.RepoHints(doc))
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return loadedPlan{
		doc:    doc,
		claims: claims,
		repos:  repos,
		v:      verify.New(repos, filepath.Dir(abs), doc),
		abs:    abs,
	}, nil
}

func parseFailOn(s string) (map[verify.Status]bool, error) {
	if s == "" {
		s = "stale,moved,missing"
	}
	set := map[verify.Status]bool{}
	for _, part := range strings.Split(s, ",") {
		switch st := verify.Status(strings.TrimSpace(part)); st {
		case verify.StatusOK, verify.StatusStale, verify.StatusMoved, verify.StatusMissing, verify.StatusUnknown, verify.StatusInfo:
			set[st] = true
		default:
			return nil, fmt.Errorf("unknown status %q in --fail-on (want ok, stale, moved, missing, unknown, info)", part)
		}
	}
	return set, nil
}

func runAudit(plans []string, o opts) error {
	failSet, err := parseFailOn(o.failOn)
	if err != nil {
		return err
	}
	color, err := parseColor(o.color)
	if err != nil {
		return err
	}
	var reps []report.Report
	anyBad := false
	for _, p := range plans {
		lp, err := loadPlan(p, o.repos)
		if err != nil {
			return err
		}
		results := lp.v.Verify(lp.claims)
		ageNote := ageNote(lp.doc, lp.v)
		var gateRuns []gates.Run
		if o.gatesOn {
			cfgPath, ok, err := gates.Find(o.gateConfig, p, lp.repos)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "warning: --gates given but no %s found for %s\n", gates.ConfigFile, p)
			} else {
				gs, err := gates.Load(cfgPath)
				if err != nil {
					return err
				}
				gateRuns = gates.RunAll(gs, filepath.Dir(cfgPath))
				results = gates.VerifyCounts(results, gateRuns)
			}
		}
		bad := false
		for _, r := range results {
			if failSet[r.Status] {
				bad = true
			}
		}
		// A failed gate contradicts "all gates green" claims: it fails under
		// the stale umbrella.
		if failSet[verify.StatusStale] {
			for _, g := range gateRuns {
				if g.Failed {
					bad = true
				}
			}
		}
		reps = append(reps, report.Build(p, lp.repos, results, ageNote, gateRuns))
		anyBad = anyBad || bad
	}
	if o.json {
		if err := report.JSON(reps, os.Stdout); err != nil {
			return err
		}
	} else {
		report.Render(reps, o.verbose, color, os.Stdout)
	}
	if o.fail && anyBad {
		os.Exit(1)
	}
	return nil
}

func parseColor(s string) (bool, error) {
	switch s {
	case "", "auto":
		fi, err := os.Stdout.Stat()
		return err == nil && fi.Mode()&os.ModeCharDevice != 0, nil
	case "always":
		return true, nil
	case "never":
		return false, nil
	}
	return false, fmt.Errorf("unknown --color %q (want auto, always, or never)", s)
}

func runBrief(plans []string, o opts) error {
	var digests []brief.Brief
	for _, p := range plans {
		lp, err := loadPlan(p, o.repos)
		if err != nil {
			return err
		}
		results := lp.v.Verify(lp.claims)
		digests = append(digests, brief.Build(p, lp.repos, lp.doc, lp.claims, results, ageNote(lp.doc, lp.v)))
	}
	if o.json {
		return brief.JSON(digests, os.Stdout)
	}
	brief.Render(os.Stdout, digests, o.maxLines)
	return nil
}

func runInit(path string, o opts) error {
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return fmt.Errorf("%s already exists; plansync init refuses to overwrite", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	repos := verify.DetectRepos(o.repos, abs, nil)
	v := verify.New(repos, filepath.Dir(abs), "")
	if len(repos) == 0 {
		fmt.Fprintln(os.Stderr, "warning: no git repository found; scaffold will carry TODO placeholders (pass --repo)")
	}
	title := o.title
	if title == "" {
		title = scaffold.DefaultTitle(path)
	}
	entry := scaffold.Build(scaffold.Options{Title: title, Date: o.date}, v)
	type initOut struct {
		File    string   `json:"file"`
		Title   string   `json:"title"`
		Repos   []string `json:"repos,omitempty"`
		Lines   []string `json:"lines"`
		Applied bool     `json:"applied"`
	}
	out := initOut{File: path, Title: title, Repos: repos, Lines: entry}
	if o.json {
		defer func() {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(out)
		}()
	} else {
		fmt.Printf("== %s\n", path)
		for _, l := range entry {
			fmt.Println(l)
		}
	}
	if !o.apply {
		if !o.json {
			fmt.Println("\n   preview only; --apply writes (init refuses to overwrite)")
		}
		return nil
	}
	if err := scaffold.Write(path, entry); err != nil {
		return err
	}
	out.Applied = true
	if !o.json {
		fmt.Printf("   created %s (%d line(s))\n", path, len(entry))
	}
	// Demonstrate the contract: a fresh scaffold audits clean and is a sync
	// no-op.
	doc := strings.Join(entry, "\n")
	claims := claim.Parse(doc)
	nv := verify.New(repos, filepath.Dir(abs), doc)
	results := nv.Verify(claims)
	bad := 0
	for _, r := range results {
		switch r.Status {
		case verify.StatusStale, verify.StatusMoved, verify.StatusMissing:
			bad++
		}
	}
	edits, _ := syncer.Compute(strings.Split(doc, "\n"), nv, claims, results, syncer.PolicyMarkers)
	if !o.json {
		fmt.Printf("   audit: %d finding(s); sync: %d edit(s) — a fresh scaffold should be 0/0\n", bad, len(edits))
	}
	return nil
}

func runLog(plans []string, o opts) error {
	if len(plans) != 1 {
		return fmt.Errorf("log appends to exactly one plan (got %d)", len(plans))
	}
	p := plans[0]
	lp, err := loadPlan(p, o.repos)
	if err != nil {
		return err
	}
	if len(lp.repos) == 0 {
		fmt.Fprintln(os.Stderr, "warning: no candidate repos discovered; entry will carry no git state")
	}
	tmpl, src, err := doclog.FindTemplate(o.template, lp.abs, lp.repos)
	if err != nil {
		return err
	}
	entry := doclog.Build(doclog.Options{
		Date:      o.date,
		Subject:   o.subject,
		Diagnosis: o.diagnosis,
		Fixes:     o.fixes,
		Tests:     o.tests,
		Deploy:    o.deploy,
		Template:  tmpl,
	}, lp.v)
	newDoc := doclog.Append(lp.doc, entry)
	type logOut struct {
		File     string   `json:"file"`
		Template string   `json:"template,omitempty"`
		Entry    []string `json:"entry"`
		Applied  bool     `json:"applied"`
	}
	out := logOut{File: p, Template: src, Entry: entry}
	if o.json {
		defer func() {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(out)
		}()
	} else {
		fmt.Printf("== %s\n", p)
		if src != "" {
			fmt.Printf("   template: %s\n", src)
		}
		for _, l := range entry {
			fmt.Println(l)
		}
	}
	if !o.apply {
		if !o.json {
			fmt.Println("\n   preview only; --apply writes (with a .bak backup unless --no-backup)")
		}
		return nil
	}
	if !o.noBackup {
		if err := os.WriteFile(p+".bak", []byte(lp.doc), 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(p, []byte(newDoc), 0o644); err != nil {
		return err
	}
	out.Applied = true
	if !o.json {
		fmt.Printf("   appended %d line(s) to %s; backup at %s.bak\n", len(entry), p, p)
	}
	// Post-apply re-audit, same contract as sync: report what audit still
	// finds (historical sections of the plan may legitimately be stale).
	nv := verify.New(lp.repos, filepath.Dir(lp.abs), newDoc)
	bad := 0
	for _, r := range nv.Verify(claim.Parse(newDoc)) {
		switch r.Status {
		case verify.StatusStale, verify.StatusMoved, verify.StatusMissing:
			bad++
		}
	}
	if !o.json {
		fmt.Printf("   re-audit: %d finding(s) (includes pre-existing plan history)\n", bad)
	}
	return nil
}

func runSync(plans []string, o opts) error {
	policy, err := syncer.ParsePolicy(o.policy)
	if err != nil {
		return err
	}
	if o.apply && policy == syncer.PolicyReport {
		return fmt.Errorf("--apply cannot be combined with --policy report")
	}
	type syncOut struct {
		File     string        `json:"file"`
		Policy   string        `json:"policy"`
		Edits    []syncer.Edit `json:"edits"`
		Warnings []string      `json:"warnings,omitempty"`
	}
	var all []syncOut
	for _, p := range plans {
		lp, err := loadPlan(p, o.repos)
		if err != nil {
			return err
		}
		lines := strings.Split(lp.doc, "\n")
		edits, warnings := syncer.Compute(lines, lp.v, lp.claims, lp.v.Verify(lp.claims), policy)
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", p, w)
		}
		all = append(all, syncOut{File: p, Policy: o.policy, Edits: edits, Warnings: warnings})
		if len(edits) == 0 {
			if !o.json {
				fmt.Printf("== %s\n   nothing to sync (%s policy)\n\n", p, o.policy)
			}
			continue
		}
		if o.json {
			continue
		}
		fmt.Print(syncer.RenderDiff(p, lines, edits))
		fmt.Printf("   %d edit(s) (%s policy)\n", len(edits), o.policy)
		if !o.apply {
			fmt.Println("   preview only; --apply writes (with a .bak backup unless --no-backup)")
			fmt.Println()
			continue
		}
		out, err := syncer.Apply(lines, edits)
		if err != nil {
			return err
		}
		if !o.noBackup {
			if err := os.WriteFile(p+".bak", []byte(lp.doc), 0o644); err != nil {
				return err
			}
		}
		newDoc := strings.Join(out, "\n")
		if err := os.WriteFile(p, []byte(newDoc), 0o644); err != nil {
			return err
		}
		nv := verify.New(lp.repos, filepath.Dir(lp.abs), newDoc)
		bad := 0
		for _, r := range nv.Verify(claim.Parse(newDoc)) {
			switch r.Status {
			case verify.StatusStale, verify.StatusMoved, verify.StatusMissing:
				bad++
			}
		}
		fmt.Printf("   applied %d edit(s); backup at %s.bak\n", len(edits), p)
		fmt.Printf("   re-audit: %d finding(s) remain that sync does not fix automatically\n\n", bad)
	}
	if o.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(all)
	}
	return nil
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
	for _, r := range v.Repos() {
		if n, ok := since[r]; ok && n > 0 {
			parts = append(parts, fmt.Sprintf("%s advanced %d commits", shortHome(r), n))
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

type flagSet struct {
	inner *flag.FlagSet
}

func newFlagSet(o *opts) *flagSet {
	fs := &flagSet{inner: flag.NewFlagSet("plansync", flag.ContinueOnError)}
	fs.inner.SetOutput(os.Stderr)
	fs.inner.Var(&stringSlice{o: &o.repos}, "repo", "")
	fs.inner.BoolVar(&o.json, "json", false, "")
	fs.inner.BoolVar(&o.verbose, "verbose", false, "")
	fs.inner.BoolVar(&o.fail, "fail", false, "")
	fs.inner.StringVar(&o.policy, "policy", "markers", "")
	fs.inner.BoolVar(&o.apply, "apply", false, "")
	fs.inner.BoolVar(&o.noBackup, "no-backup", false, "")
	fs.inner.IntVar(&o.maxLines, "max-lines", 40, "")
	fs.inner.StringVar(&o.date, "date", "", "")
	fs.inner.StringVar(&o.subject, "subject", "", "")
	fs.inner.StringVar(&o.diagnosis, "diagnosis", "", "")
	fs.inner.StringVar(&o.fixes, "fixes", "", "")
	fs.inner.StringVar(&o.tests, "tests", "", "")
	fs.inner.StringVar(&o.deploy, "deploy", "", "")
	fs.inner.StringVar(&o.template, "template", "", "")
	fs.inner.StringVar(&o.title, "title", "", "")
	fs.inner.BoolVar(&o.gatesOn, "gates", false, "")
	fs.inner.StringVar(&o.gateConfig, "gate-config", "", "")
	fs.inner.StringVar(&o.color, "color", "auto", "")
	fs.inner.StringVar(&o.failOn, "fail-on", "", "")
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
		"-policy": true, "--policy": true,
		"-max-lines": true, "--max-lines": true,
		"-date": true, "--date": true,
		"-subject": true, "--subject": true,
		"-diagnosis": true, "--diagnosis": true,
		"-fixes": true, "--fixes": true,
		"-tests": true, "--tests": true,
		"-deploy": true, "--deploy": true,
		"-template": true, "--template": true,
		"-title": true, "--title": true,
		"-gate-config": true, "--gate-config": true,
		"-color": true, "--color": true,
		"-fail-on": true, "--fail-on": true,
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
