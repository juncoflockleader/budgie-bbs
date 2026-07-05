package natsconn

import (
	"context"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

var _ core.EventPartitionOffsetLister = (*JetStreamEventLogClient)(nil)

func TestJetStreamEventLogClientExposesPartitionOffsetListing(t *testing.T) {
	var client *JetStreamEventLogClient
	_, err := client.ListEventPartitionOffsets(context.Background(), 0)
	requireErrorContains(t, err, "nil client")
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
