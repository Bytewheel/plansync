// Package report renders audit results for humans and machines.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"bytewheel/plansync/internal/verify"
)

type Report struct {
	File      string          `json:"file"`
	Repos     []string        `json:"repos,omitempty"`
	Results   []verify.Result `json:"results"`
	AgeNote   string          `json:"age_note,omitempty"`
	Freshness float64         `json:"freshness"`
}

func Build(file string, repos []string, results []verify.Result, ageNote string) Report {
	ok, bad := 0, 0
	for _, r := range results {
		switch r.Status {
		case verify.StatusOK:
			ok++
		case verify.StatusStale, verify.StatusMoved, verify.StatusMissing:
			bad++
		}
	}
	f := 1.0
	if ok+bad > 0 {
		f = float64(ok) / float64(ok+bad)
	}
	return Report{File: file, Repos: repos, Results: results, AgeNote: ageNote, Freshness: f}
}

func JSON(reps []Report, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(reps)
}

func Render(reps []Report, verbose bool, w io.Writer) bool {
	anyBad := false
	totals := map[verify.Status]int{}
	for _, rep := range reps {
		fmt.Fprintf(w, "== %s\n", rep.File)
		if len(rep.Repos) > 0 {
			fmt.Fprintf(w, "   repos: %s\n", strings.Join(shorten(rep.Repos), ", "))
		}
		okByKind := map[string]int{}
		unknown := 0
		openBoxes, doneBoxes := 0, 0
		var openLines []string
		counts := 0
		for _, r := range rep.Results {
			totals[r.Status]++
			switch {
			case r.Status == verify.StatusOK:
				okByKind[string(r.Kind)]++
				if verbose {
					fmt.Fprintf(w, "   %-7s L%-4d %-9s %s\n", r.Status, r.Line, r.Kind, truncate(r.Message, 140))
				}
			case r.Status == verify.StatusInfo && (r.Kind == "checkbox" || r.Kind == "count"):
				if r.Kind == "checkbox" {
					if strings.Contains(r.Message, "open") {
						openBoxes++
						openLines = append(openLines, fmt.Sprintf("L%d", r.Line))
					} else {
						doneBoxes++
					}
				} else {
					counts++
				}
			case r.Status == verify.StatusUnknown:
				unknown++
			default:
				anyBad = true
				line := fmt.Sprintf("   %-7s L%-4d %-9s %s", r.Status, r.Line, r.Kind, truncate(r.Message, 200))
				if r.Suggestion != "" {
					line += "  -> " + r.Suggestion
				}
				fmt.Fprintln(w, line)
			}
		}
		var oks []string
		for k, n := range okByKind {
			oks = append(oks, fmt.Sprintf("%d %s", n, k))
		}
		sort.Strings(oks)
		if len(oks) > 0 {
			fmt.Fprintf(w, "   ok      %s\n", strings.Join(oks, ", "))
		}
		if unknown > 0 {
			fmt.Fprintf(w, "   info    %d claims did not resolve to any repo (paths outside candidates, remotes-only refs)\n", unknown)
		}
		if openBoxes+doneBoxes > 0 {
			boxLine := fmt.Sprintf("   info    checkboxes: %d open, %d done", openBoxes, doneBoxes)
			if openBoxes > 0 && openBoxes <= 10 {
				boxLine += " (" + strings.Join(openLines, ", ") + ")"
			}
			fmt.Fprintln(w, boxLine)
		}
		if counts > 0 {
			fmt.Fprintf(w, "   info    %d numeric claims (test/gate counts) not verified; re-run gates to confirm\n", counts)
		}
		if rep.AgeNote != "" {
			fmt.Fprintf(w, "   info    %s\n", rep.AgeNote)
		}
		fmt.Fprintf(w, "   freshness: %.0f%%\n\n", rep.Freshness*100)
	}
	fmt.Fprintf(w, "audited %d plan(s):", len(reps))
	for _, s := range []verify.Status{verify.StatusOK, verify.StatusStale, verify.StatusMoved, verify.StatusMissing, verify.StatusUnknown, verify.StatusInfo} {
		if totals[s] > 0 {
			fmt.Fprintf(w, " %d %s", totals[s], s)
		}
	}
	fmt.Fprintln(w)
	return anyBad
}

func shorten(repos []string) []string {
	var out []string
	home := homeDir()
	for _, r := range repos {
		if home != "" && strings.HasPrefix(r, home) {
			r = "~" + strings.TrimPrefix(r, home)
		}
		out = append(out, r)
	}
	return out
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
