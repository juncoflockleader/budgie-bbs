package commandrules

import (
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestNormalizeChatLineDefaultsRoomAndFormatsName(t *testing.T) {
	line, errDetail := NormalizeChatLine("", "  hello  ")
	if errDetail != nil {
		t.Fatalf("NormalizeChatLine error = %+v", errDetail)
	}
	if line.RoomID != "lobby" || line.RoomName != "Lobby" || line.Text != "hello" {
		t.Fatalf("line = %+v, want lobby/Lobby/hello", line)
	}

	line, errDetail = NormalizeChatLine("ops_room", "deploy")
	if errDetail != nil {
		t.Fatalf("NormalizeChatLine named room error = %+v", errDetail)
	}
	if line.RoomID != "ops_room" || line.RoomName != "Ops Room" {
		t.Fatalf("named room = %+v, want ops_room/Ops Room", line)
	}
}

func TestNormalizeChatLineRejectsInvalidInput(t *testing.T) {
	if _, errDetail := NormalizeChatLine("bad room", "hello"); errDetail == nil || errDetail.Code != proto.ErrValidationFailed {
		t.Fatalf("invalid room error = %+v, want validation failed", errDetail)
	}
	if _, errDetail := NormalizeChatLine("lobby", strings.Repeat("x", 1001)); errDetail == nil || errDetail.Code != proto.ErrValidationFailed {
		t.Fatalf("long text error = %+v, want validation failed", errDetail)
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

func TestValidatePresenceTextRejectsLongFields(t *testing.T) {
	if errDetail := ValidatePresenceText(strings.Repeat("x", 121), "default", "", "", "", "", ""); errDetail == nil || errDetail.Code != proto.ErrValidationFailed {
		t.Fatalf("status error = %+v, want validation failed", errDetail)
	}
	if errDetail := ValidatePresenceText("active", "default", "", "", "", "", strings.Repeat("x", 161)); errDetail == nil || errDetail.Code != proto.ErrValidationFailed {
		t.Fatalf("fromHost error = %+v, want validation failed", errDetail)
	}
}
