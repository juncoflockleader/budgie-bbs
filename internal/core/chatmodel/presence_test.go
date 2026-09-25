package chatmodel

import (
	"strings"
	"testing"
)

func TestNormalizeLineDefaultsRoomAndFormatsName(t *testing.T) {
	line, failure := NormalizeLine("", "  hello  ")
	if failure != LineOK {
		t.Fatalf("NormalizeLine failure = %q", failure)
	}
	if line.RoomID != "lobby" || line.RoomName != "Lobby" || line.Text != "hello" {
		t.Fatalf("line = %+v, want lobby/Lobby/hello", line)
	}

	line, failure = NormalizeLine("ops_room", "deploy")
	if failure != LineOK {
		t.Fatalf("NormalizeLine named room failure = %q", failure)
	}
	if line.RoomID != "ops_room" || line.RoomName != "Ops Room" {
		t.Fatalf("named room = %+v, want ops_room/Ops Room", line)
	}
}

func TestNormalizeLineRejectsInvalidInput(t *testing.T) {
	if _, failure := NormalizeLine("bad room", "hello"); failure != LineInvalidRoom {
		t.Fatalf("invalid room failure = %q, want %q", failure, LineInvalidRoom)
	}
	if _, failure := NormalizeLine("lobby", strings.Repeat("x", 1001)); failure != LineTextTooLong {
		t.Fatalf("long text failure = %q, want %q", failure, LineTextTooLong)
	}
}

func TestDerivePresenceHints(t *testing.T) {
	mode, boardID, threadID := DerivePresenceHints("reading:board:general")
	if mode != "reading" || boardID != "general" || threadID != "" {
		t.Fatalf("board hints = %q/%q/%q", mode, boardID, threadID)
	}

	mode, boardID, threadID = DerivePresenceHints("reading:thread:thr_1")
	if mode != "reading" || boardID != "" || threadID != "thr_1" {
		t.Fatalf("thread hints = %q/%q/%q", mode, boardID, threadID)
	}

	mode, boardID, threadID = DerivePresenceHints("reading:general")
	if mode != "reading" || boardID != "general" || threadID != "" {
		t.Fatalf("legacy board hints = %q/%q/%q", mode, boardID, threadID)
	}
}

func TestPresenceTextTooLongField(t *testing.T) {
	if got := PresenceTextTooLongField(PresenceText{Status: strings.Repeat("x", 121), SessionID: "default"}); got != "status" {
		t.Fatalf("status field = %q, want status", got)
	}
	if got := PresenceTextTooLongField(PresenceText{Status: "active", SessionID: "default", FromHost: strings.Repeat("x", 161)}); got != "fromHost" {
		t.Fatalf("fromHost field = %q, want fromHost", got)
	}
}
