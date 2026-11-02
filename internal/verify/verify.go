// Package verify checks parsed plan claims against repository state.
package verify

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"bytewheel/plansync/internal/claim"
)

type Status string

const (
	StatusOK      Status = "ok"
	StatusStale   Status = "stale"
	StatusMoved   Status = "moved"
	StatusMissing Status = "missing"
	StatusUnknown Status = "unknown"
	StatusInfo    Status = "info"
)

type Result struct {
	Kind       claim.Kind `json:"kind"`
	Status     Status     `json:"status"`
	Line       int        `json:"line"`
	Path       string     `json:"path,omitempty"`
	Repo       string     `json:"repo,omitempty"`
	Message    string     `json:"message"`
	Suggestion string     `json:"suggestion,omitempty"`
}

type Verifier struct {
	repos       []string
	planDir     string
	docLines    []string
	statusCache map[string]map[string]string
	headCache   map[string]string
	logCache    map[string]string
	indexCache  map[string]map[string]string
}

func New(repos []string, planDir string, doc string) *Verifier {
	return &Verifier{
		repos:       repos,
		planDir:     planDir,
		docLines:    strings.Split(doc, "\n"),
		statusCache: map[string]map[string]string{},
		headCache:   map[string]string{},
		logCache:    map[string]string{},
	}
}

func (v *Verifier) Repos() []string { return v.repos }

func DetectRepos(flagged []string, planPath string, hints []string) []string {
	var repos []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" {
			return
		}
		root := gitRoot(expandHome(dir))
		if root == "" || seen[root] {
			return
		}
		seen[root] = true
		repos = append(repos, root)
	}
	for _, f := range flagged {
		add(f)
	}
	for _, h := range hints {
		add(h)
	}
	if planPath != "" {
		add(filepath.Dir(planPath))
	}
	return repos
}

func gitRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return ""
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

func (v *Verifier) Verify(claims []claim.Claim) []Result {
	var results []Result
	for _, c := range claims {
		switch c.Kind {
		case claim.KindGitTree:
			results = append(results, v.verifyGitTree(c))
		case claim.KindAnchor:
			results = append(results, v.verifyAnchor(c))
		case claim.KindPath:
			results = append(results, v.verifyPath(c))
		case claim.KindCommit:
			results = append(results, v.verifyCommit(c))
		case claim.KindBranch:
			results = append(results, v.verifyBranch(c))
		case claim.KindCheckbox:
			msg := "checkbox open"
			if c.Checked {
				msg = "checkbox done"
			}
			results = append(results, Result{Kind: c.Kind, Status: StatusInfo, Line: c.Line, Message: msg})
		case claim.KindCount:
			results = append(results, Result{Kind: c.Kind, Status: StatusInfo, Line: c.Line, Message: "numeric claim not verified: " + c.Detail})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Line != results[j].Line {
			return results[i].Line < results[j].Line
		}
		return results[i].Kind < results[j].Kind
	})
	return results
}

