package natsconn

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestJetStreamPresenceStoreTracksSessionLifecycle(t *testing.T) {
	store := newJetStreamPresenceStoreWithKV(newFakeCounterStoreKV())
	base := time.Now().UTC().Add(-time.Second).UnixMilli()

	if err := store.SetUserPresence("bob", "web", "active", "reading", "general", "", "General", "203.0.113.9", base); err != nil {
		t.Fatalf("set active presence: %v", err)
	}
	record := assertJetStreamPresenceRecord(t, store, "bob", "web")
	if record.Status != "active" || record.Mode != "reading" || record.BoardID != "general" || record.LocationLabel != "General" || record.FromHost != "203.0.113.9" || record.LastSeen != base {
		t.Fatalf("initial presence record = %+v", record)
	}

	if err := store.SetUserPresence("bob", "web", "active", "reading", "general", "", "General", "203.0.113.9", base+10_000); err != nil {
		t.Fatalf("set coalesced active presence: %v", err)
	}
	record = assertJetStreamPresenceRecord(t, store, "bob", "web")
	if record.LastSeen != base {
		t.Fatalf("coalesced presence lastSeen = %d, want unchanged %d", record.LastSeen, base)
	}

	if err := store.SetUserPresence("bob", "web", "active", "reading", "general", "", "General", "203.0.113.9", base+35_000); err != nil {
		t.Fatalf("set post-window active presence: %v", err)
	}
	record = assertJetStreamPresenceRecord(t, store, "bob", "web")
	if record.LastSeen != base+35_000 {
		t.Fatalf("post-window presence lastSeen = %d, want %d", record.LastSeen, base+35_000)
	}

	if err := store.SetUserPresence("bob", "web", "invisible", "", "general", "", "Hidden", "203.0.113.9", base+40_000); err != nil {
		t.Fatalf("set invisible presence: %v", err)
	}
	record = assertJetStreamPresenceRecord(t, store, "bob", "web")
	if record.Status != "invisible" || record.LastSeen != base+40_000 {
		t.Fatalf("invisible presence record = %+v", record)
	}

	if err := store.SetGuestPresence("guest-web", "active", "lobby", "203.0.113.25", base); err != nil {
		t.Fatalf("set active guest presence: %v", err)
	}
	guestRecord := assertJetStreamGuestPresenceRecord(t, store, "guest-web")
	if guestRecord.Status != "active" || guestRecord.LocationLabel != "lobby" || guestRecord.FromHost != "203.0.113.25" || guestRecord.LastSeen != base {
		t.Fatalf("initial guest presence record = %+v", guestRecord)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("stats after guest login: %v", err)
	}
	if stats.OnlineGuests != 1 || stats.TotalGuestLogins != 1 || stats.TotalGuestLogouts != 0 {
		t.Fatalf("guest stats after login = %+v", stats)
	}
	if err := store.SetGuestPresence("guest-web", "active", "lobby", "203.0.113.25", base+10_000); err != nil {
		t.Fatalf("set coalesced guest presence: %v", err)
	}
	guestRecord = assertJetStreamGuestPresenceRecord(t, store, "guest-web")
	if guestRecord.LastSeen != base {
		t.Fatalf("coalesced guest lastSeen = %d, want unchanged %d", guestRecord.LastSeen, base)
	}
	stats, err = store.Stats()
	if err != nil {
		t.Fatalf("stats after coalesced guest ping: %v", err)
	}
	if stats.TotalGuestLogins != 1 || stats.TotalGuestLogouts != 0 {
		t.Fatalf("guest stats after coalesced ping = %+v", stats)
	}
	if err := store.SetGuestPresence("guest-web", "offline", "lobby", "203.0.113.25", base+20_000); err != nil {
		t.Fatalf("set offline guest presence: %v", err)
	}
	stats, err = store.Stats()
	if err != nil {
		t.Fatalf("stats after guest logout: %v", err)
	}
	if stats.OnlineGuests != 0 || stats.TotalGuestLogins != 1 || stats.TotalGuestLogouts != 1 {
		t.Fatalf("guest stats after logout = %+v", stats)
	}
}

