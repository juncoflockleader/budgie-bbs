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

func TestNormalizeBrokerEventTransactionRecordsPreparesPendingEvents(t *testing.T) {
	record := brokerEventIdentityRecord(1000)
	record.ID = " evt_pending "
	record.PartitionOffset = 99
	record.PartitionKind = ""
	record.PartitionKey = ""

	records, err := NormalizeBrokerEventTransactionRecords([]BrokerEventRecord{record}, "")
	if err != nil {
		t.Fatalf("NormalizeBrokerEventTransactionRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	got := records[0]
	if got.ID != "evt_pending" || got.PartitionKind != PartitionGlobal || got.PartitionKey != PartitionGlobal || got.PartitionOffset != 0 {
		t.Fatalf("normalized record = %+v, want trimmed id, global partition, pending offset", got)
	}
}

func TestNormalizeBrokerEventTransactionRecordsRejectsDuplicateIDs(t *testing.T) {
	record := brokerEventIdentityRecord(1000)

	_, err := NormalizeBrokerEventTransactionRecords([]BrokerEventRecord{record, record}, "one batch")
	if err == nil || err.Error() != `duplicate event id "evt_identity_ts" in one batch` {
		t.Fatalf("duplicate err = %v", err)
	}

	conflict := record
	conflict.TS = 2000
	_, err = NormalizeBrokerEventTransactionRecords([]BrokerEventRecord{record, conflict}, "one batch")
	if err == nil || err.Error() != `duplicate event id "evt_identity_ts" has different content` {
		t.Fatalf("conflict err = %v", err)
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