func (v *Verifier) verifyGitTree(c claim.Claim) Result {
	counts := map[string]int{}
	for _, e := range c.Entries {
		if res, ok := v.resolveFile(e.Path); ok {
			counts[res.repo]++
		}
	}
	repo := ""
	best := 0
	for r, n := range counts {
		if n > best {
			best, repo = n, r
		}
	}
	if repo == "" {
		return Result{Kind: c.Kind, Status: StatusUnknown, Line: c.Line,
			Message: fmt.Sprintf("git-status block with %d entries; no candidate repo contains the claimed paths", len(c.Entries))}
	}
	actual := v.gitStatus(repo)
	claimedSet := map[string]bool{}
	var match, changed, gone []string
	for _, e := range c.Entries {
		claimedSet[e.Path] = true
		if axy, ok := actual[e.Path]; ok {
			if axy == e.XY {
				match = append(match, e.Path)
			} else {
				changed = append(changed, fmt.Sprintf("%s claimed %q now %q", e.Path, e.XY, axy))
			}
		} else {
			gone = append(gone, e.Path)
		}
	}
	var extra []string
	for p := range actual {
		if !claimedSet[p] {
			extra = append(extra, p)
		}
	}
	sort.Strings(gone)
	sort.Strings(extra)
	head := v.headShort(repo)
	if len(changed) == 0 && len(gone) == 0 && len(extra) == 0 {
		return Result{Kind: c.Kind, Status: StatusOK, Line: c.Line, Repo: repo,
			Message: fmt.Sprintf("working tree matches all %d claimed entries (HEAD %s)", len(match), head)}
	}
	var parts []string
	if len(gone) > 0 {
		if len(actual) == 0 {
			parts = append(parts, fmt.Sprintf("working tree is CLEAN (HEAD %s) but plan claims %d dirty entries", head, len(gone)))
		} else {
			parts = append(parts, fmt.Sprintf("%d claimed entries no longer in working tree", len(gone)))
		}
		byCommit := map[string][]string{}
		var order []string
		for _, g := range gone {
			lc := v.lastCommit(repo, g)
			if _, ok := byCommit[lc]; !ok {
				order = append(order, lc)
			}
			byCommit[lc] = append(byCommit[lc], g)
		}
		for _, lc := range order {
			paths := byCommit[lc]
			list := strings.Join(paths, ", ")
			if len(list) > 60 {
				list = list[:60] + "..."
			}
			if lc == "" {
				parts = append(parts, fmt.Sprintf("%s (no commit found)", list))
			} else {
				parts = append(parts, fmt.Sprintf("%s landed in %s", list, lc))
			}
		}
	}
	parts = append(parts, changed...)
	if len(extra) > 0 {
		list := strings.Join(extra, ", ")
		if len(list) > 120 {
			list = list[:120] + "..."
		}
		parts = append(parts, fmt.Sprintf("%d unreported change(s): %s", len(extra), list))
	}
	return Result{Kind: c.Kind, Status: StatusStale, Line: c.Line, Repo: repo,
		Message: strings.Join(parts, "; ")}
}

func (v *Verifier) verifyAnchor(c claim.Claim) Result {
	res, ok := v.resolveFile(c.Path)
	base := Result{Kind: c.Kind, Line: c.Line, Path: c.Path, Repo: res.repo}
	if !ok {
		base.Status = StatusUnknown
		base.Message = "file not found in any candidate repo"
		return base
	}
	data, err := os.ReadFile(res.full)
	if err != nil {
		base.Status = StatusUnknown
		base.Message = "unreadable: " + err.Error()
		return base
	}
	display := c.Path
	if res.suffix {
		if rel, err := filepath.Rel(res.repo, res.full); err == nil {
			display = rel
		}
	}
	lines := strings.Split(string(data), "\n")
	n := len(lines)
	start, end := c.Start, c.End
	if end < start {
		start, end = end, start
	}
	if len(c.Symbols) == 0 || len(data) > 8<<20 {
		if start <= n {
			base.Status = StatusOK
			base.Message = fmt.Sprintf("line within file bounds (%d lines; no symbol context to verify)", n)
			return base
		}
		base.Status = StatusStale
		base.Message = fmt.Sprintf("line %d beyond end of file (file has %d lines)", c.Start, n)
		return base
	}
	lo, hi := start-1, end+1
	if lo < 1 {
		lo = 1
	}
	if hi > n {
		hi = n
	}
	for _, s := range c.Symbols {
		m := symMatcher(s)
		for i := lo; i <= hi; i++ {
			if m(lines[i-1]) {
				base.Status = StatusOK
				base.Message = fmt.Sprintf("%s present at claimed line%s", s, plural(start, end))
				return base
			}
		}
	}
	type cand struct {
		sym   string
		count int
		first int
	}
	var cands []cand
	for _, s := range c.Symbols {
		m := symMatcher(s)
		cnt, first := 0, 0
		for i, l := range lines {
			if m(l) {
				if cnt == 0 {
					first = i + 1
				}
				cnt++
				if cnt > 200 {
					break
				}
			}
		}
		if cnt > 0 {
			cands = append(cands, cand{s, cnt, first})
		}
	}
	if len(cands) == 0 {
		base.Status = StatusStale
		base.Message = fmt.Sprintf("claimed symbols %s not found in file (code moved or removed)", strings.Join(c.Symbols, ", "))
		return base
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].count < cands[j].count })
	best := cands[0]
	base.Status = StatusMoved
	base.Message = fmt.Sprintf("%s not at :%d; nearest occurrence of %s at :%d", display, c.Start, best.sym, best.first)
	base.Suggestion = fmt.Sprintf("%s:%d", display, best.first)
	return base
}

