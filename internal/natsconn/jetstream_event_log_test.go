package natsconn

import (
	"context"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

var _ core.EventPartitionOffsetLister = (*JetStreamEventLogClient)(nil)

func TestJetStreamEventLogClientExposesPartitionOffsetListing(t *testing.T) {
	var client *JetStreamEventLogClient
	_, err := client.ListEventPartitionOffsets(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "nil client") {
		t.Fatalf("ListEventPartitionOffsets nil client err = %v, want nil client", err)
	}
}

func TestJetStreamEventLogClientRemembersKnownStreamSequence(t *testing.T) {
	client := &JetStreamEventLogClient{}
	partition := core.LogPartition{Kind: "board", Key: "general"}.Normalize()

	if got := client.knownStreamSequenceAfterOffset(partition, 10); got != 0 {
		t.Fatalf("known stream sequence before remember = %d, want scan fallback", got)
	}
	client.rememberEventPosition(partition, 10, 42)
	if got := client.knownStreamSequenceAfterOffset(partition, 10); got != 43 {
		t.Fatalf("known stream sequence after offset 10 = %d, want 43", got)
	}
	if got := client.knownStreamSequenceAfterOffset(partition, 9); got != 0 {
		t.Fatalf("known stream sequence after offset 9 = %d, want scan fallback", got)
	}
	client.rememberEventPosition(partition, 9, 41)
	if got := client.knownStreamSequenceAfterOffset(partition, 10); got != 43 {
		t.Fatalf("known stream sequence after older remember = %d, want latest offset preserved", got)
	}
}

func TestJetStreamEventIdentityIncludesTimestamp(t *testing.T) {
	first := testJetStreamEventIdentityRecord(1000)
	second := first
	second.TS = 2000

	if sameJetStreamEventIdentity(first, second) {
		t.Fatalf("sameJetStreamEventIdentity accepted timestamp drift")
	}
}

func testJetStreamEventIdentityRecord(ts int64) core.BrokerEventRecord {
	return core.BrokerEventRecord{
		Version:       1,
		ID:            "evt_nats_identity_ts",
		Kind:          proto.EvtThreadNew,
		Scopes:        []string{"board:general"},
		Payload:       []byte(`{"id":"thr_nats_identity_ts","board":"general","author":"alice","title":"Timestamp","ts":1000}`),
		TS:            ts,
		PartitionKind: "board",
		PartitionKey:  "general",
	}
}
