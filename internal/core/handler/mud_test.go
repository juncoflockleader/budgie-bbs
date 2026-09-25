package handler

import (
	"testing"
	"time"
)

func TestMUDWorldExitsAreValid(t *testing.T) {
	// Every exit must point to a real room (catches typos in the map).
	for id, room := range mudWorld {
		for dir, dest := range room.Exits {
			if _, ok := mudWorld[dest]; !ok {
				t.Fatalf("room %q exit %q -> unknown room %q", id, dir, dest)
			}
			if mudDirNames[dir] == "" {
				t.Fatalf("room %q has unknown exit direction %q", id, dir)
			}
		}
	}
	if _, ok := mudWorld[mudStartRoom]; !ok {
		t.Fatalf("start room %q missing from world", mudStartRoom)
	}
}

func TestMUDSplit(t *testing.T) {
	cases := []struct{ in, verb, arg string }{
		{"look", "look", ""},
		{"  NORTH ", "north", ""},
		{"say hello there", "say", "hello there"},
		{"'hi", "say", "hi"},
		{":waves", "emote", "waves"},
		{"go n", "go", "n"},
		{"", "", ""},
	}
	for _, c := range cases {
		v, a := mudSplit(c.in)
		if v != c.verb || a != c.arg {
			t.Fatalf("mudSplit(%q) = (%q,%q), want (%q,%q)", c.in, v, a, c.verb, c.arg)
		}
	}
}

func TestMUDOccupancyTouchAndLeave(t *testing.T) {
	reg := &mudOccupancyRegistry{byUser: map[string]*mudOccupant{}}
	now := time.Now()
	if !reg.touch("u1", "alice", "square", now) {
		t.Fatal("first touch should report arrival")
	}
	if reg.touch("u1", "alice", "square", now) {
		t.Fatal("same-room touch should not report a new arrival")
	}
	if !reg.touch("u1", "alice", "library", now) {
		t.Fatal("moving rooms should report arrival in the new room")
	}
	reg.touch("u2", "bob", "library", now)
	occ := reg.occupants("library", "u1", now)
	if len(occ) != 1 || occ[0] != "bob" {
		t.Fatalf("occupants(library, exclude u1) = %v, want [bob]", occ)
	}
	reg.leave("u2")
	if occ := reg.occupants("library", "u1", now); len(occ) != 0 {
		t.Fatalf("after leave, occupants = %v, want []", occ)
	}
}
