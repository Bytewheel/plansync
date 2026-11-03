// Package gates loads per-repo gate configuration (.plansync.toml), runs
// the configured commands, and verifies the plan's numeric count claims
// against their output.
package gates

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"bytewheel/plansync/internal/claim"
	"bytewheel/plansync/internal/verify"
)

// ConfigFile is the per-repo gate configuration location.
const ConfigFile = ".plansync.toml"

// Gate is one configured verification command.
type Gate struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// Run is the outcome of executing one gate.
type Run struct {
	Gate   Gate   `json:"gate"`
	Dir    string `json:"dir"`
	Failed bool   `json:"failed"`
	Error  string `json:"error,omitempty"`
	Output string `json:"output"`
}

// Load parses a .plansync.toml. The accepted subset: comments (#), blank
// lines, [[gate]] table headers, and string fields name = "…" / command =
// "…" (basic strings, "" and \\ escapes). Anything else is an error — the
// file is tool-read, not hand-loose.
func Load(path string) ([]Gate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var gates []Gate
	cur := -1
	for n, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[[gate]]" {
			gates = append(gates, Gate{})
			cur = len(gates) - 1
			continue
		}
		k, v, ok := parseField(line)
		if !ok || cur < 0 {
			return nil, fmt.Errorf("%s:%d: expected [[gate]] header or name/command field, got %q", path, n+1, line)
		}
		switch k {
		case "name":
			gates[cur].Name = v
		case "command":
			gates[cur].Command = v
		default:
			return nil, fmt.Errorf("%s:%d: unknown field %q (want name or command)", path, n+1, k)
		}
	}
	for i, g := range gates {
		if g.Command == "" {
			return nil, fmt.Errorf("%s: gate %d has no command", path, i+1)
		}
		if g.Name == "" {
			gates[i].Name = fmt.Sprintf("gate %d", i+1)
		}
	}
	return gates, nil
}

func parseField(line string) (key, val string, ok bool) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:eq])
	val = strings.TrimSpace(line[eq+1:])
	if len(val) < 2 || val[0] != '"' || val[len(val)-1] != '"' {
		return "", "", false
	}
	body := val[1 : len(val)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' {
			if i+1 >= len(body) || (body[i+1] != '"' && body[i+1] != '\\') {
				return "", "", false
			}
			b.WriteByte(body[i+1])
			i++
			continue
		}
		b.WriteByte(body[i])
	}
	return key, b.String(), true
}

// Find locates the gate config: an explicit path first (read errors
// propagate), then ConfigFile in each candidate repo root, then next to
// the plan. ok is false when no config exists.
func Find(explicit, planPath string, repos []string) (string, bool, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", false, err
		}
		return explicit, true, nil
	}
	var dirs []string
	dirs = append(dirs, repos...)
	if planPath != "" {
		dirs = append(dirs, filepath.Dir(planPath))
	}
	for _, d := range dirs {
		p := filepath.Join(d, ConfigFile)
		if _, err := os.Stat(p); err == nil {
			return p, true, nil
		}
	}
	return "", false, nil
}

// RunAll executes the gates with the shell in dir and captures combined
// output (capped). One failing command does not stop the rest.
func RunAll(gs []Gate, dir string) []Run {
	var out []Run
	for _, g := range gs {
		r := Run{Gate: g, Dir: dir}
		cmd := exec.Command("sh", "-c", g.Command)
		cmd.Dir = dir
		b, err := cmd.CombinedOutput()
		r.Output = strings.TrimSpace(string(b))
		if len(r.Output) > 8000 {
			r.Output = r.Output[:8000] + "\n... (truncated)"
		}
		if err != nil {
			r.Failed = true
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out
}

// VerifyCounts rewrites the count-claim results against the gate output.
// A claim is ok when its detail appears verbatim, or when every number it
// carries appears in one count-shaped line of the output ("61 tests" is
// confirmed by "OK=61 FAIL=0"). When the output carries count-shaped lines
// but none confirms the claim, it becomes stale with the actual numbers;
// otherwise it stays info. Other results pass through untouched.
func VerifyCounts(results []verify.Result, runs []Run) []verify.Result {
	var text []string
	for _, r := range runs {
		text = append(text, r.Output)
	}
	gateText := strings.Join(text, "\n")
	actual := claim.CountMatches(gateText)
	out := make([]verify.Result, len(results))
	copy(out, results)
	for i, r := range out {
		if r.Kind != claim.KindCount || r.Detail == "" {
			continue
		}
		if strings.Contains(gateText, r.Detail) {
			out[i].Status = verify.StatusOK
			out[i].Message = r.Detail + " matches gate output"
			continue
		}
		nums := numberRuns(r.Detail)
		confirmed := ""
		for _, m := range actual {
			if containsAllNumbers(m, nums) {
				confirmed = m
				break
			}
		}
		switch {
		case confirmed != "":
			out[i].Status = verify.StatusOK
			out[i].Message = r.Detail + " confirmed by gate output \"" + confirmed + "\""
		case len(actual) > 0:
			list := strings.Join(actual, ", ")
			if len(list) > 100 {
				list = list[:100] + "..."
			}
			out[i].Status = verify.StatusStale
			out[i].Message = r.Detail + " not found in gate output; gates report: " + list
		default:
			out[i].Message = r.Detail + " not confirmed by gates (no count-shaped lines in gate output)"
		}
	}
	return out
}

// numberRuns returns the maximal digit runs of s.
func numberRuns(s string) []string {
	var out []string
	start := -1
	for i := 0; i <= len(s); i++ {
		d := i < len(s) && s[i] >= '0' && s[i] <= '9'
		if d && start < 0 {
			start = i
		}
		if !d && start >= 0 {
			out = append(out, s[start:i])
			start = -1
		}
	}
	return out
}

func containsAllNumbers(shape string, nums []string) bool {
	for _, n := range nums {
		found := false
		for _, m := range numberRuns(shape) {
			if m == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(nums) > 0
}