func TestJetStreamPresenceStoreBacksCommandPresenceAndOnlineReads(t *testing.T) {
	store := newJetStreamPresenceStoreWithKV(newFakeCounterStoreKV())
	c, err := core.New(filepath.Join(t.TempDir(), "nats-presence-store.db"), core.WithPresenceStore(store))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice := registerNATSCounterStoreUser(t, c, "alice")
	bob := registerNATSCounterStoreUser(t, c, "bob")
	execNATSCounterStoreCommand(t, c, alice, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "friend",
		Active: true,
	})
	execNATSCounterStoreCommand(t, c, bob, proto.CmdSetUserRelationship, proto.SetUserRelationshipPayload{
		User:   "alice",
		Kind:   "friend",
		Active: true,
	})
	execNATSCounterStoreCommand(t, c, alice, proto.CmdSetLoginWatch, proto.SetLoginWatchPayload{
		User:   "bob",
		Active: true,
	})
	execNATSCounterStoreCommand(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{
		Status:    "active",
		SessionID: "web",
		Mode:      "reading",
		Board:     "general",
		Location:  "General",
		FromHost:  "203.0.113.9",
	})

	globalOnline, err := c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatalf("list global online: %v", err)
	}
	if len(globalOnline) != 1 || globalOnline[0].Name != "bob" || globalOnline[0].Status != "active" || globalOnline[0].Mode != "reading" || globalOnline[0].BoardID != "general" {
		t.Fatalf("expected bob online from NATS KV presence store, got %+v", globalOnline)
	}
	if globalOnline[0].SessionID != "web" || globalOnline[0].LocationLabel != "General" || globalOnline[0].FromHost != "203.0.113.9" {
		t.Fatalf("expected rich NATS KV presence metadata, got %+v", globalOnline[0])
	}
	boardOnline, err := c.ListOnlineUsers(alice.ID, "general", 10, 0)
	if err != nil {
		t.Fatalf("list board online: %v", err)
	}
	if len(boardOnline) != 1 || boardOnline[0].Name != "bob" || boardOnline[0].BoardName != "General" {
		t.Fatalf("expected bob in board-scoped NATS KV online list, got %+v", boardOnline)
	}
	execNATSCounterStoreCommand(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{
		Status:    "active",
		SessionID: "web",
		Mode:      "chat",
		Location:  "lobby",
	})
	chatRoster, err := c.ListChatOnlineUsers(alice.ID, "lobby", 10, 0)
	if err != nil {
		t.Fatalf("list chat roster: %v", err)
	}
	if len(chatRoster) != 1 || chatRoster[0].Name != "bob" || chatRoster[0].Kind != "chat" || chatRoster[0].Mode != "chat" || chatRoster[0].LocationLabel != "lobby" {
		t.Fatalf("expected bob in NATS KV chat roster, got %+v", chatRoster)
	}
	chatRooms, err := c.ListChatRooms()
	if err != nil {
		t.Fatalf("list chat rooms: %v", err)
	}
	var lobbyRoom *core.ChatRoom
	for i := range chatRooms {
		if chatRooms[i].ID == "lobby" {
			lobbyRoom = &chatRooms[i]
			break
		}
	}
	if lobbyRoom == nil || lobbyRoom.OnlineUsers != 1 {
		t.Fatalf("expected NATS KV chat online count in lobby, got %+v", chatRooms)
	}
	notifs, err := c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifs) != 1 || notifs[0].Kind != "login" || notifs[0].Actor != "bob" {
		t.Fatalf("expected NATS KV visible presence to satisfy login watch, got %+v", notifs)
	}
	assertNATSCounterSQLRows(t, c, "user_presence_sessions", 0)
	assertNATSCounterSQLRows(t, c, "user_presence", 0)

	execNATSCounterStoreCommand(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{
		Status:    "typing",
		SessionID: "web",
		Mode:      "replying",
		Board:     "general",
		Location:  "Reply box",
	})
	globalOnline, err = c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatalf("list after typing: %v", err)
	}
	if len(globalOnline) != 1 || globalOnline[0].Status != "active" || globalOnline[0].Mode != "chat" || globalOnline[0].LocationLabel != "lobby" {
		t.Fatalf("typing should not overwrite NATS KV roster, got %+v", globalOnline)
	}

	execNATSCounterStoreCommand(t, c, bob, proto.CmdSetPresence, proto.SetPresencePayload{
		Status:    "invisible",
		SessionID: "web",
		Board:     "general",
		Location:  "Hidden",
	})
	globalOnline, err = c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatalf("list after invisible: %v", err)
	}
	if len(globalOnline) != 0 {
		t.Fatalf("invisible NATS KV presence should be hidden, got %+v", globalOnline)
	}

	guestAt := time.Now().UTC()
	if err := c.SetGuestPresence("guest-web", "active", "lobby", "203.0.113.25", guestAt); err != nil {
		t.Fatalf("set guest presence: %v", err)
	}
	stats, err := c.GetCommunityStats()
	if err != nil {
		t.Fatalf("community stats after guest presence: %v", err)
	}
	if stats.OnlineGuests != 1 || stats.TotalGuestLogins != 1 || stats.TotalGuestLogouts != 0 {
		t.Fatalf("community stats after guest login = %+v", stats)
	}
	if err := c.SetGuestPresence("guest-web", "offline", "lobby", "203.0.113.25", guestAt.Add(time.Second)); err != nil {
		t.Fatalf("set guest offline: %v", err)
	}
	stats, err = c.GetCommunityStats()
	if err != nil {
		t.Fatalf("community stats after guest offline: %v", err)
	}
	if stats.OnlineGuests != 0 || stats.TotalGuestLogins != 1 || stats.TotalGuestLogouts != 1 {
		t.Fatalf("community stats after guest logout = %+v", stats)
	}
	assertNATSCounterSQLRows(t, c, "guest_presence_sessions", 0)
	var guestTotalsRows int
	if err := c.DB.QueryRow(`SELECT COUNT(*) FROM community_counter_totals WHERE total_guest_logins<>0 OR total_guest_logouts<>0`).Scan(&guestTotalsRows); err != nil {
		t.Fatalf("count SQL guest totals: %v", err)
	}
	if guestTotalsRows != 0 {
		t.Fatalf("NATS KV guest presence wrote %d SQL guest counter rows, want 0", guestTotalsRows)
	}
}

func assertJetStreamPresenceRecord(t *testing.T, store *JetStreamPresenceStore, userID, sessionID string) jetStreamPresenceSessionRecord {
	t.Helper()
	record, _, found, err := store.readPresenceSessionRecord(jetStreamPresenceSessionKey(userID, sessionID))
	if err != nil {
		t.Fatalf("read presence record: %v", err)
	}
	if !found {
		t.Fatalf("presence record %s/%s not found", userID, sessionID)
	}
	return record
}

func assertJetStreamGuestPresenceRecord(t *testing.T, store *JetStreamPresenceStore, sessionID string) jetStreamGuestPresenceSessionRecord {
	t.Helper()
	record, _, found, err := store.readGuestPresenceSessionRecord(jetStreamGuestPresenceSessionKey(sessionID))
	if err != nil {
		t.Fatalf("read guest presence record: %v", err)
	}
	if !found {
		t.Fatalf("guest presence record %s not found", sessionID)
	}
	return record
}
