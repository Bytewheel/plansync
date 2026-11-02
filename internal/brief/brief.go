// Package brief renders a session-start digest of a plan: the audit's
// stale/moved/missing findings, the plan's remaining-work sections, and its
// open checkboxes, capped at a line budget so it can replace reading the
// whole plan.
package brief

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"bytewheel/plansync/internal/claim"
	"bytewheel/plansync/internal/verify"
)

type Section struct {
	Heading string   `json:"heading"`
	Line    int      `json:"line"`
	End     int      `json:"end"`
	Body    []string `json:"body"`
}

type Checkbox struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type Brief struct {
	File      string          `json:"file"`
	Repos     []string        `json:"repos,omitempty"`
	Freshness float64         `json:"freshness"`
	AgeNote   string          `json:"age_note,omitempty"`
	Findings  []verify.Result `json:"findings,omitempty"`
	Unknowns  int             `json:"unknowns,omitempty"`
	Sections  []Section       `json:"sections,omitempty"`
	Open      []Checkbox      `json:"open_checkboxes,omitempty"`
	OpenCount int             `json:"open_boxes"`
	DoneCount int             `json:"done_boxes"`
}

func Build(file string, repos []string, doc string, claims []claim.Claim, results []verify.Result, ageNote string) Brief {
	b := Brief{File: file, Repos: repos, AgeNote: ageNote}
	ok, bad := 0, 0
	for _, r := range results {
		switch r.Status {
		case verify.StatusOK:
			ok++
		case verify.StatusStale, verify.StatusMoved, verify.StatusMissing:
			bad++
			b.Findings = append(b.Findings, r)
		case verify.StatusUnknown:
			b.Unknowns++
		}
	}
	b.Freshness = 1.0
	if ok+bad > 0 {
		b.Freshness = float64(ok) / float64(ok+bad)
	}
	b.Sections = ExtractSections(doc)
	for _, c := range claims {
		if c.Kind != claim.KindCheckbox {
			continue
		}
		if c.Checked {
			b.DoneCount++
			continue
		}
		b.OpenCount++
		if inSections(b.Sections, c.Line) {
			continue
		}
		b.Open = append(b.Open, Checkbox{Line: c.Line, Text: checkboxText(c.Text)})
	}
	return b
}

func inSections(secs []Section, line int) bool {
	for _, s := range secs {
		if line >= s.Line && line <= s.End {
			return true
		}
	}
	return false
}

var (
	headingRe     = regexp.MustCompile(`^ {0,3}(#{1,6})\s+(.+?)\s*#*\s*$`)
	fenceRe       = regexp.MustCompile("^(\\s{0,3})(```|~~~)")
	remainingRe   = regexp.MustCompile(`(?i)\b(?:remaining|outstanding|unfinished|todos?|to-do|open\s+(?:items?|questions?|work)|next\s+(?:steps?|tasks?|agent)|resume\s+order|left\s+to\s+do|not\s+yet\s+done|pending)\b`)
	checkboxPreRe = regexp.MustCompile(`^\s*[-*]\s+\[[ xX]\]\s*`)
)

func checkboxText(s string) string {
	return strings.TrimSpace(checkboxPreRe.ReplaceAllString(s, ""))
}

// ExtractSections returns the plan's remaining-work sections: headings whose
// text matches the remaining-work vocabulary, each extending to the next
// heading of the same or higher level. Nested matches inside an
// already-extracted section are skipped.
func ExtractSections(doc string) []Section {
	lines := strings.Split(doc, "\n")
	type heading struct {
		line  int
		level int
		text  string
	}
	var headings []heading
	inFence := false
	for i, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		if fenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := headingRe.FindStringSubmatch(line); m != nil {
			headings = append(headings, heading{line: i + 1, level: len(m[1]), text: m[2]})
		}
	}
	var secs []Section
	for i, h := range headings {
		if !remainingRe.MatchString(h.text) {
			continue
		}
		contained := false
		for _, s := range secs {
			if h.line > s.Line && h.line <= s.End {
				contained = true
				break
			}
		}
		if contained {
			continue
		}
		end := len(lines)
		for _, h2 := range headings[i+1:] {
			if h2.level <= h.level {
				end = h2.line - 1
				break
			}
		}
		body := make([]string, 0, end-h.line)
		for _, raw := range lines[h.line:end] {
			body = append(body, strings.TrimSuffix(raw, "\r"))
		}
		for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
			body = body[:len(body)-1]
		}
		secs = append(secs, Section{Heading: h.text, Line: h.line, End: h.line + len(body), Body: body})
	}
	return secs
}

