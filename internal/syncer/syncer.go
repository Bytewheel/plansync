// Package syncer computes and applies edits that bring plan documentation
// back in sync with repository state. It is deliberately conservative: only
// tool-owned marker regions are rewritten freely, moved anchors are re-pointed
// with symbol verification, and everything else is left to the human (or the
// heuristic policy, which additionally refreshes the most recent unmarked
// git-status block per repository).
package syncer

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"bytewheel/plansync/internal/claim"
	"bytewheel/plansync/internal/verify"
)

type Policy string

const (
	PolicyMarkers   Policy = "markers"
	PolicyHeuristic Policy = "heuristic"
	PolicyReport    Policy = "report"
)

func ParsePolicy(s string) (Policy, error) {
	switch s {
	case "markers":
		return PolicyMarkers, nil
	case "heuristic":
		return PolicyHeuristic, nil
	case "report":
		return PolicyReport, nil
	}
	return "", fmt.Errorf("unknown policy %q (want markers, heuristic, or report)", s)
}

// Edit replaces the inclusive 1-based line range [Start, End] with After.
// Start == End+1 means a pure insertion before line Start.
type Edit struct {
	Start  int      `json:"start"`
	End    int      `json:"end"`
	Kind   string   `json:"kind"`
	Reason string   `json:"reason"`
	After  []string `json:"after"`
}

var (
	beginRe = regexp.MustCompile(`^\s*<!--\s*plansync:begin\s+([\w-]+)((?:\s+[\w-]+=[^\s>]+)*)\s*-->`)
	endRe   = regexp.MustCompile(`^\s*<!--\s*plansync:end\s*-->`)
	argRe   = regexp.MustCompile(`([\w-]+)=([^\s>]+)`)
)

// Compute returns the edits the given policy would make to lines, plus
// human-readable warnings for malformed markers. It never mutates lines.
func Compute(lines []string, v *verify.Verifier, claims []claim.Claim, results []verify.Result, policy Policy) ([]Edit, []string) {
	type editP struct {
		prio int
		e    Edit
	}
	var all []editP
	var warnings []string

	// Marker regions are tool-owned in every policy.
	markerEdits, mw := scanMarkers(lines, v)
	warnings = append(warnings, mw...)
	for _, e := range markerEdits {
		all = append(all, editP{prio: 0, e: e})
	}

	// Line rewrites: anchors in every policy; stale head-table commits only
	// in heuristic/report (unmarked tables are not tool-owned).
	rewrites := map[int]rewrite{}
	for _, r := range results {
		switch {
		case r.Kind == claim.KindAnchor && r.Status == verify.StatusMoved && r.Suggestion != "":
			addRewrite(rewrites, r.Line, "anchor", "re-anchor "+r.Path, anchorFn(r))
		case (policy == PolicyHeuristic || policy == PolicyReport) &&
			r.Kind == claim.KindCommit && r.Status == verify.StatusStale && r.Suggestion != "":
			addRewrite(rewrites, r.Line, "commit", "refresh head "+r.Hash+" -> "+r.Suggestion, commitFn(r))
		}
	}
	for ln, rw := range rewrites {
		newLine, changed := rw.fn(lines[ln-1])
		if !changed {
			continue
		}
		all = append(all, editP{prio: 3, e: Edit{Start: ln, End: ln, Kind: rw.kind, Reason: rw.reason, After: []string{newLine}}})
	}

	// Heuristic extras: refresh the most recent unmarked git-status block per
	// repository (older blocks are doc-log history; the newest is the live
	// "working tree state" pointer).
	if policy == PolicyHeuristic || policy == PolicyReport {
		for _, e := range gitTreeRefreshes(lines, v, claims, results) {
			all = append(all, editP{prio: 1, e: e})
		}
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].e.Start != all[j].e.Start {
			return all[i].e.Start < all[j].e.Start
		}
		return all[i].prio < all[j].prio
	})
	var out []Edit
	lastEnd := 0
	for _, x := range all {
		if x.e.Start <= lastEnd {
			continue // overlaps a kept, higher-priority edit
		}
		out = append(out, x.e)
		if x.e.End > lastEnd {
			lastEnd = x.e.End
		}
	}
	return out, warnings
}

type rewrite struct {
	kind   string
	reason string
	fn     func(string) (string, bool)
}

func addRewrite(m map[int]rewrite, ln int, kind, reason string, fn func(string) (string, bool)) {
	if prev, ok := m[ln]; ok {
		pf, pk, pr := prev.fn, prev.kind, prev.reason
		m[ln] = rewrite{kind: pk + "+" + kind, reason: pr + "; " + reason, fn: func(s string) (string, bool) {
			s1, c1 := pf(s)
			s2, c2 := fn(s1)
			return s2, c1 || c2
		}}
		return
	}
	m[ln] = rewrite{kind: kind, reason: reason, fn: fn}
}

// anchorFn rebuilds the moved anchor's document token. Anchors are parsed
// only from backticked spans, so the backticked forms are replaced.
func anchorFn(r verify.Result) func(string) (string, bool) {
	return func(line string) (string, bool) {
		old := fmt.Sprintf("`%s:%d`", r.Path, r.Start)
		if r.End != r.Start {
			old = fmt.Sprintf("`%s:%d-%d`", r.Path, r.Start, r.End)
		}
		if strings.Contains(line, old) {
			return strings.ReplaceAll(line, old, "`"+r.Suggestion+"`"), true
		}
		if r.End == r.Start {
			elided := fmt.Sprintf("`:%d`", r.Start)
			if strings.Contains(line, elided) {
				if p := strings.SplitN(r.Suggestion, ":", 2); len(p) == 2 && p[0] == r.Path {
					return strings.ReplaceAll(line, elided, "`:"+p[1]+"`"), true
				}
			}
		}
		return line, false
	}
}