func plural(start, end int) string {
	if end != start {
		return fmt.Sprintf("s :%d-%d", start, end)
	}
	return fmt.Sprintf(" :%d", start)
}

func symMatcher(s string) func(string) bool {
	if strings.Contains(s, "::") {
		return func(l string) bool { return strings.Contains(l, s) }
	}
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(s) + `\b`)
	if err != nil {
		return func(l string) bool { return strings.Contains(l, s) }
	}
	return re.MatchString
}

func (v *Verifier) verifyPath(c claim.Claim) Result {
	base := Result{Kind: c.Kind, Line: c.Line, Path: c.Path}
	res, ok := v.resolveAny(c.Path)
	if !ok {
		base.Status = StatusUnknown
		switch {
		case isSystemPath(c.Path):
			base.Message = c.Path + ": system/temp path, likely on the target host; not checked"
		case !strings.Contains(strings.TrimPrefix(c.Path, "~/"), "/"):
			base.Message = c.Path + ": bare filename, not found at any candidate repo root"
		default:
			base.Status = StatusMissing
			base.Message = c.Path + ": not found in any candidate repo or as a literal path"
		}
		return base
	}
	base.Status = StatusOK
	if res.suffix {
		if rel, err := filepath.Rel(res.repo, res.full); err == nil {
			base.Message = "exists at " + rel + " (suffix match)"
			return base
		}
	}
	where := res.repo
	if where == "" {
		where = res.full
	}
	base.Repo = res.repo
	base.Message = "exists (" + where + ")"
	return base
}

var systemPrefixes = []string{"/tmp/", "/var/", "/usr/", "/etc/", "/opt/", "/proc/", "/sys/", "/dev/", "/run/", "/media/", "/mnt/", "/sbin/", "/bin/", "/lib/"}

func isSystemPath(p string) bool {
	for _, pre := range systemPrefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func (v *Verifier) verifyCommit(c claim.Claim) Result {
	base := Result{Kind: c.Kind, Line: c.Line, Message: c.Hash}
	for _, r := range v.repos {
		out, err := exec.Command("git", "-C", r, "cat-file", "-t", c.Hash).Output()
		if err != nil || strings.TrimSpace(string(out)) == "" {
			continue
		}
		head, _ := exec.Command("git", "-C", r, "rev-parse", "HEAD").Output()
		headFull := strings.TrimSpace(string(head))
		if strings.HasPrefix(headFull, c.Hash) || strings.HasPrefix(c.Hash, headFull) {
			base.Status = StatusOK
			base.Repo = r
			base.Message = "is HEAD of " + r
			return base
		}
		base.Repo = r
		if v.headContext(c.Line) {
			base.Status = StatusStale
			base.Message = fmt.Sprintf("claimed as head; HEAD of %s is now %s", r, v.headShort(r))
			return base
		}
		base.Status = StatusInfo
		base.Message = fmt.Sprintf("exists in %s (HEAD is now %s)", r, v.headShort(r))
		return base
	}
	base.Status = StatusUnknown
	base.Message = "commit not found in candidate repos"
	return base
}

func (v *Verifier) headContext(line int) bool {
	lo := line - 4
	if lo < 1 {
		lo = 1
	}
	hi := line
	if hi > len(v.docLines) {
		hi = len(v.docLines)
	}
	for i := lo; i <= hi; i++ {
		if strings.Contains(strings.ToLower(v.docLines[i-1]), "head") {
			return true
		}
	}
	return false
}

func (v *Verifier) verifyBranch(c claim.Claim) Result {
	base := Result{Kind: c.Kind, Line: c.Line, Message: c.Name}
	for _, r := range v.repos {
		if err := exec.Command("git", "-C", r, "rev-parse", "--verify", "--quiet", "refs/heads/"+c.Name).Run(); err == nil {
			base.Status = StatusOK
			base.Repo = r
			base.Message = "branch exists in " + r
			return base
		}
	}
	base.Status = StatusUnknown
	base.Message = "branch not found in candidate repos"
	return base
}

type resolution struct {
	full   string
	repo   string
	suffix bool
}

func (v *Verifier) resolveFile(p string) (resolution, bool) {
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "/") {
		full := expandHome(p)
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			return resolution{full: full}, true
		}
		return resolution{}, false
	}
	for _, r := range v.repos {
		full := filepath.Join(r, p)
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			return resolution{full: full, repo: r}, true
		}
	}
	full := filepath.Join(v.planDir, p)
	if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
		return resolution{full: full, repo: v.planDir}, true
	}
	if res, ok := v.suffixLookup(p); ok {
		return res, true
	}
	return resolution{}, false
}

