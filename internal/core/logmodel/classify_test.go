package logmodel

import (
	"encoding/json"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestCommandPartitionMatchesAppendPostTargetAcceptsBaseAndSplitPartitions(t *testing.T) {
	payload := json.RawMessage(`{"thread":"thr_hot","body":"hello"}`)
	for _, partition := range []Partition{
		{Kind: PartitionThread, Key: "thr_hot"},
		{Kind: PartitionThread, Key: "thr_hot#reply-0"},
		{Kind: PartitionThread, Key: "thr_hot#reply-99"},
	} {
		if !CommandPartitionMatchesAppendPostTarget(proto.CmdAppendPost, payload, partition) {
			t.Fatalf("partition %+v should match appendPost target", partition)
		}
	}
}

func TestCommandPartitionMatchesAppendPostTargetRejectsWrongTargets(t *testing.T) {
	payload := json.RawMessage(`{"thread":"thr_hot","body":"hello"}`)
	cases := []struct {
		name      string
		command   proto.CommandName
		payload   json.RawMessage
		partition Partition
	}{
		{
			name:      "non append command",
			command:   proto.CmdCreateThread,
			payload:   payload,
			partition: Partition{Kind: PartitionThread, Key: "thr_hot"},
		},
		{
			name:      "wrong kind",
			command:   proto.CmdAppendPost,
			payload:   payload,
			partition: Partition{Kind: PartitionBoard, Key: "thr_hot"},
		},
		{
			name:      "wrong thread",
			command:   proto.CmdAppendPost,
			payload:   payload,
			partition: Partition{Kind: PartitionThread, Key: "thr_other#reply-0"},
		},
		{
			name:      "missing thread",
			command:   proto.CmdAppendPost,
			payload:   json.RawMessage(`{"body":"hello"}`),
			partition: Partition{Kind: PartitionThread, Key: "thr_hot"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if CommandPartitionMatchesAppendPostTarget(tc.command, tc.payload, tc.partition) {
				t.Fatalf("partition %+v unexpectedly matched", tc.partition)
			}
		})
	}
}

func TestCommandBypassesCommandLogCoversUnorderedCommands(t *testing.T) {
	for _, name := range []proto.CommandName{
		proto.CmdSendChatLine,
		proto.CmdSetPresence,
		proto.CmdReactPost,
		proto.CmdUnreactPost,
		proto.CmdVotePoll,
		proto.CmdMarkBoardRead,
		proto.CmdRestoreBoardRead,
		proto.CmdMarkFavoriteFolderRead,
		proto.CmdRestoreFavoriteFolderRead,
		proto.CmdMarkThreadRead,
		proto.CmdRestoreThreadRead,
		proto.CmdMarkPostRead,
		proto.CmdSetThreadPref,
		proto.CmdSubscribe,
		proto.CmdUnsubscribe,
	} {
		if !CommandBypassesCommandLog(name) {
			t.Fatalf("%s should bypass the command log", name)
		}
	}
}

func TestCommandBypassesCommandLogKeepsOrderedCommandsOnLog(t *testing.T) {
	for _, name := range []proto.CommandName{
		proto.CmdCreateThread,
		proto.CmdAppendPost,
		proto.CmdEditPost,
		proto.CmdPublishStatsSnapshot,
	} {
		if CommandBypassesCommandLog(name) {
			t.Fatalf("%s should stay on the command log", name)
		}
	}
}