const (
	sectionBodyCap = 20
	msgCap         = 120
	minPlanLines   = 3
)

func JSON(briefs []Brief, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(briefs)
}

// Render writes the digests under a total line budget: each plan gets a share
// of the remaining budget (floor minPlanLines), and per plan the header and
// findings survive trimming first, then section excerpts, then checkbox
// lines. Elision lines report what was cut.
func Render(w io.Writer, briefs []Brief, maxLines int) {
	if maxLines < 1 {
		maxLines = 1
	}
	budget, left := maxLines, len(briefs)
	total := 0
	for _, b := range briefs {
		share := budget / left
		if share > budget {
			share = budget
		}
		if share < minPlanLines && budget >= minPlanLines {
			share = minPlanLines
		}
		if share < 1 {
			share = 1
		}
		n := renderOne(w, b, share)
		total += n
		budget -= n
		if budget < 0 {
			budget = 0
		}
		left--
	}
	fmt.Fprintf(w, "briefed %d plan(s) in %d line(s) (cap %d)\n", len(briefs), total, maxLines)
}

func renderOne(w io.Writer, b Brief, n int) int {
	var lines []string
	parts := []string{fmt.Sprintf("freshness %.0f%%", b.Freshness*100)}
	if b.OpenCount+b.DoneCount > 0 {
		parts = append(parts, fmt.Sprintf("%d open/%d done checkboxes", b.OpenCount, b.DoneCount))
	}
	if b.AgeNote != "" {
		parts = append(parts, trunc(b.AgeNote, 100))
	}
	lines = append(lines, "== "+b.File+" ("+strings.Join(parts, "; ")+")")
	for _, r := range b.Findings {
		l := fmt.Sprintf("   %-7s L%-4d %-9s %s", r.Status, r.Line, r.Kind, trunc(r.Message, msgCap))
		if r.Suggestion != "" {
			l += "  -> " + r.Suggestion
		}
		lines = append(lines, l)
	}
	if b.Unknowns > 0 {
		lines = append(lines, fmt.Sprintf("   info    -    unknown   %d claims did not resolve to any repo", b.Unknowns))
	}
	for _, s := range b.Sections {
		lines = append(lines, fmt.Sprintf("   work    L%-4d section  %s", s.Line, trunc(s.Heading, msgCap)))
		for _, l := range excerpt(s.Body, sectionBodyCap) {
			lines = append(lines, "      "+l)
		}
	}
	for _, c := range b.Open {
		lines = append(lines, fmt.Sprintf("   open    L%-4d checkbox %s", c.Line, trunc(c.Text, msgCap)))
	}
	if len(b.Findings) == 0 && len(b.Sections) == 0 && b.OpenCount == 0 && b.Unknowns == 0 {
		lines = append(lines, "   info    -    digest    no stale findings, no remaining-work sections, no open checkboxes")
	}
	if len(lines) > n {
		cut := len(lines) - n
		lines = append(lines[:n], fmt.Sprintf("   ...    +%d line(s) elided; raise --max-lines", cut))
	}
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	fmt.Fprintln(w)
	return len(lines) + 1
}

// excerpt collapses blank runs and caps the body, noting what was cut.
func excerpt(body []string, cap int) []string {
	var out []string
	blank := false
	for _, l := range body {
		if strings.TrimSpace(l) == "" {
			blank = true
			continue
		}
		if blank && len(out) > 0 {
			out = append(out, "")
		}
		blank = false
		out = append(out, l)
	}
	if len(out) > cap {
		cut := len(out) - cap
		out = append(out[:cap], fmt.Sprintf("... (+%d more line(s) in plan)", cut))
	}
	return out
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
