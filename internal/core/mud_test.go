package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func mudCmd(t *testing.T, c *core.Core, actor *projections.User, line string) {
	t.Helper()
	raw := marshalCoreTestPayload(t, proto.MUDCommandPayload{Line: line})
	if r := c.ExecCmd(context.Background(), actor, proto.CmdMUDCommand, raw, ""); r.Err != nil {
		t.Fatalf("mud %q: %s", line, r.Err.Message)
	}
}

func drainMUD(sub *core.Subscription) []*proto.Event {
	var out []*proto.Event
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case evt := <-sub.Ch:
			out = append(out, evt)
		case <-deadline:
			return out
		}
	}
}

func TestMUDMoveSpeakAndPresence(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	alice, err := c.RegisterUser("alice", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}

	sub := c.Subscribe([]string{"mud:room:square", "mud:room:library", "mud:user:" + alice.ID})
	defer c.Unsubscribe(sub)

	mudCmd(t, c, alice, "look")
	mudCmd(t, c, alice, "north")
	mudCmd(t, c, alice, "say hello world")

	evts := drainMUD(sub)
	var sawSquareView, sawLibraryView, sawEnterSquare, sawLeaveSquare, sawEnterLibrary, sawSay bool
	for _, e := range evts {
		switch e.Kind {
		case proto.EvtMUDView:
			if v, ok := e.Payload.(*proto.MUDViewPayload); ok && v.Room != nil {
				switch v.Room.ID {
				case "square":
					sawSquareView = true
				case "library":
					sawLibraryView = true
					if len(v.Room.Exits) != 1 || v.Room.Exits[0] != "south" {
						t.Fatalf("library exits = %v, want [south]", v.Room.Exits)
					}
				}
			}
		case proto.EvtMUDRoom:
			p, ok := e.Payload.(*proto.MUDRoomEventPayload)
			if !ok {
				continue
			}
			switch {
			case p.Room == "square" && p.Kind == "enter":
				sawEnterSquare = true
			case p.Room == "square" && p.Kind == "leave":
				sawLeaveSquare = true
			case p.Room == "library" && p.Kind == "enter":
				sawEnterLibrary = true
			case p.Room == "library" && p.Kind == "say":
				sawSay = true
			}
		}
	}
	if !sawSquareView || !sawLibraryView {
		t.Fatalf("expected room views for square and library (square=%v library=%v)", sawSquareView, sawLibraryView)
	}
	if !sawEnterSquare || !sawLeaveSquare || !sawEnterLibrary {
		t.Fatalf("expected enter(square)+leave(square)+enter(library); got %v/%v/%v", sawEnterSquare, sawLeaveSquare, sawEnterLibrary)
	}
	if !sawSay {
		t.Fatal("expected a say event in the library")
	}

	var room string
	if err := c.DB.QueryRow(`SELECT room_id FROM mud_players WHERE user_id=?`, alice.ID).Scan(&room); err != nil {
		t.Fatalf("load player room: %v", err)
	}
	if room != "library" {
		t.Fatalf("persisted room = %q, want library", room)
	}
}
