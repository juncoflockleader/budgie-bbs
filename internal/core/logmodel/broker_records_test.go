package logmodel

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestSameBrokerCommandRecordIdentityIncludesEnqueuedAt(t *testing.T) {
	first := brokerCommandIdentityRecord(1000)
	second := first
	second.EnqueuedAt = 2000

	if SameBrokerCommandRecordIdentity(first, second) {
		t.Fatalf("SameBrokerCommandRecordIdentity accepted enqueue time drift")
	}
}

func TestSameBrokerEventRecordIdentityIncludesTimestamp(t *testing.T) {
	first := brokerEventIdentityRecord(1000)
	second := first
	second.TS = 2000

	if SameBrokerEventRecordIdentity(first, second) {
		t.Fatalf("SameBrokerEventRecordIdentity accepted timestamp drift")
	}
}

func brokerCommandIdentityRecord(enqueuedAt int64) BrokerCommandRecord {
	return BrokerCommandRecord{
		Version:       BrokerCommandRecordVersion,
		ActorID:       "usr_alice",
		CID:           "cid_identity_enqueued_at",
		Command:       proto.CmdCreateThread,
		Payload:       []byte(`{"board":"general","title":"Timestamp"}`),
		EnqueuedAt:    enqueuedAt,
		PartitionKind: PartitionBoard,
		PartitionKey:  "general",
	}
}

func brokerEventIdentityRecord(ts int64) BrokerEventRecord {
	return BrokerEventRecord{
		Version:       BrokerEventRecordVersion,
		ID:            "evt_identity_ts",
		Kind:          proto.EvtThreadNew,
		Scopes:        []string{"board:general"},
		Payload:       []byte(`{"id":"thr_identity_ts","board":"general","author":"alice","title":"Timestamp","ts":1000}`),
		TS:            ts,
		PartitionKind: PartitionBoard,
		PartitionKey:  "general",
	}
}
