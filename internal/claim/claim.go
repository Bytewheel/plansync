// Package claim parses plan documentation into machine-checkable claims
// about repository state.
package claim

import (
	"regexp"
	"strings"
)

type Kind string

const (
	KindGitTree  Kind = "git-tree"
	KindAnchor   Kind = "anchor"
	KindPath     Kind = "path"
	KindCommit   Kind = "commit"
	KindBranch   Kind = "branch"
	KindCheckbox Kind = "checkbox"
	KindCount    Kind = "count"
)

type GitEntry struct {
	XY   string
	Path string
	Note string
	Line int
}

type Claim struct {
	Kind    Kind
	Line    int
	Text    string
	Path    string
	Start   int
	End     int
	Symbols []string
	Checked bool
	Hash    string
	Name    string
	Detail  string
	Entries []GitEntry
}

var (
	gitEntryRe = regexp.MustCompile(`^ {0,3}((?:[MADRCU][MDA? ])|(?:[?!]{2})|(?: [MDA?])) ("(?:[^"\\]|\\.)*"|\S+)(.*)$`)
	anchorRe   = regexp.MustCompile(`^(\S+):(\d+)(?:-(\d+))?$`)
	elidedRe   = regexp.MustCompile(`^:(\d+)(?:-(\d+))?$`)
	commitRe   = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	identRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_:]{3,}$`)
	checkboxRe = regexp.MustCompile(`^\s{0,3}[-*]\s+\[([ xX])\]`)
	ipv4Re     = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)
	cidrRe     = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}/\d{1,2}$`)
	versionRe  = regexp.MustCompile(`^\d+(\.\d+)+$`)
	protoRe    = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*/\d`)
	moduleRe   = regexp.MustCompile(`^(\S+?)::`)
	fenceRe    = regexp.MustCompile("^(\\s{0,3})(```|~~~)")

	countRes = []*regexp.Regexp{
		regexp.MustCompile(`\bOK=(\d+)\s+FAIL=(\d+)\b`),
		regexp.MustCompile(`\b(\d+)\s+tests?\b`),
		regexp.MustCompile(`\b(\d+)\s+failures?\b`),
		regexp.MustCompile(`\b(\d+)\s+errors?\b`),
		regexp.MustCompile(`\b(\d+)/(\d+)\b`),
	}

	repoLabelRe = regexp.MustCompile("(?i)^\\*{0,2}(repo|repository|working tree)\\*{0,2}\\s*:\\s*\\*{0,2}\\s*`?([^\\s`,;|*]+)")
	tableRepoRe = regexp.MustCompile(`(?i)^\s*\|\s*(server|repo|path|plan|packaging|project)\s*\|`)
)

var keywords = map[string]bool{
	"import": true, "exports": true, "pub": true, "use": true, "mod": true,
	"fn": true, "test": true, "tests": true, "main": true, "self": true,
	"crate": true, "super": true, "true": true, "false": true, "this": true,
	"that": true, "with": true, "from": true, "into": true, "over": true,
	"head": true, "plan": true, "file": true, "line": true, "repo": true,
}

type parser struct {
	claims        []Claim
	inFence       bool
	pending       []GitEntry
	lastAnchor    string
	lastAnchorLin int
}

func Parse(doc string) []Claim {
	p := &parser{}
	for i, raw := range strings.Split(doc, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		n := i + 1
		if fenceRe.MatchString(line) {
			p.inFence = !p.inFence
			p.flushGit()
			continue
		}
		if e, ok := parseGitEntry(line, p.inFence); ok {
			e.Line = n
			p.pending = append(p.pending, e)
			continue
		}
		p.flushGit()
		p.parseLine(line, n)
	}
	p.flushGit()
	return p.claims
}

func (p *parser) flushGit() {
	if len(p.pending) == 0 {
		return
	}
	p.claims = append(p.claims, Claim{
		Kind:    KindGitTree,
		Line:    p.pending[0].Line,
		Text:    fmtLine(p.pending),
		Entries: p.pending,
	})
	p.pending = nil
}

func fmtLine(entries []GitEntry) string {
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(e.XY + " " + e.Path)
	}
	return b.String()
}

