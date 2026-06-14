package core

import (
	"testing"
	"time"
)

// Tests reuse mockBus from pg_listener_test.go (same package).

func TestNodeRegistryRegisterUnregister(t *testing.T) {
	bus := &mockBus{publishFn: func() {}}
	reg := newNodeRegistry(bus)

	entry := NodeEntry{
		NodeID:    "sess_test01",
		UserID:    "usr_1",
		Username:  "alice",
		RemoteIP:  "127.0.0.1",
		Location:  "main-menu",
		LoginTime: time.Now(),
	}

	kicked := false
	msgCh := reg.Register(entry, func() { kicked = true })

	nodes := reg.List()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node after Register, got %d", len(nodes))
	}
	if nodes[0].Username != "alice" {
		t.Errorf("expected username alice, got %s", nodes[0].Username)
	}
	_ = msgCh

	reg.Unregister("sess_test01")
	nodes = reg.List()
	if len(nodes) != 0 {
		t.Fatalf("expected 0 nodes after Unregister, got %d", len(nodes))
	}
	_ = kicked // kick was not called; just checking compile
}

func TestNodeRegistryKick(t *testing.T) {
	bus := &mockBus{publishFn: func() {}}
	reg := newNodeRegistry(bus)

	kicked := false
	entry := NodeEntry{NodeID: "sess_kick01", UserID: "usr_2", Username: "bob", LoginTime: time.Now()}
	_ = reg.Register(entry, func() { kicked = true })

	if err := reg.KickNode("sess_kick01"); err != nil {
		t.Fatalf("KickNode returned unexpected error: %v", err)
	}
	if !kicked {
		t.Error("expected cancel function to be called on kick")
	}

	// Kicking a non-existent node should return an error.
	if err := reg.KickNode("sess_does_not_exist"); err == nil {
		t.Error("expected error for unknown nodeID")
	}
}

func TestNodeRegistrySendMessage(t *testing.T) {
	bus := &mockBus{publishFn: func() {}}
	reg := newNodeRegistry(bus)

	entry := NodeEntry{NodeID: "sess_msg01", UserID: "usr_3", Username: "carol", LoginTime: time.Now()}
	msgCh := reg.Register(entry, func() {})

	if err := reg.SendMessage("sess_msg01", "hello"); err != nil {
		t.Fatalf("SendMessage unexpected error: %v", err)
	}

	select {
	case got := <-msgCh:
		if got != "hello" {
			t.Errorf("expected 'hello', got %q", got)
		}
	default:
		t.Error("expected message in channel")
	}

	// Unknown node.
	if err := reg.SendMessage("sess_unknown", "hi"); err == nil {
		t.Error("expected error for unknown nodeID")
	}
}

func TestNodeRegistryUpdateLocation(t *testing.T) {
	bus := &mockBus{publishFn: func() {}}
	reg := newNodeRegistry(bus)

	entry := NodeEntry{NodeID: "sess_loc01", UserID: "usr_4", Username: "dave", Location: "main-menu", LoginTime: time.Now()}
	_ = reg.Register(entry, func() {})

	reg.UpdateLocation("sess_loc01", "board-list")

	nodes := reg.List()
	if len(nodes) == 0 {
		t.Fatal("expected 1 node")
	}
	if nodes[0].Location != "board-list" {
		t.Errorf("expected location 'board-list', got %q", nodes[0].Location)
	}

	// UpdateLocation on unknown node should not panic.
	reg.UpdateLocation("sess_unknown", "anywhere")
}

func TestNodeRegistryDoubleUnregister(t *testing.T) {
	bus := &mockBus{publishFn: func() {}}
	reg := newNodeRegistry(bus)

	entry := NodeEntry{NodeID: "sess_dup01", UserID: "usr_5", Username: "eve", LoginTime: time.Now()}
	_ = reg.Register(entry, func() {})

	reg.Unregister("sess_dup01")
	// Second call must not panic.
	reg.Unregister("sess_dup01")
}
