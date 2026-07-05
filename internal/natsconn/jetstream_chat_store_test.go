package natsconn

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestJetStreamChatStoreTracksBoundedHistoryAndRooms(t *testing.T) {
	store := newJetStreamChatStoreWithKV(newFakeCounterStoreKV())
	for i := 1; i <= 205; i++ {
		if err := store.InsertChatLine(
			fmt.Sprintf("line-%03d", i),
			"study-room",
			"Study Room",
			"alice",
			"Alice",
			fmt.Sprintf("body %03d", i),
			int64(1000+i),
		); err != nil {
			t.Fatalf("insert chat line %d: %v", i, err)
		}
	}

	rooms, err := store.ListChatRooms()
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	if len(rooms) < 2 || rooms[0].ID != "lobby" {
		t.Fatalf("expected lobby first plus study room, got %+v", rooms)
	}
	var studyRoom *projections.ChatRoom
	for i := range rooms {
		if rooms[i].ID == "study-room" {
			studyRoom = &rooms[i]
			break
		}
	}
	if studyRoom == nil || studyRoom.Name != "Study Room" || studyRoom.CreatedBy != "alice" || studyRoom.LineCount != 200 || studyRoom.UpdatedAt != 1205 {
		t.Fatalf("expected bounded study-room metadata, got rooms=%+v", rooms)
	}

	recent, err := store.ListChatLines("study-room", 3)
	if err != nil {
		t.Fatalf("list recent lines: %v", err)
	}
	if len(recent) != 3 || recent[0].ID != "line-203" || recent[1].ID != "line-204" || recent[2].ID != "line-205" {
		t.Fatalf("recent lines = %+v, want latest three ascending", recent)
	}
	full, err := store.ListChatLines("study-room", 200)
	if err != nil {
		t.Fatalf("list full bounded history: %v", err)
	}
	if len(full) != 200 || full[0].ID != "line-006" || full[len(full)-1].ID != "line-205" {
		t.Fatalf("bounded history = len %d first/last %+v/%+v", len(full), full[0], full[len(full)-1])
	}
}

func TestJetStreamChatStoreBacksCommandChatAndRecentReads(t *testing.T) {
	store := newJetStreamChatStoreWithKV(newFakeCounterStoreKV())
	c, err := core.New(filepath.Join(t.TempDir(), "nats-chat-store.db"), core.WithChatStore(store))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice := registerNATSCounterStoreUser(t, c, "alice")
	first := execNATSCounterStoreCommand(t, c, alice, proto.CmdSendChatLine, proto.SendChatLinePayload{
		Room: "Study-Room",
		Text: "  meet at the terminal  ",
	})
	second := execNATSCounterStoreCommand(t, c, alice, proto.CmdSendChatLine, proto.SendChatLinePayload{
		Room: "study-room",
		Text: "bring notes",
	})

	lines, err := c.ListChatLines("study-room", 10)
	if err != nil {
		t.Fatalf("list chat lines: %v", err)
	}
	gotLines := map[string]string{}
	for _, line := range lines {
		gotLines[line.ID] = line.Text
	}
	if len(lines) != 2 || gotLines[first.ID] != "meet at the terminal" || gotLines[second.ID] != "bring notes" {
		t.Fatalf("expected NATS KV recent chat lines, got %+v", lines)
	}
	rooms, err := c.ListChatRooms()
	if err != nil {
		t.Fatalf("list chat rooms: %v", err)
	}
	var studyRoom *projections.ChatRoom
	for i := range rooms {
		if rooms[i].ID == "study-room" {
			studyRoom = &rooms[i]
			break
		}
	}
	if studyRoom == nil || studyRoom.Name != "Study Room" || studyRoom.LineCount != 2 || studyRoom.CreatedBy != alice.ID {
		t.Fatalf("expected NATS KV study room metadata, got rooms=%+v", rooms)
	}
	assertNATSCounterSQLRows(t, c, "chat_lines", 0)
	var sqlStudyRooms int
	if err := c.DB.QueryRow(`SELECT COUNT(*) FROM chat_rooms WHERE id=?`, "study-room").Scan(&sqlStudyRooms); err != nil {
		t.Fatalf("count study-room SQL rows: %v", err)
	}
	if sqlStudyRooms != 0 {
		t.Fatalf("NATS KV chat store wrote %d SQL study-room rows, want 0", sqlStudyRooms)
	}
}
