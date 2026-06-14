package natsconn

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestJetStreamCommandIdentityIncludesEnqueuedAt(t *testing.T) {
	first := testJetStreamCommandIdentityRecord(1000)
	second := first
	second.EnqueuedAt = 2000

	if sameJetStreamCommandIdentity(first, second) {
		t.Fatalf("sameJetStreamCommandIdentity accepted enqueue time drift")
	}
}

func testJetStreamCommandIdentityRecord(enqueuedAt int64) core.BrokerCommandRecord {
	return core.BrokerCommandRecord{
		Version:       1,
		ActorID:       "usr_alice",
		CID:           "cid_nats_identity_enqueued_at",
		Command:       proto.CmdCreateThread,
		Payload:       []byte(`{"board":"general","title":"Timestamp"}`),
		EnqueuedAt:    enqueuedAt,
		PartitionKind: "board",
		PartitionKey:  "general",
	}
}
