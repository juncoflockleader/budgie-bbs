package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// TestNativeAutomodEmitsActionEvents confirms the native command-log decider
// evaluates automod rules and emits the action event (parity with the legacy
// handler path).
func TestNativeAutomodEmitsActionEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	go c.Run(ctx)
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatal(err)
	}

	mustExec := func(actor *User, cmd proto.CommandName, payload any) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		if r := c.ExecCmd(ctx, actor, cmd, raw, ""); r.Err != nil {
			t.Fatalf("%s: %+v", cmd, r.Err)
		}
	}
	mustExec(admin, proto.CmdCreateBoard, proto.CreateBoardPayload{ID: "garden", Name: "Garden"})
	mustExec(admin, proto.CmdSetBoardAutomodRule, proto.SetBoardAutomodRulePayload{
		Board: "garden", MatchType: "keyword", Pattern: "spam", Action: "redact", Reason: "no spam",
	})

	executor := NewCommandLogNativeDecisionExecutor(c)
	payload, _ := json.Marshal(proto.CreateThreadPayload{Board: "garden", Title: "hi", Body: "cheap spam here"})
	rec := CommandLogRecord{
		Partition: LogPartition{Kind: partitionBoard, Key: "garden"},
		Offset:    1, ActorID: alice.ID, CID: "automod-native-1",
		Command: proto.CmdCreateThread, Payload: payload, EnqueuedAt: 1000,
	}
	reply := executor.ExecuteCommandLogRecord(ctx, rec)
	if reply.Err != nil {
		t.Fatalf("decide createThread: %+v", reply.Err)
	}
	events, err := executor.DecideCommandLogEvents(ctx, rec, reply)
	if err != nil {
		t.Fatalf("decide events: %v", err)
	}
	hasRedact := false
	for _, e := range events {
		if e.Kind == proto.EvtPostRedacted {
			hasRedact = true
		}
	}
	if !hasRedact {
		t.Fatalf("native createThread with spam should emit EvtPostRedacted; got %d events", len(events))
	}
}