func (v *Verifier) resolveAny(p string) (resolution, bool) {
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "/") {
		full := expandHome(p)
		if _, err := os.Stat(full); err == nil {
			return resolution{full: full}, true
		}
		return resolution{}, false
	}
	for _, r := range v.repos {
		full := filepath.Join(r, p)
		if _, err := os.Stat(full); err == nil {
			return resolution{full: full, repo: r}, true
		}
	}
	full := filepath.Join(v.planDir, p)
	if _, err := os.Stat(full); err == nil {
		return resolution{full: full, repo: v.planDir}, true
	}
	return v.suffixLookup(p)
}

var indexSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "target": true, ".cache": true,
	"vendor": true, "moz-src": true, "dictionaries": true,
}

func (v *Verifier) suffixLookup(p string) (resolution, bool) {
	if !strings.Contains(p, "/") {
		return resolution{}, false
	}
	for _, r := range v.repos {
		idx := v.repoIndex(r)
		bestRel := ""
		for rel := range idx {
			if rel == p || strings.HasSuffix(rel, "/"+p) {
				if bestRel == "" || len(rel) < len(bestRel) {
					bestRel = rel
				}
			}
		}
		if bestRel != "" {
			return resolution{full: idx[bestRel], repo: r, suffix: true}, true
		}
	}
	return resolution{}, false
}

func (v *Verifier) repoIndex(root string) map[string]string {
	if v.indexCache == nil {
		v.indexCache = map[string]map[string]string{}
	}
	if idx, ok := v.indexCache[root]; ok {
		return idx
	}
	idx := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (indexSkipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if _, ok := idx[rel]; !ok {
			idx[rel] = path
		}
		return nil
	})
	v.indexCache[root] = idx
	return idx
}

func (v *Verifier) gitStatus(root string) map[string]string {
	if m, ok := v.statusCache[root]; ok {
		return m
	}
	m := map[string]string{}
	out, err := exec.Command("git", "-C", root, "status", "--porcelain=v1", "-z").Output()
	if err != nil {
		v.statusCache[root] = m
		return m
	}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 || f[2] != ' ' || !validXY(f[:2]) {
			continue
		}
		xy, path := f[:2], f[3:]
		if path == "" {
			continue
		}
		m[path] = xy
		if (xy[0] == 'R' || xy[0] == 'C') && i+1 < len(fields) && fields[i+1] != "" {
			m[fields[i+1]] = xy
			i++
		}
	}
	v.statusCache[root] = m
	return m
}

func validXY(xy string) bool {
	if len(xy) != 2 {
		return false
	}
	x, y := xy[0], xy[1]
	if strings.IndexByte("MADRCU?!", x) < 0 {
		return false
	}
	return y == ' ' || strings.IndexByte("MDAUC?", y) >= 0
}

func (v *Verifier) headShort(root string) string {
	if s, ok := v.headCache[root]; ok {
		return s
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	s := strings.TrimSpace(string(out))
	if err != nil {
		s = "?"
	}
	v.headCache[root] = s
	return s
}

func (v *Verifier) lastCommit(root, path string) string {
	key := root + "\x00" + path
	if s, ok := v.logCache[key]; ok {
		return s
	}
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%h %s", "--", path).Output()
	s := strings.TrimSpace(string(out))
	if err != nil {
		s = ""
	}
	if len(s) > 100 {
		s = s[:100] + "..."
	}
	v.logCache[key] = s
	return s
}

func (v *Verifier) CommitsSince(date string) map[string]int {
	out := map[string]int{}
	for _, r := range v.repos {
		o, err := exec.Command("git", "-C", r, "rev-list", "--count", "HEAD", "--since="+date).Output()
		if err != nil {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(string(o))); err == nil {
			out[r] = n
		}
	}
	return out
}
