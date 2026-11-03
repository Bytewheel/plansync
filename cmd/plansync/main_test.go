package main

import (
	"testing"
	"time"

	"bytewheel/plansync/internal/verify"
)

func TestAgeNote(t *testing.T) {
	v := verify.New(nil, "", "")
	today := time.Now().Format("2006-01-02")
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"no date", "no dates here\n", ""},
		{"past date", "decided 2026-10-22 to ship\n", "plan dated 2026-10-22"},
		{"today", "dated " + today + "\n", "plan dated " + today},
		{"future only", "wrap at 2038-01-19T03:14:07Z looms\n", ""},
		{"future then past", "wrap at 2038-01-19; decided 2026-10-22 to ship\n", "plan dated 2026-10-22"},
	}
	for _, tc := range cases {
		if got := ageNote(tc.doc, v); got != tc.want {
			t.Errorf("%s: ageNote = %q, want %q", tc.name, got, tc.want)
		}
	}
}
