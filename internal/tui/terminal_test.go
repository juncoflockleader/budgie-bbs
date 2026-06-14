package tui

import (
	"testing"
	"time"
)

func TestTerminalProfileFromEnvironDumbTerm(t *testing.T) {
	profile := terminalProfileFromEnviron([]string{"TERM=dumb"})
	if profile.supportsANSI {
		t.Fatalf("expected dumb terminal to disable ANSI rendering")
	}
}

func TestTerminalProfileFromEnvironUnsupportedVt100(t *testing.T) {
	profile := terminalProfileFromEnviron([]string{"TERM=vt100"})
	if profile.supportsANSI {
		t.Fatalf("expected vt100 terminal to disable ANSI rendering")
	}
}

func TestTerminalProfileFromEnvironFallsBackToANSIWithColorEnv(t *testing.T) {
	profile := terminalProfileFromEnviron([]string{"COLORTERM=truecolor"})
	if !profile.supportsANSI {
		t.Fatalf("expected terminal with color profile env var to enable ANSI rendering")
	}
}

func TestBaudDelayFromSetting(t *testing.T) {
	got := baudDelayFromSetting("2400")
	if got <= 4*time.Millisecond || got >= 5*time.Millisecond {
		t.Fatalf("expected 2400 baud delay to be around 4.16ms, got %s", got)
	}

	got = baudDelayFromSetting("120")
	if got != 0 {
		t.Fatalf("expected low baud to disable delay, got %s", got)
	}

	got = baudDelayFromSetting("bad")
	if got != 0 {
		t.Fatalf("expected invalid setting to disable delay")
	}
}
