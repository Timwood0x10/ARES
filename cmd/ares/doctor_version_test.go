package main

import (
	"runtime"
	"testing"
)

// TestGoMinorVersionParsesAllToolchains locks C-7's fix: the doctor's Go
// check must accept every toolchain AT OR ABOVE the floor, not a hardcoded
// prefix list that false-positives on each new release (go1.27 printed the
// recommend-warn on a toolchain four minors above the floor).
func TestGoMinorVersionParsesAllToolchains(t *testing.T) {
	cases := []struct {
		in   string
		want int
		note string
	}{
		{"go1.27.1", 27, "current toolchain — was false-flagged by the prefix list"},
		{"go1.25.0", 25, "floor, accepted"},
		{"go1.26", 26, "no patch segment"},
		{"go1.30.2", 30, "future release, accepted"},
		{"go1.24.8", 24, "below floor"},
		{"devel +abc123", 0, "devel build: unparseable → honest warn"},
		{"", 0, "empty"},
	}
	for _, tc := range cases {
		if got := goMinorVersion(tc.in); got != tc.want {
			t.Errorf("goMinorVersion(%q) = %d, want %d (%s)", tc.in, got, tc.want, tc.note)
		}
	}
}

// TestDoctorGoCheckAcceptsCurrentToolchain runs the real check against the
// binary's actual toolchain: on any Go >= 1.25 this must pass, so the test
// doubles as a CI canary for the doctor's own correctness.
func TestDoctorGoCheckAcceptsCurrentToolchain(t *testing.T) {
	if v := runtime.Version(); goMinorVersion(v) < 25 {
		t.Fatalf("CI toolchain %s is below the doctor's documented floor", v)
	}
}