func commitFn(r verify.Result) func(string) (string, bool) {
	return func(line string) (string, bool) {
		if !strings.Contains(line, r.Hash) {
			return line, false
		}
		return strings.ReplaceAll(line, r.Hash, r.Suggestion), true
	}
}

func scanMarkers(lines []string, v *verify.Verifier) ([]Edit, []string) {
	var edits []Edit
	var warnings []string
	i := 0
	for i < len(lines) {
		m := beginRe.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		kind, argstr := m[1], m[2]
		args := map[string]string{}
		for _, am := range argRe.FindAllStringSubmatch(argstr, -1) {
			args[am[1]] = am[2]
		}
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if endRe.MatchString(lines[j]) {
				end = j
				break
			}
		}
		if end < 0 {
			warnings = append(warnings, fmt.Sprintf("L%d: plansync:begin %s has no matching plansync:end", i+1, kind))
			i++
			continue
		}
		body, warn := markerBody(kind, args, v)
		if warn != "" {
			warnings = append(warnings, fmt.Sprintf("L%d: %s", i+1, warn))
			i = end + 1
			continue
		}
		region := lines[i+1 : end]
		if equalLines(region, body) {
			i = end + 1
			continue
		}
		edits = append(edits, Edit{
			Start:  i + 2,
			End:    end,
			Kind:   "marker-" + kind,
			Reason: "refresh marker region to current state",
			After:  body,
		})
		i = end + 1
	}
	return edits, warnings
}

func markerBody(kind string, args map[string]string, v *verify.Verifier) ([]string, string) {
	switch kind {
	case "git-status":
		repo := args["repo"]
		if repo == "" {
			return nil, "git-status marker requires repo=PATH"
		}
		root := verify.GitRootOf(repo)
		if root == "" {
			return nil, fmt.Sprintf("repo %s is not a git worktree", repo)
		}
		body := []string{"```"}
		body = append(body, treeLines(v, root)...)
		return append(body, "```"), ""
	case "heads":
		repos := v.Repos()
		if len(repos) == 0 {
			return nil, "no candidate repos discovered for heads marker"
		}
		body := []string{"| repo | head |", "|---|---|"}
		for _, r := range repos {
			body = append(body, fmt.Sprintf("| `%s` | `%s` |", shortHome(r), v.HeadShort(r)))
		}
		return body, ""
	default:
		return nil, fmt.Sprintf("unknown marker kind %q", kind)
	}
}

// treeLines is the unfenced body for a git-status region: current porcelain
// lines, or a clean marker comment.
func treeLines(v *verify.Verifier, root string) []string {
	if pl := v.PorcelainLines(root); len(pl) > 0 {
		return pl
	}
	return []string{"# clean (HEAD " + v.HeadShort(root) + ")"}
}

func gitTreeRefreshes(lines []string, v *verify.Verifier, claims []claim.Claim, results []verify.Result) []Edit {
	resByLine := map[int]verify.Result{}
	for _, r := range results {
		if r.Kind == claim.KindGitTree {
			resByLine[r.Line] = r
		}
	}
	lastByRepo := map[string]claim.Claim{}
	for _, c := range claims {
		if c.Kind != claim.KindGitTree || len(c.Entries) == 0 {
			continue
		}
		r, ok := resByLine[c.Line]
		if !ok || r.Repo == "" || r.Status != verify.StatusStale {
			continue
		}
		if prev, seen := lastByRepo[r.Repo]; !seen || c.Line > prev.Line {
			lastByRepo[r.Repo] = c
		}
	}
	repos := make([]string, 0, len(lastByRepo))
	for r := range lastByRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	var edits []Edit
	for _, repo := range repos {
		c := lastByRepo[repo]
		last := c.Entries[len(c.Entries)-1].Line
		body := treeLines(v, repo)
		if equalLines(lines[c.Line-1:last], body) {
			continue
		}
		edits = append(edits, Edit{
			Start:  c.Line,
			End:    last,
			Kind:   "git-tree",
			Reason: "refresh most recent working-tree block to current state (heuristic)",
			After:  body,
		})
	}
	return edits
}

// Apply returns a copy of lines with edits applied. Edits must be sorted by
// Start and non-overlapping (as returned by Compute).
func Apply(lines []string, edits []Edit) ([]string, error) {
	out := make([]string, len(lines))
	copy(out, lines)
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		if e.Start < 1 || e.End > len(out) || e.Start > e.End+1 {
			return nil, fmt.Errorf("edit %s has invalid range %d-%d for %d lines", e.Kind, e.Start, e.End, len(out))
		}
		next := make([]string, 0, len(out)+len(e.After))
		next = append(next, out[:e.Start-1]...)
		next = append(next, e.After...)
		next = append(next, out[e.End:]...)
		out = next
	}
	return out, nil
}

// RenderDiff renders a compact unified-style diff of the edits.
func RenderDiff(path string, lines []string, edits []Edit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "== %s\n", path)
	for _, e := range edits {
		fmt.Fprintf(&b, "@@ L%d-L%d @@ %s\n", e.Start, e.End, e.Reason)
		if e.Start > 1 {
			fmt.Fprintf(&b, "  %s\n", trunc(lines[e.Start-2]))
		}
		for i := e.Start; i <= e.End; i++ {
			fmt.Fprintf(&b, "- %s\n", trunc(lines[i-1]))
		}
		for _, l := range e.After {
			fmt.Fprintf(&b, "+ %s\n", trunc(l))
		}
		if e.End >= 1 && e.End < len(lines) {
			fmt.Fprintf(&b, "  %s\n", trunc(lines[e.End]))
		}
	}
	return b.String()
}

func trunc(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func shortHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