func parseGitEntry(line string, inFence bool) (GitEntry, bool) {
	m := gitEntryRe.FindStringSubmatch(line)
	if m == nil {
		return GitEntry{}, false
	}
	xy, path, rest := m[1], m[2], strings.TrimSpace(m[3])
	if len(path) >= 2 && strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`) {
		path = path[1 : len(path)-1]
	}
	if !inFence && !strings.Contains(path, "/") {
		return GitEntry{}, false
	}
	if inFence && !strings.ContainsAny(path, "/.") {
		return GitEntry{}, false
	}
	note := ""
	if strings.HasPrefix(rest, "->") {
		f := strings.Fields(strings.TrimPrefix(rest, "->"))
		if len(f) > 0 {
			path = f[0]
			if len(f) > 1 {
				note = strings.Join(f[1:], " ")
			}
		}
	} else if strings.HasPrefix(rest, "(") {
		if end := strings.Index(rest, ")"); end > 0 {
			note = rest[:end+1]
		}
	}
	return GitEntry{XY: xy, Path: path, Note: note}, true
}

func (p *parser) parseLine(line string, n int) {
	if m := checkboxRe.FindStringSubmatch(line); m != nil {
		p.claims = append(p.claims, Claim{Kind: KindCheckbox, Line: n, Text: line, Checked: m[1] != " "})
	}
	for _, s := range backtickSpans(line) {
		s = strings.TrimSpace(s)
		if el := elidedRe.FindStringSubmatch(s); el != nil {
			if p.lastAnchor != "" && n-p.lastAnchorLin >= 0 && n-p.lastAnchorLin <= 2 {
				start, end := nums(el[1], el[2])
				p.claims = append(p.claims, Claim{Kind: KindAnchor, Line: n, Text: line, Path: p.lastAnchor, Start: start, End: end})
			}
			continue
		}
		if a := anchorRe.FindStringSubmatch(s); a != nil && pathish(a[1]) {
			start, end := nums(a[2], a[3])
			p.claims = append(p.claims, Claim{
				Kind:    KindAnchor,
				Line:    n,
				Text:    line,
				Path:    a[1],
				Start:   start,
				End:     end,
				Symbols: symbols(line, a[1]),
			})
			p.lastAnchor, p.lastAnchorLin = a[1], n
			continue
		}
		if commitRe.MatchString(s) {
			p.claims = append(p.claims, Claim{Kind: KindCommit, Line: n, Text: line, Hash: s})
			continue
		}
		if strings.Contains(strings.ToLower(line), "branch") && branchish(s) {
			p.claims = append(p.claims, Claim{Kind: KindBranch, Line: n, Text: line, Name: s})
			continue
		}
		if m := moduleRe.FindStringSubmatch(s); m != nil {
			if pathish(m[1]) {
				p.claims = append(p.claims, Claim{Kind: KindPath, Line: n, Text: line, Path: m[1]})
			}
			continue
		}
		if pathish(s) {
			p.claims = append(p.claims, Claim{Kind: KindPath, Line: n, Text: line, Path: s})
		}
	}
	seenDetail := map[string]bool{}
	for _, re := range countRes {
		m := re.FindString(line)
		if m == "" || seenDetail[m] {
			continue
		}
		seenDetail[m] = true
		p.claims = append(p.claims, Claim{Kind: KindCount, Line: n, Text: line, Detail: m})
	}
}

func backtickSpans(line string) []string {
	var spans []string
	for i := 0; i < len(line); i++ {
		if line[i] != '`' {
			continue
		}
		end := strings.IndexByte(line[i+1:], '`')
		if end < 0 {
			break
		}
		spans = append(spans, line[i+1:i+1+end])
		i += end + 1
	}
	return spans
}

var fsRoots = []string{"/home/", "/root/", "/tmp/", "/var/", "/usr/", "/etc/", "/opt/", "/proc/", "/sys/", "/dev/", "/run/", "/media/", "/mnt/", "/sbin/", "/bin/", "/lib/", "/lib64/", "/srv/", "/boot/", "/data/"}

func pathish(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t") {
		return false
	}
	if strings.Contains(s, ":") || strings.ContainsAny(s, "{}*<>[]?…") {
		return false
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "-") || strings.Contains(s, "@") {
		return false
	}
	if ipv4Re.MatchString(s) || versionRe.MatchString(s) || cidrRe.MatchString(s) {
		return false
	}
	if strings.HasPrefix(s, "~/") {
		return true
	}
	if strings.HasPrefix(s, "/") {
		for _, r := range fsRoots {
			if strings.HasPrefix(s, r) {
				return true
			}
		}
		return false
	}
	if protoRe.MatchString(s) {
		return false
	}
	if !strings.Contains(s, ".") {
		return false
	}
	return true
}

func branchish(s string) bool {
	if len(s) < 2 || strings.ContainsAny(s, "./@: \\") {
		return false
	}
	if commitRe.MatchString(s) {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func symbols(line, anchorPath string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] || keywords[s] || s == anchorPath {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, sp := range backtickSpans(line) {
		if !identRe.MatchString(sp) {
			continue
		}
		if strings.Contains(sp, "::") {
			add(sp)
			for _, part := range strings.Split(sp, "::") {
				if len(part) >= 4 && !keywords[part] {
					add(part)
				}
			}
			continue
		}
		if strings.Contains(sp, "_") || camel(sp) {
			add(sp)
		}
	}
	return out
}

func camel(s string) bool {
	var lower, upper bool
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			lower = true
		}
		if r >= 'A' && r <= 'Z' {
			upper = true
		}
	}
	return lower && upper
}

func nums(a, b string) (int, int) {
	n := atoi(a)
	end := n
	if b != "" {
		end = atoi(b)
	}
	return n, end
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return n
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func RepoHints(doc string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.Trim(p, "`\"'")
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, raw := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(raw)
		if m := repoLabelRe.FindStringSubmatch(trimmed); m != nil {
			add(m[2])
			continue
		}
		if tableRepoRe.MatchString(raw) {
			for _, sp := range backtickSpans(raw) {
				if strings.HasPrefix(sp, "/") || strings.HasPrefix(sp, "~/") {
					add(sp)
					break
				}
			}
		}
	}
	return out
}
