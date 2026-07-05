package core

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func TestNativeCommandDecisionCoversOrderedHandlerCommands(t *testing.T) {
	commandNames := parseNativeDecisionTestCommandNames(t)
	routed := parseNativeDecisionTestCommandCases(t, filepath.Join("handler", "route.go"), "route")
	native := parseNativeDecisionTestCommandCases(t, "command_log_native_decider.go", "decide")

	missing := []string{}
	for ident := range routed {
		name, ok := commandNames[ident]
		if !ok {
			t.Fatalf("handler route references %s, but proto command constant was not found", ident)
		}
		if logmodel.CommandBypassesCommandLog(name) {
			continue
		}
		if _, ok := native[ident]; !ok {
			missing = append(missing, fmt.Sprintf("%s (%s)", ident, name))
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("ordered handler commands missing native decision cases: %s", strings.Join(missing, ", "))
	}
}

func TestNativeCommandDecisionUnsupportedCommandsBypassCommandLog(t *testing.T) {
	commandNames := parseNativeDecisionTestCommandNames(t)
	native := parseNativeDecisionTestCommandCases(t, "command_log_native_decider.go", "decide")

	missing := []string{}
	for ident, name := range commandNames {
		if _, ok := native[ident]; ok {
			continue
		}
		if logmodel.CommandBypassesCommandLog(name) {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s (%s)", ident, name))
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("commands must have native decision cases or explicit command-log bypass: %s", strings.Join(missing, ", "))
	}
}

func parseNativeDecisionTestCommandNames(t *testing.T) map[string]proto.CommandName {
	t.Helper()
	file := parseNativeDecisionTestFile(t, filepath.Join("..", "proto", "command.go"))
	commands := map[string]proto.CommandName{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range values.Names {
				if !strings.HasPrefix(name.Name, "Cmd") {
					continue
				}
				if i >= len(values.Values) {
					t.Fatalf("%s has no explicit command value", name.Name)
				}
				lit, ok := values.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s has non-string command value", name.Name)
				}
				raw, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", name.Name, err)
				}
				commands[name.Name] = proto.CommandName(raw)
			}
		}
	}
	return commands
}

func parseNativeDecisionTestCommandCases(t *testing.T, path string, functionName string) map[string]struct{} {
	t.Helper()
	file := parseNativeDecisionTestFile(t, path)
	cases := map[string]struct{}{}
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != functionName || fn.Body == nil {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				selector, ok := expr.(*ast.SelectorExpr)
				if !ok || !strings.HasPrefix(selector.Sel.Name, "Cmd") {
					continue
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || pkg.Name != "proto" {
					continue
				}
				cases[selector.Sel.Name] = struct{}{}
			}
			return true
		})
	}
	if !found {
		t.Fatalf("function %s not found in %s", functionName, path)
	}
	return cases
}

func parseNativeDecisionTestFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func registerNativeDecisionTestUser(t *testing.T, c *Core, name string) *User {
	t.Helper()
	user, err := c.RegisterUser(name, "pw")
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return user
}

func newNativeDecisionTestHarness(c *Core) (*BrokerCommandLog, *BrokerEventStore, *CommandLogNativeDecisionExecutor, *CommandLogWorker) {
	harness := newBrokerCommandEventTestHarness()
	executor := NewCommandLogNativeDecisionExecutor(c)
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       harness.commandLog,
		BatchSize: 10,
		Executor:  executor,
		Finalizer: CommandEventTransactionFinalizer{
			Transactions: harness.transactionStore,
			Events:       executor,
		},
	})
	return harness.commandLog, harness.eventStore, executor, worker
}

func materializeNativeDecisionPartition(t *testing.T, ctx context.Context, c *Core, store EventStore, source string, partition LogPartition, limit int, label string) EventStorePartitionMaterializationResult {
	t.Helper()
	result, err := c.MaterializeEventStorePartition(ctx, store, EventStorePartitionMaterializationConfig{
		Source:    source,
		Partition: partition,
		Limit:     limit,
	})
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return result
}

func requireNativeDecisionPayload[T any](t *testing.T, event any, label string) *T {
	t.Helper()
	var raw any
	switch event := event.(type) {
	case *proto.Event:
		raw = event.Payload
	case EventAppend:
		raw = event.Payload
	default:
		t.Fatalf("%s event = %T, want payload-bearing event", label, event)
	}
	payload, ok := raw.(*T)
	if !ok {
		t.Fatalf("%s = %T, want %T", label, raw, (*T)(nil))
	}
	return payload
}

func requireNativeDecisionEventKinds(t *testing.T, events any, label string, kinds ...proto.EventKind) {
	t.Helper()
	want := fmt.Sprintf("event kinds %v", kinds)
	var gotLen int
	var kindAt func(int) proto.EventKind
	switch events := events.(type) {
	case []*proto.Event:
		gotLen = len(events)
		kindAt = func(i int) proto.EventKind { return events[i].Kind }
	case []EventAppend:
		gotLen = len(events)
		kindAt = func(i int) proto.EventKind { return events[i].Kind }
	default:
		t.Fatalf("%s = %+v, want %s", label, events, want)
	}
	if gotLen != len(kinds) {
		t.Fatalf("%s = %+v, want %s", label, events, want)
	}
	for i, kind := range kinds {
		if kindAt(i) != kind {
			t.Fatalf("%s = %+v, want %s", label, events, want)
		}
	}
}

func requireNativeDecisionTerminalError(t *testing.T, reply commandexec.Reply, code string, label string, messages ...string) {
	t.Helper()
	if reply.Err == nil || reply.Err.Code != code || reply.Err.Retryable {
		t.Fatalf("%s = %+v, want terminal %s", label, reply, code)
	}
	if len(messages) != 0 && reply.Err.Message != messages[0] {
		t.Fatalf("%s = %+v, want terminal %s with message %q", label, reply, code, messages[0])
	}
}

func produceNativeDecisionCommand(t *testing.T, ctx context.Context, commandLog *BrokerCommandLog, label string, record CommandLogRecord) CommandLogRecord {
	t.Helper()
	produced, err := commandLog.Produce(ctx, record)
	if err != nil {
		t.Fatalf("produce %s command: %v", label, err)
	}
	return produced
}

func produceDrainNativeDecisionCommand(t *testing.T, ctx context.Context, commandLog *BrokerCommandLog, worker *CommandLogWorker, label string, record CommandLogRecord) CommandLogRecord {
	t.Helper()
	produced := produceNativeDecisionCommand(t, ctx, commandLog, label, record)
	drainCommandLogWorkerOnce(t, ctx, worker, label)
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, produced.Partition, produced.Offset, label)
	return produced
}

func produceDrainReplayNativeDecisionEvents(t *testing.T, ctx context.Context, commandLog *BrokerCommandLog, eventStore EventStore, worker *CommandLogWorker, label string, replayPartition LogPartition, after int64, limit int, record CommandLogRecord) (CommandLogRecord, []*proto.Event) {
	t.Helper()
	produced := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, label, record)
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, replayPartition, after, limit, "replay "+label+" events")
	return produced, events
}

func produceDrainReplayNativeDecisionCommand(t *testing.T, ctx context.Context, commandLog *BrokerCommandLog, eventStore EventStore, worker *CommandLogWorker, label string, after int64, limit int, record CommandLogRecord) (CommandLogRecord, []*proto.Event) {
	t.Helper()
	return produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, label, record.Partition, after, limit, record)
}

func totalCommandLogWorkerResults(results []CommandLogWorkerResult) (processed int, applied int) {
	for _, result := range results {
		processed += result.Processed
		applied += result.Applied
	}
	return processed, applied
}

func TestNativeCommandLogDecisionExecutorProjectsCreateBoard(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "clubs"}
	payload := marshalCoreTestJSON(t, "marshal create board payload", proto.CreateBoardPayload{
		ID:          "clubs",
		Name:        " Clubs ",
		Description: "Campus clubs",
		ParentID:    " general ",
	})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-create-board-forbidden",
		Command:    proto.CmdCreateBoard,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "create board as non-admin")
	missingParentPayload := marshalCoreTestJSON(t, "marshal missing parent payload", proto.CreateBoardPayload{
		ID:       "orphan",
		Name:     "Orphan",
		ParentID: "missing",
	})
	missingParent := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "orphan"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-create-board-missing-parent",
		Command:    proto.CmdCreateBoard,
		Payload:    missingParentPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missingParent, proto.ErrNotFound, "create board missing parent")

	record, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create board", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-create-board",
		Command:    proto.CmdCreateBoard,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "create board events", proto.EvtBoardCreated)
	boardEvent := requireNativeDecisionPayload[proto.BoardCreatedPayload](t, events[0], "create board payload")
	if boardEvent.ID != "clubs" || boardEvent.Name != "Clubs" || boardEvent.Description != "Campus clubs" ||
		boardEvent.ParentID != "general" || boardEvent.Position != 0 || boardEvent.By != admin.ID || boardEvent.TS != 2234 {
		t.Fatalf("create board event = %+v, want deterministic child board", boardEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-create-board-test", boardPartition, 10, "materialize create board event")
	board, err := c.GetBoard("clubs")
	if err != nil {
		t.Fatalf("get projected board: %v", err)
	}
	if board == nil || board.Name != "Clubs" || board.Description != "Campus clubs" {
		t.Fatalf("projected board = %+v, want native-created board", board)
	}
	categories, err := c.ListCategories()
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	var projectedCategory *Category
	for i := range categories {
		if categories[i].ID == "clubs" {
			projectedCategory = &categories[i]
			break
		}
	}
	if projectedCategory == nil || projectedCategory.ParentID != "general" || projectedCategory.Position != 0 {
		t.Fatalf("projected category = %+v, want clubs under general at position 0", projectedCategory)
	}

	retryReply := executor.ExecuteCommandLogRecord(ctx, record)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "clubs" {
		t.Fatalf("create board retry reply = %+v, want idempotent success after projection", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, record, retryReply)
	if err != nil {
		t.Fatalf("decide create board retry events: %v", err)
	}
	if len(retryEvents) != 1 || retryEvents[0].ID != stableCommandLogDecisionID("evt_", record, 0) ||
		retryEvents[0].Kind != proto.EvtBoardCreated {
		t.Fatalf("create board retry events = %+v, want stable board.created", retryEvents)
	}
	retryBoard := requireNativeDecisionPayload[proto.BoardCreatedPayload](t, retryEvents[0], "create board retry payload")
	if retryBoard.ID != boardEvent.ID || retryBoard.Name != boardEvent.Name || retryBoard.ParentID != boardEvent.ParentID ||
		retryBoard.Position != boardEvent.Position || retryBoard.Description != boardEvent.Description {
		t.Fatalf("create board retry payload = %+v, want stable payload %+v", retryBoard, boardEvent)
	}

	conflictPayload := marshalCoreTestJSON(t, "marshal conflicting create board payload", proto.CreateBoardPayload{
		ID:          "clubs",
		Name:        "Other Clubs",
		Description: "Changed",
		ParentID:    "general",
	})
	conflict := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     record.Offset + 1,
		ActorID:    admin.ID,
		CID:        "cid-native-create-board-conflict",
		Command:    proto.CmdCreateBoard,
		Payload:    conflictPayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, conflict, proto.ErrConflict, "conflicting create board")
}

func TestNativeCommandLogDecisionExecutorProjectsRecommendedBoard(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin board setup: %v", err)
	}
	if err := projections.InsertBoard(tx, "tech", "Tech", "Technology discussion", "", 1); err != nil {
		tx.Rollback()
		t.Fatalf("insert tech board: %v", err)
	}
	if err := projections.InsertBoard(tx, "secret", "Secret", "Members only", "", 2); err != nil {
		tx.Rollback()
		t.Fatalf("insert secret board: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit board setup: %v", err)
	}
	memberReadMode := true
	if err := projections.SetBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberReadMode}); err != nil {
		t.Fatalf("set secret board settings: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "tech"}
	payload := marshalCoreTestJSON(t, "marshal recommended board payload", proto.SetRecommendedBoardPayload{
		Board:       " tech ",
		Recommended: true,
		Note:        " Start here. ",
	})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-recommended-board-forbidden",
		Command:    proto.CmdSetRecommendedBoard,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "set recommended board as non-admin", "admin role required")
	longRecommendationNote := strings.Repeat("x", proto.MaxBoardNoteLength+1)
	longNotePayload := marshalCoreTestJSON(t, "marshal long recommended board note payload", proto.SetRecommendedBoardPayload{
		Board:       "tech",
		Recommended: true,
		Note:        longRecommendationNote,
	})
	longNote := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-recommended-board-long-note",
		Command:    proto.CmdSetRecommendedBoard,
		Payload:    longNotePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, longNote, proto.ErrValidationFailed, "set recommended board long note", validationMessage(longRecommendationNote, proto.NormalizeBoardRecommendationNote))
	secretPayload := marshalCoreTestJSON(t, "marshal secret recommended board payload", proto.SetRecommendedBoardPayload{
		Board:       "secret",
		Recommended: true,
	})
	secret := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "secret"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-recommended-board-secret",
		Command:    proto.CmdSetRecommendedBoard,
		Payload:    secretPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, secret, proto.ErrValidationFailed, "set recommended member-read board", "member-read boards cannot be publicly recommended")

	record, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "recommended board", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-recommended-board",
		Command:    proto.CmdSetRecommendedBoard,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "recommended board events", proto.EvtBoardRecommendedSet)
	recommendedEvent := requireNativeDecisionPayload[proto.BoardRecommendedSetPayload](t, events[0], "recommended board payload")
	if recommendedEvent.Board != "tech" || !recommendedEvent.Recommended || recommendedEvent.Note != "Start here." ||
		recommendedEvent.Position != 0 || recommendedEvent.CuratedBy != admin.ID || recommendedEvent.TS != 2234 {
		t.Fatalf("recommended board event = %+v, want deterministic recommendation", recommendedEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-recommended-board-test", partition, 10, "materialize recommended board event")
	recommended, err := c.ListRecommendedBoards(10, 0)
	if err != nil {
		t.Fatalf("list recommended boards: %v", err)
	}
	if len(recommended) != 1 || recommended[0].ID != "tech" || recommended[0].Note != "Start here." ||
		recommended[0].Position != 0 || recommended[0].CuratedBy != admin.ID {
		t.Fatalf("recommended boards = %+v, want projected tech recommendation", recommended)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, record)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "tech" {
		t.Fatalf("recommended board retry reply = %+v, want idempotent success after projection", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, record, retryReply)
	if err != nil {
		t.Fatalf("decide recommended board retry events: %v", err)
	}
	if len(retryEvents) != 1 || retryEvents[0].ID != stableCommandLogDecisionID("evt_", record, 0) ||
		retryEvents[0].Kind != proto.EvtBoardRecommendedSet {
		t.Fatalf("recommended board retry events = %+v, want stable recommendation event", retryEvents)
	}
	retryRecommended := requireNativeDecisionPayload[proto.BoardRecommendedSetPayload](t, retryEvents[0], "recommended board retry payload")
	if retryRecommended.Board != recommendedEvent.Board || retryRecommended.Position != recommendedEvent.Position ||
		retryRecommended.Note != recommendedEvent.Note || retryRecommended.CuratedBy != recommendedEvent.CuratedBy {
		t.Fatalf("recommended board retry payload = %+v, want stable payload %+v", retryRecommended, recommendedEvent)
	}

	clearPayload := marshalCoreTestJSON(t, "marshal clear recommended board payload", proto.SetRecommendedBoardPayload{
		Board:       "tech",
		Recommended: false,
	})
	clearRecord := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "clear recommended board", CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-recommended-board-clear",
		Command:    proto.CmdSetRecommendedBoard,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-recommended-board-test", partition, 10, "materialize clear recommended board event")
	recommended, err = c.ListRecommendedBoards(10, 0)
	if err != nil {
		t.Fatalf("list recommended boards after clear: %v", err)
	}
	if len(recommended) != 0 {
		t.Fatalf("recommended boards after clear = %+v, want empty list", recommended)
	}
	clearRetryReply := executor.ExecuteCommandLogRecord(ctx, clearRecord)
	if clearRetryReply.Err != nil || clearRetryReply.Result == nil || clearRetryReply.Result.ID != "tech" {
		t.Fatalf("clear recommended board retry reply = %+v, want idempotent success after deletion", clearRetryReply)
	}
	clearRetryEvents, err := executor.DecideCommandLogEvents(ctx, clearRecord, clearRetryReply)
	if err != nil {
		t.Fatalf("decide clear recommended board retry events: %v", err)
	}
	requireNativeDecisionEventKinds(t, clearRetryEvents, "clear recommended board retry events", proto.EvtBoardRecommendedSet)
	clearEvent := requireNativeDecisionPayload[proto.BoardRecommendedSetPayload](t, clearRetryEvents[0], "clear recommended board retry payload")
	if clearEvent.Board != "tech" || clearEvent.Recommended || clearEvent.Position != 0 ||
		clearEvent.CuratedBy != admin.ID || clearEvent.TS != 3234 {
		t.Fatalf("clear recommended board retry payload = %+v, want deterministic clear event", clearEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardSettings(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	readOnly := true
	mailInAllowed := true
	statsExcluded := true
	zapAllowed := false
	payload := marshalCoreTestJSON(t, "marshal board settings payload", proto.SetBoardSettingsPayload{
		Board:         " general ",
		ReadOnly:      &readOnly,
		MailInAllowed: &mailInAllowed,
		StatsExcluded: &statsExcluded,
		ZapAllowed:    &zapAllowed,
	})
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-board-settings-forbidden",
		Command:    proto.CmdSetBoardSettings,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "set board settings without permission", "board settings permission required")
	if _, err := qExec(c.DB,
		`INSERT INTO board_members (board_id, user_id, position, can_set_board_settings, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		"general", alice.ID, 10, int64(1234), int64(1234),
	); err != nil {
		t.Fatalf("grant delegated board-settings permission: %v", err)
	}

	record, boardEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board settings", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-settings",
		Command:    proto.CmdSetBoardSettings,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, boardEvents, "board settings events", proto.EvtBoardSettingsSet)
	settingsEvent := requireNativeDecisionPayload[proto.BoardSettingsSetPayload](t, boardEvents[0], "board settings payload")
	if settingsEvent.Board != "general" || !settingsEvent.ReadOnly || !settingsEvent.MailInAllowed ||
		!settingsEvent.StatsExcluded || settingsEvent.ZapAllowed || settingsEvent.By != alice.ID || settingsEvent.TS != 2234 {
		t.Fatalf("board settings event = %+v, want deterministic final settings", settingsEvent)
	}
	syssecurityPartition := LogPartition{Kind: partitionBoard, Key: proto.SyssecuritySystemBoardID}
	syssecurityEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, syssecurityPartition, 0, 10, "replay board settings syssecurity events")
	if len(syssecurityEvents) != 3 ||
		syssecurityEvents[0].Kind != proto.EvtBoardCreated ||
		syssecurityEvents[1].Kind != proto.EvtThreadNew ||
		syssecurityEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("board settings syssecurity events = %+v, want board/thread/post audit events", syssecurityEvents)
	}
	auditPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, syssecurityEvents[2], "board settings audit payload")
	for _, want := range []string{
		"Action: board settings changed",
		"Board: general",
		"Actor: alice",
		"readOnly: true",
		"mailInAllowed: true",
		"statsExcluded: true",
		"zapAllowed: false",
	} {
		if !strings.Contains(auditPost.Body, want) {
			t.Fatalf("board settings audit body missing %q:\n%s", want, auditPost.Body)
		}
	}

	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-settings-test", partition, 10, "materialize board settings event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-settings-test", syssecurityPartition, 10, "materialize board settings syssecurity events")
	settings, err := projections.GetBoardSettings(c.DB, "general")
	if err != nil {
		t.Fatalf("get projected board settings: %v", err)
	}
	if settings == nil || !settings.ReadOnly || !settings.MailInAllowed ||
		!settings.StatsExcluded || settings.ZapAllowed || settings.UpdatedAt != 2234 {
		t.Fatalf("projected board settings = %+v, want event final settings", settings)
	}
	syssecurityThreads, err := c.ListThreads(proto.SyssecuritySystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list syssecurity threads after board settings: %v", err)
	}
	if len(syssecurityThreads) != 1 {
		t.Fatalf("syssecurity threads after board settings = %+v, want one audit thread", syssecurityThreads)
	}

	retryReply := executor.ExecuteCommandLogRecord(ctx, record)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "general" {
		t.Fatalf("board settings retry reply = %+v, want idempotent success after projection", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, record, retryReply)
	if err != nil {
		t.Fatalf("decide board settings retry events: %v", err)
	}
	if len(retryEvents) != 3 ||
		retryEvents[0].ID != stableCommandLogDecisionID("evt_", record, 0) ||
		retryEvents[0].Kind != proto.EvtBoardSettingsSet ||
		retryEvents[1].ID != stableCommandLogDecisionID("evt_", record, 2) ||
		retryEvents[1].Kind != proto.EvtThreadNew ||
		retryEvents[2].ID != stableCommandLogDecisionID("evt_", record, 3) ||
		retryEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("board settings retry events = %+v, want stable settings plus existing syssecurity-board audit events", retryEvents)
	}
	retrySettings := requireNativeDecisionPayload[proto.BoardSettingsSetPayload](t, retryEvents[0], "board settings retry payload")
	if retrySettings.Board != settingsEvent.Board || retrySettings.ReadOnly != settingsEvent.ReadOnly ||
		retrySettings.MailInAllowed != settingsEvent.MailInAllowed ||
		retrySettings.StatsExcluded != settingsEvent.StatsExcluded ||
		retrySettings.ZapAllowed != settingsEvent.ZapAllowed ||
		retrySettings.By != settingsEvent.By || retrySettings.TS != settingsEvent.TS {
		t.Fatalf("board settings retry payload = %+v, want stable payload %+v", retrySettings, settingsEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardMemberRequirements(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	minPostCount := 4
	minBoardMarkCount := 2
	if err := projections.SetBoardMemberRequirements(c.DB, "general", BoardMemberRequirementsPatch{
		MinPostCount:      &minPostCount,
		MinBoardMarkCount: &minBoardMarkCount,
	}); err != nil {
		t.Fatalf("seed board member requirements: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	minLoginCount := 3
	minTrustLevel := 2
	minScore := 7
	minBoardDigestCount := 5
	maxMembers := 42
	approvalMode := " automatic "
	payload := marshalCoreTestJSON(t, "marshal board member requirements payload", proto.SetBoardMemberRequirementsPayload{
		Board:               " general ",
		MinLoginCount:       &minLoginCount,
		MinTrustLevel:       &minTrustLevel,
		MinScore:            &minScore,
		MinBoardDigestCount: &minBoardDigestCount,
		MaxMembers:          &maxMembers,
		ApprovalMode:        &approvalMode,
	})
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-board-member-requirements-forbidden",
		Command:    proto.CmdSetBoardMemberRequirements,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "set board member requirements without permission", "board settings permission required")
	if _, err := qExec(c.DB,
		`INSERT INTO board_members (board_id, user_id, position, can_set_board_settings, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		"general", alice.ID, 10, int64(1234), int64(1234),
	); err != nil {
		t.Fatalf("grant delegated board-settings permission: %v", err)
	}
	negativeMinScore := -1
	negativePayload := marshalCoreTestJSON(t, "marshal negative requirements payload", proto.SetBoardMemberRequirementsPayload{
		Board:    "general",
		MinScore: &negativeMinScore,
	})
	negative := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-requirements-negative",
		Command:    proto.CmdSetBoardMemberRequirements,
		Payload:    negativePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, negative, proto.ErrValidationFailed, "set negative board member requirement", "minScore must be non-negative")
	badApproval := "open"
	badApprovalPayload := marshalCoreTestJSON(t, "marshal bad approval mode payload", proto.SetBoardMemberRequirementsPayload{
		Board:        "general",
		ApprovalMode: &badApproval,
	})
	badMode := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-requirements-bad-mode",
		Command:    proto.CmdSetBoardMemberRequirements,
		Payload:    badApprovalPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, badMode, proto.ErrValidationFailed, "set invalid board member approval mode", validationMessage("maybe", proto.NormalizeBoardMemberApprovalMode))

	record, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board member requirements", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-requirements",
		Command:    proto.CmdSetBoardMemberRequirements,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "board member requirements events", proto.EvtBoardMemberRequirementsSet)
	requirementsEvent := requireNativeDecisionPayload[proto.BoardMemberRequirementsSetPayload](t, events[0], "board member requirements payload")
	if requirementsEvent.Board != "general" ||
		requirementsEvent.MinLoginCount != 3 ||
		requirementsEvent.MinPostCount != 4 ||
		requirementsEvent.MinTrustLevel != 2 ||
		requirementsEvent.MinScore != 7 ||
		requirementsEvent.MinBoardDigestCount != 5 ||
		requirementsEvent.MinBoardMarkCount != 2 ||
		requirementsEvent.MaxMembers != 42 ||
		requirementsEvent.ApprovalMode != "auto" ||
		requirementsEvent.By != alice.ID ||
		requirementsEvent.TS != 2234 {
		t.Fatalf("board member requirements event = %+v, want deterministic final requirements", requirementsEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-member-requirements-test", partition, 10, "materialize board member requirements event")
	requirements, err := c.GetBoardMemberRequirements("general")
	if err != nil {
		t.Fatalf("get projected board member requirements: %v", err)
	}
	if requirements == nil ||
		requirements.MinLoginCount != 3 ||
		requirements.MinPostCount != 4 ||
		requirements.MinTrustLevel != 2 ||
		requirements.MinScore != 7 ||
		requirements.MinBoardDigestCount != 5 ||
		requirements.MinBoardMarkCount != 2 ||
		requirements.MaxMembers != 42 ||
		requirements.ApprovalMode != "auto" ||
		requirements.UpdatedAt != 2234 {
		t.Fatalf("projected board member requirements = %+v, want event final requirements", requirements)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, record)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "general" {
		t.Fatalf("board member requirements retry reply = %+v, want idempotent success after projection", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, record, retryReply)
	if err != nil {
		t.Fatalf("decide board member requirements retry events: %v", err)
	}
	if len(retryEvents) != 1 ||
		retryEvents[0].ID != stableCommandLogDecisionID("evt_", record, 0) ||
		retryEvents[0].Kind != proto.EvtBoardMemberRequirementsSet {
		t.Fatalf("board member requirements retry events = %+v, want stable requirements event", retryEvents)
	}
	retryRequirements := requireNativeDecisionPayload[proto.BoardMemberRequirementsSetPayload](t, retryEvents[0], "board member requirements retry payload")
	if retryRequirements.Board != requirementsEvent.Board ||
		retryRequirements.MinLoginCount != requirementsEvent.MinLoginCount ||
		retryRequirements.MinPostCount != requirementsEvent.MinPostCount ||
		retryRequirements.MinTrustLevel != requirementsEvent.MinTrustLevel ||
		retryRequirements.MinScore != requirementsEvent.MinScore ||
		retryRequirements.MinBoardDigestCount != requirementsEvent.MinBoardDigestCount ||
		retryRequirements.MinBoardMarkCount != requirementsEvent.MinBoardMarkCount ||
		retryRequirements.MaxMembers != requirementsEvent.MaxMembers ||
		retryRequirements.ApprovalMode != requirementsEvent.ApprovalMode ||
		retryRequirements.By != requirementsEvent.By ||
		retryRequirements.TS != requirementsEvent.TS {
		t.Fatalf("board member requirements retry payload = %+v, want stable payload %+v", retryRequirements, requirementsEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardModerator(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin secret board setup: %v", err)
	}
	if err := projections.InsertBoard(tx, "secret", "Secret", "Private board", "", 10); err != nil {
		t.Fatalf("insert secret board: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit secret board: %v", err)
	}
	memberRead := true
	if err := projections.SetBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	position := 7
	appointPayload := marshalCoreTestJSON(t, "marshal board moderator appoint payload", proto.SetBoardModeratorPayload{
		Board:     " general ",
		User:      alice.Name,
		Moderator: true,
		Position:  &position,
	})
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-board-moderator-forbidden",
		Command:    proto.CmdSetBoardModerator,
		Payload:    appointPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "set board moderator as non-admin", "admin role required")
	missingPayload := marshalCoreTestJSON(t, "marshal missing moderator user payload", proto.SetBoardModeratorPayload{
		Board:     "general",
		User:      "missing",
		Moderator: true,
	})
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-board-moderator-missing",
		Command:    proto.CmdSetBoardModerator,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missing, proto.ErrNotFound, "set board moderator missing user", "user not found")

	appointRecord, boardEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board moderator appoint", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-moderator-appoint",
		Command:    proto.CmdSetBoardModerator,
		Payload:    appointPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, boardEvents, "board moderator appoint events", proto.EvtBoardModeratorSet)
	appointEvent := requireNativeDecisionPayload[proto.BoardModeratorSetPayload](t, boardEvents[0], "board moderator appoint payload")
	if appointEvent.Board != "general" || appointEvent.User != alice.ID || !appointEvent.Moderator ||
		appointEvent.Position != 7 || appointEvent.By != admin.ID || appointEvent.TS != 2234 {
		t.Fatalf("board moderator appoint event = %+v, want deterministic final moderator assignment", appointEvent)
	}
	syssecurityPartition := LogPartition{Kind: partitionBoard, Key: proto.SyssecuritySystemBoardID}
	syssecurityEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, syssecurityPartition, 0, 10, "replay board moderator syssecurity events")
	if len(syssecurityEvents) != 3 ||
		syssecurityEvents[0].Kind != proto.EvtBoardCreated ||
		syssecurityEvents[1].Kind != proto.EvtThreadNew ||
		syssecurityEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("board moderator syssecurity events = %+v, want board/thread/post audit events", syssecurityEvents)
	}
	appointAudit := requireNativeDecisionPayload[proto.PostAppendedPayload](t, syssecurityEvents[2], "board moderator appoint audit payload")
	for _, want := range []string{"Action: board moderator appointed", "Board: general", "User: alice", "Actor: admin"} {
		if !strings.Contains(appointAudit.Body, want) {
			t.Fatalf("board moderator appoint audit body missing %q:\n%s", want, appointAudit.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-moderator-test", partition, 10, "materialize board moderator appoint event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-moderator-test", syssecurityPartition, 10, "materialize board moderator appoint syssecurity events")
	info, err := c.GetBoardInfo("general")
	if err != nil {
		t.Fatalf("get general board info after appoint: %v", err)
	}
	if len(info.Moderators) != 1 || info.Moderators[0].UserID != alice.ID || info.Moderators[0].Position != 7 {
		t.Fatalf("general moderators after appoint = %+v, want alice at position 7", info.Moderators)
	}
	terms, err := c.ListBoardModeratorTerms("general", 10, 0)
	if err != nil {
		t.Fatalf("list moderator terms after appoint: %v", err)
	}
	if len(terms) != 1 || terms[0].UserID != alice.ID || terms[0].Position != 7 ||
		terms[0].StartedAt != 2234 || terms[0].EndedAt != 0 || terms[0].AppointedByID != admin.ID {
		t.Fatalf("moderator terms after appoint = %+v, want active alice term", terms)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, appointRecord)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "general" {
		t.Fatalf("board moderator appoint retry reply = %+v, want idempotent success after projection", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, appointRecord, retryReply)
	if err != nil {
		t.Fatalf("decide board moderator appoint retry events: %v", err)
	}
	if len(retryEvents) != 3 ||
		retryEvents[0].ID != stableCommandLogDecisionID("evt_", appointRecord, 0) ||
		retryEvents[0].Kind != proto.EvtBoardModeratorSet ||
		retryEvents[1].Kind != proto.EvtThreadNew ||
		retryEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("board moderator appoint retry events = %+v, want stable moderator event plus syssecurity thread/post", retryEvents)
	}
	retryAppoint := requireNativeDecisionPayload[proto.BoardModeratorSetPayload](t, retryEvents[0], "board moderator appoint retry payload")
	if retryAppoint.Board != appointEvent.Board || retryAppoint.User != appointEvent.User ||
		retryAppoint.Moderator != appointEvent.Moderator || retryAppoint.Position != appointEvent.Position ||
		retryAppoint.By != appointEvent.By || retryAppoint.TS != appointEvent.TS {
		t.Fatalf("board moderator appoint retry payload = %+v, want stable payload %+v", retryAppoint, appointEvent)
	}

	removePayload := marshalCoreTestJSON(t, "marshal board moderator remove payload", proto.SetBoardModeratorPayload{
		Board:     "general",
		User:      alice.ID,
		Moderator: false,
	})
	removeRecord, boardEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board moderator remove", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-moderator-remove",
		Command:    proto.CmdSetBoardModerator,
		Payload:    removePayload,
		EnqueuedAt: 3234,
	})
	if len(boardEvents) != 2 || boardEvents[1].Kind != proto.EvtBoardModeratorSet {
		t.Fatalf("board moderator events after remove = %+v, want appointment then removal", boardEvents)
	}
	removeEvent := requireNativeDecisionPayload[proto.BoardModeratorSetPayload](t, boardEvents[1], "board moderator remove payload")
	if removeEvent.Board != "general" || removeEvent.User != alice.ID || removeEvent.Moderator ||
		removeEvent.Position != 7 || removeEvent.By != admin.ID || removeEvent.TS != 3234 {
		t.Fatalf("board moderator remove event = %+v, want deterministic removal with prior position", removeEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-moderator-test", partition, 10, "materialize board moderator remove event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-moderator-test", syssecurityPartition, 10, "materialize board moderator remove syssecurity events")
	info, err = c.GetBoardInfo("general")
	if err != nil {
		t.Fatalf("get general board info after remove: %v", err)
	}
	if len(info.Moderators) != 0 {
		t.Fatalf("general moderators after remove = %+v, want empty", info.Moderators)
	}
	terms, err = c.ListBoardModeratorTerms("general", 10, 0)
	if err != nil {
		t.Fatalf("list moderator terms after remove: %v", err)
	}
	if len(terms) != 1 || terms[0].UserID != alice.ID || terms[0].Position != 7 ||
		terms[0].StartedAt != 2234 || terms[0].EndedAt != 3234 || terms[0].RemovedByID != admin.ID {
		t.Fatalf("moderator terms after remove = %+v, want closed alice term", terms)
	}
	removeRetryReply := executor.ExecuteCommandLogRecord(ctx, removeRecord)
	if removeRetryReply.Err != nil || removeRetryReply.Result == nil || removeRetryReply.Result.ID != "general" {
		t.Fatalf("board moderator remove retry reply = %+v, want idempotent success after projection", removeRetryReply)
	}
	removeRetryEvents, err := executor.DecideCommandLogEvents(ctx, removeRecord, removeRetryReply)
	if err != nil {
		t.Fatalf("decide board moderator remove retry events: %v", err)
	}
	if len(removeRetryEvents) != 3 || removeRetryEvents[0].Kind != proto.EvtBoardModeratorSet {
		t.Fatalf("board moderator remove retry events = %+v, want stable moderator removal plus syssecurity thread/post", removeRetryEvents)
	}
	removeRetry := requireNativeDecisionPayload[proto.BoardModeratorSetPayload](t, removeRetryEvents[0], "board moderator remove retry payload")
	if removeRetry.Position != removeEvent.Position || removeRetry.Moderator != removeEvent.Moderator ||
		removeRetry.User != removeEvent.User || removeRetry.TS != removeEvent.TS {
		t.Fatalf("board moderator remove retry payload = %+v, want stable removal %+v", removeRetry, removeEvent)
	}

	secretPayload := marshalCoreTestJSON(t, "marshal secret board moderator payload", proto.SetBoardModeratorPayload{
		Board:     "secret",
		User:      bob.Name,
		Moderator: true,
	})
	secretPartition := LogPartition{Kind: partitionBoard, Key: "secret"}
	_, secretEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "secret board moderator", 0, 10, CommandLogRecord{
		Partition:  secretPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-moderator-secret",
		Command:    proto.CmdSetBoardModerator,
		Payload:    secretPayload,
		EnqueuedAt: 4234,
	})
	requireNativeDecisionEventKinds(t, secretEvents, "secret board moderator events", proto.EvtBoardModeratorSet)
	syssecurityEvents = replayCommandLogWorkerPartition(t, ctx, eventStore, syssecurityPartition, 0, 10, "replay syssecurity after secret board moderator")
	if len(syssecurityEvents) != 5 {
		t.Fatalf("syssecurity events after private board moderator = %+v, want no new private-board audit", syssecurityEvents)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardMember(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	manager := true
	if err := projections.SetBoardMember(c.DB, "general", alice.ID, true, BoardMemberPatch{CanManageMembers: &manager}); err != nil {
		t.Fatalf("seed delegated board member manager: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	position := 3
	canCurate := true
	canSetSettings := true
	memberPayload := marshalCoreTestJSON(t, "marshal board member payload", proto.SetBoardMemberPayload{
		Board:               " general ",
		User:                bob.Name,
		Member:              true,
		Title:               " Curator ",
		Position:            &position,
		CanCurate:           &canCurate,
		CanSetBoardSettings: &canSetSettings,
	})
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-board-member-forbidden",
		Command:    proto.CmdSetBoardMember,
		Payload:    memberPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "set board member without manager permission", "board member manager permission required")
	managerDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-manager-denied-permissions",
		Command:    proto.CmdSetBoardMember,
		Payload:    memberPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, managerDenied, proto.ErrForbidden, "delegated manager changing permissions", "board moderator role required to change member permissions")
	longTitle := strings.Repeat("x", 81)
	longTitlePayload := marshalCoreTestJSON(t, "marshal long board member title payload", proto.SetBoardMemberPayload{
		Board:  "general",
		User:   bob.Name,
		Member: true,
		Title:  longTitle,
	})
	longTitleReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-board-member-long-title",
		Command:    proto.CmdSetBoardMember,
		Payload:    longTitlePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, longTitleReply, proto.ErrValidationFailed, "long board member title", "member title must be 80 characters or less")

	record, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board member add", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-member-add",
		Command:    proto.CmdSetBoardMember,
		Payload:    memberPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "board member events", proto.EvtBoardMemberSet)
	memberEvent := requireNativeDecisionPayload[proto.BoardMemberSetPayload](t, events[0], "board member payload")
	if memberEvent.Board != "general" || memberEvent.User != bob.ID || !memberEvent.Member ||
		memberEvent.Title != "Curator" || memberEvent.Position != 3 ||
		!memberEvent.CanCurate || !memberEvent.CanSetBoardSettings ||
		memberEvent.CanManageMembers || memberEvent.CanModeratePosts || memberEvent.CanModerateThreads ||
		memberEvent.CanAnnounce || memberEvent.CanManagePolls ||
		memberEvent.By != admin.ID || memberEvent.TS != 2234 {
		t.Fatalf("board member event = %+v, want deterministic final membership", memberEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-member-test", partition, 10, "materialize board member add event")
	info, err := c.GetBoardInfo("general")
	if err != nil {
		t.Fatalf("get board info after member add: %v", err)
	}
	var projectedBob *projections.BoardMember
	for i := range info.Members {
		if info.Members[i].UserID == bob.ID {
			projectedBob = &info.Members[i]
			break
		}
	}
	if projectedBob == nil || projectedBob.Title != "Curator" || projectedBob.Position != 3 ||
		!projectedBob.CanCurate || !projectedBob.CanSetBoardSettings {
		t.Fatalf("projected bob member = %+v in %+v, want curator membership", projectedBob, info.Members)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, record)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "general" {
		t.Fatalf("board member add retry reply = %+v, want idempotent success after projection", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, record, retryReply)
	if err != nil {
		t.Fatalf("decide board member add retry events: %v", err)
	}
	if len(retryEvents) != 1 || retryEvents[0].ID != stableCommandLogDecisionID("evt_", record, 0) ||
		retryEvents[0].Kind != proto.EvtBoardMemberSet {
		t.Fatalf("board member add retry events = %+v, want stable board.member_set", retryEvents)
	}
	retryMember := requireNativeDecisionPayload[proto.BoardMemberSetPayload](t, retryEvents[0], "board member add retry payload")
	if retryMember.Board != memberEvent.Board || retryMember.User != memberEvent.User ||
		retryMember.Member != memberEvent.Member || retryMember.Title != memberEvent.Title ||
		retryMember.Position != memberEvent.Position ||
		retryMember.CanCurate != memberEvent.CanCurate ||
		retryMember.CanSetBoardSettings != memberEvent.CanSetBoardSettings ||
		retryMember.By != memberEvent.By || retryMember.TS != memberEvent.TS {
		t.Fatalf("board member add retry payload = %+v, want stable payload %+v", retryMember, memberEvent)
	}

	managerDeniedPrivilegedRemovePayload := marshalCoreTestJSON(t, "marshal delegated remove payload", proto.SetBoardMemberPayload{
		Board:  "general",
		User:   bob.Name,
		Member: false,
	})
	managerDeniedPrivileged := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-manager-denied-privileged",
		Command:    proto.CmdSetBoardMember,
		Payload:    managerDeniedPrivilegedRemovePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, managerDeniedPrivileged, proto.ErrForbidden, "delegated manager removing privileged member", "board moderator role required to manage delegated board members")

	removePayload := marshalCoreTestJSON(t, "marshal board member remove payload", proto.SetBoardMemberPayload{
		Board:  "general",
		User:   bob.ID,
		Member: false,
	})
	removeRecord, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board member remove", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-member-remove",
		Command:    proto.CmdSetBoardMember,
		Payload:    removePayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtBoardMemberSet {
		t.Fatalf("board member events after remove = %+v, want add then remove", events)
	}
	removeEvent := requireNativeDecisionPayload[proto.BoardMemberSetPayload](t, events[1], "board member remove payload")
	if removeEvent.Board != "general" || removeEvent.User != bob.ID || removeEvent.Member ||
		removeEvent.Title != "" || removeEvent.Position != 0 ||
		removeEvent.CanCurate || removeEvent.CanSetBoardSettings ||
		removeEvent.By != admin.ID || removeEvent.TS != 3234 {
		t.Fatalf("board member remove event = %+v, want deterministic final removal", removeEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-member-test", partition, 10, "materialize board member remove event")
	info, err = c.GetBoardInfo("general")
	if err != nil {
		t.Fatalf("get board info after member remove: %v", err)
	}
	for _, member := range info.Members {
		if member.UserID == bob.ID {
			t.Fatalf("bob still present after removal: %+v", info.Members)
		}
	}
	removeRetryReply := executor.ExecuteCommandLogRecord(ctx, removeRecord)
	if removeRetryReply.Err != nil || removeRetryReply.Result == nil || removeRetryReply.Result.ID != "general" {
		t.Fatalf("board member remove retry reply = %+v, want idempotent success after projection", removeRetryReply)
	}
	removeRetryEvents, err := executor.DecideCommandLogEvents(ctx, removeRecord, removeRetryReply)
	if err != nil {
		t.Fatalf("decide board member remove retry events: %v", err)
	}
	requireNativeDecisionEventKinds(t, removeRetryEvents, "board member remove retry events", proto.EvtBoardMemberSet)
	removeRetry := requireNativeDecisionPayload[proto.BoardMemberSetPayload](t, removeRetryEvents[0], "board member remove retry payload")
	if removeRetry.User != removeEvent.User || removeRetry.Member != removeEvent.Member ||
		removeRetry.Title != removeEvent.Title || removeRetry.Position != removeEvent.Position ||
		removeRetry.TS != removeEvent.TS {
		t.Fatalf("board member remove retry payload = %+v, want stable removal %+v", removeRetry, removeEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardMembershipLeave(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	registerNativeDecisionTestUser(t, c, "admin")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	if err := projections.SetBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{Title: "Resident"}); err != nil {
		t.Fatalf("seed bob board membership: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	emptyPayload := marshalCoreTestJSON(t, "marshal empty leave membership payload", proto.LeaveBoardMembershipPayload{Board: " "})
	emptyReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: partitionGlobal},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-leave-membership-empty",
		Command:    proto.CmdLeaveBoardMembership,
		Payload:    emptyPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, emptyReply, proto.ErrValidationFailed, "leave membership empty board", "board is required")

	missingPayload := marshalCoreTestJSON(t, "marshal missing board leave membership payload", proto.LeaveBoardMembershipPayload{Board: "missing"})
	missingReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-leave-membership-missing-board",
		Command:    proto.CmdLeaveBoardMembership,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missingReply, proto.ErrNotFound, "leave membership missing board", "board not found")

	leavePayload := marshalCoreTestJSON(t, "marshal leave membership payload", proto.LeaveBoardMembershipPayload{Board: " general "})
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	record, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "leave membership", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    bob.ID,
		CID:        "cid-native-leave-membership",
		Command:    proto.CmdLeaveBoardMembership,
		Payload:    leavePayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "leave membership events", proto.EvtBoardMemberSet)
	leaveEvent := requireNativeDecisionPayload[proto.BoardMemberSetPayload](t, events[0], "leave membership payload")
	if leaveEvent.Board != "general" || leaveEvent.User != bob.ID || leaveEvent.Member ||
		leaveEvent.Title != "" || leaveEvent.Position != 0 ||
		leaveEvent.CanManageMembers || leaveEvent.CanCurate || leaveEvent.CanModeratePosts ||
		leaveEvent.CanModerateThreads || leaveEvent.CanAnnounce || leaveEvent.CanManagePolls ||
		leaveEvent.CanSetBoardSettings || leaveEvent.By != bob.ID || leaveEvent.TS != 2234 {
		t.Fatalf("leave membership event = %+v, want deterministic final removal", leaveEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-membership-leave-test", partition, 10, "materialize leave membership event")
	isMember, err := c.UserIsBoardMember("general", bob.ID)
	if err != nil {
		t.Fatalf("check projected membership after leave: %v", err)
	}
	if isMember {
		t.Fatal("bob is still a projected board member after leave")
	}
	members, err := c.ListBoardMembers("general")
	if err != nil {
		t.Fatalf("list board members after leave: %v", err)
	}
	for _, member := range members {
		if member.UserID == bob.ID {
			t.Fatalf("bob still present after leave: %+v", members)
		}
	}

	retryReply := executor.ExecuteCommandLogRecord(ctx, record)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "general" {
		t.Fatalf("leave membership retry reply = %+v, want idempotent success after projection", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, record, retryReply)
	if err != nil {
		t.Fatalf("decide leave membership retry events: %v", err)
	}
	if len(retryEvents) != 1 || retryEvents[0].ID != stableCommandLogDecisionID("evt_", record, 0) ||
		retryEvents[0].Kind != proto.EvtBoardMemberSet {
		t.Fatalf("leave membership retry events = %+v, want stable board.member_set", retryEvents)
	}
	retryLeave := requireNativeDecisionPayload[proto.BoardMemberSetPayload](t, retryEvents[0], "leave membership retry payload")
	if retryLeave.Board != leaveEvent.Board || retryLeave.User != leaveEvent.User ||
		retryLeave.Member != leaveEvent.Member || retryLeave.By != leaveEvent.By ||
		retryLeave.TS != leaveEvent.TS {
		t.Fatalf("leave membership retry payload = %+v, want stable removal %+v", retryLeave, leaveEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardMembershipApplication(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	registerNativeDecisionTestUser(t, c, "admin")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	manualPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	longApplicationNote := strings.Repeat("x", proto.MaxBoardNoteLength+1)
	longNotePayload := marshalCoreTestJSON(t, "marshal long membership note payload", proto.ApplyBoardMembershipPayload{
		Board: "general",
		Note:  longApplicationNote,
	})
	longNote := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  manualPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-apply-membership-long-note",
		Command:    proto.CmdApplyBoardMembership,
		Payload:    longNotePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, longNote, proto.ErrValidationFailed, "apply membership long note", validationMessage(longApplicationNote, proto.NormalizeBoardMembershipApplicationNote))

	manualPayload := marshalCoreTestJSON(t, "marshal manual membership application payload", proto.ApplyBoardMembershipPayload{
		Board: " general ",
		Note:  " I read this board daily. ",
	})
	manualRecord, manualEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "manual membership application", 0, 10, CommandLogRecord{
		Partition:  manualPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-apply-membership-manual",
		Command:    proto.CmdApplyBoardMembership,
		Payload:    manualPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, manualEvents, "manual membership events", proto.EvtBoardMemberApplicationSubmitted)
	manualEvent := requireNativeDecisionPayload[proto.BoardMemberApplicationSubmittedPayload](t, manualEvents[0], "manual membership payload")
	manualAppID := stableCommandLogDecisionID("bmap_", manualRecord, 0)
	if manualEvent.ID != manualAppID || manualEvent.Board != "general" ||
		manualEvent.User != bob.ID || manualEvent.Note != "I read this board daily." ||
		manualEvent.TS != 2234 {
		t.Fatalf("manual membership event = %+v, want deterministic submitted application", manualEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-membership-application-test", manualPartition, 10, "materialize manual membership application event")
	manualApp, err := c.GetBoardMemberApplication(manualAppID)
	if err != nil {
		t.Fatalf("get manual membership application: %v", err)
	}
	if manualApp == nil || manualApp.Status != "pending" || manualApp.BoardID != "general" ||
		manualApp.UserID != bob.ID || manualApp.Note != "I read this board daily." ||
		manualApp.CreatedAt != 2234 || manualApp.UpdatedAt != 2234 {
		t.Fatalf("manual membership application = %+v, want pending projected row", manualApp)
	}
	manualRetryReply := executor.ExecuteCommandLogRecord(ctx, manualRecord)
	if manualRetryReply.Err != nil || manualRetryReply.Result == nil || manualRetryReply.Result.ID != manualAppID {
		t.Fatalf("manual membership retry reply = %+v, want idempotent success after projection", manualRetryReply)
	}
	manualRetryEvents, err := executor.DecideCommandLogEvents(ctx, manualRecord, manualRetryReply)
	if err != nil {
		t.Fatalf("decide manual membership retry events: %v", err)
	}
	if len(manualRetryEvents) != 1 || manualRetryEvents[0].ID != stableCommandLogDecisionID("evt_", manualRecord, 0) ||
		manualRetryEvents[0].Kind != proto.EvtBoardMemberApplicationSubmitted {
		t.Fatalf("manual membership retry events = %+v, want stable submitted event", manualRetryEvents)
	}
	duplicateReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  manualPartition,
		Offset:     99,
		ActorID:    bob.ID,
		CID:        "cid-native-apply-membership-duplicate",
		Command:    proto.CmdApplyBoardMembership,
		Payload:    manualPayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, duplicateReply, proto.ErrConflict, "duplicate manual membership application", "membership application already pending")

	if _, err := qExec(c.DB,
		`INSERT INTO boards (id, name, description) VALUES (?, ?, ?)`,
		"autoclub", "Auto Club", "Auto-approved residents",
	); err != nil {
		t.Fatalf("seed auto board: %v", err)
	}
	approvalMode := "auto"
	if err := projections.SetBoardMemberRequirements(c.DB, "autoclub", BoardMemberRequirementsPatch{
		ApprovalMode: &approvalMode,
	}); err != nil {
		t.Fatalf("seed auto approval requirements: %v", err)
	}
	autoPartition := LogPartition{Kind: partitionBoard, Key: "autoclub"}
	autoPayload := marshalCoreTestJSON(t, "marshal auto membership application payload", proto.ApplyBoardMembershipPayload{
		Board: "autoclub",
		Note:  "private note",
	})
	autoRecord, autoEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "auto membership application", 0, 10, CommandLogRecord{
		Partition:  autoPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-apply-membership-auto",
		Command:    proto.CmdApplyBoardMembership,
		Payload:    autoPayload,
		EnqueuedAt: 4234,
	})
	if len(autoEvents) != 2 ||
		autoEvents[0].Kind != proto.EvtBoardMemberApplicationSubmitted ||
		autoEvents[1].Kind != proto.EvtBoardMemberApplicationReviewed {
		t.Fatalf("auto membership events = %+v, want submitted plus reviewed", autoEvents)
	}
	autoSubmit := requireNativeDecisionPayload[proto.BoardMemberApplicationSubmittedPayload](t, autoEvents[0], "auto membership submitted payload")
	autoReview := requireNativeDecisionPayload[proto.BoardMemberApplicationReviewedPayload](t, autoEvents[1], "auto membership reviewed payload")
	autoAppID := stableCommandLogDecisionID("bmap_", autoRecord, 0)
	if autoSubmit.ID != autoAppID || autoSubmit.Board != "autoclub" ||
		autoSubmit.User != bob.ID || autoSubmit.Note != "private note" || autoSubmit.TS != 4234 {
		t.Fatalf("auto submitted event = %+v, want deterministic application", autoSubmit)
	}
	if autoReview.Application != autoAppID || autoReview.Board != "autoclub" ||
		autoReview.User != bob.ID || autoReview.Status != "approved" ||
		autoReview.Reviewer != bob.ID || autoReview.ReviewNote != "auto-approved by board membership rules" ||
		autoReview.TS != 4234 {
		t.Fatalf("auto reviewed event = %+v, want deterministic auto approval", autoReview)
	}
	registryPartition := LogPartition{Kind: partitionBoard, Key: proto.RegistrySystemBoardID}
	registryEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, registryPartition, 0, 10, "replay registry membership events")
	if len(registryEvents) != 3 ||
		registryEvents[0].Kind != proto.EvtBoardCreated ||
		registryEvents[1].Kind != proto.EvtThreadNew ||
		registryEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("registry membership events = %+v, want board/thread/post", registryEvents)
	}
	registryPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, registryEvents[2], "registry post payload")
	if !strings.Contains(registryPost.Body, "Status: approved") ||
		!strings.Contains(registryPost.Body, "Board: Auto Club (autoclub)") ||
		!strings.Contains(registryPost.Body, "Applicant: bob") ||
		!strings.Contains(registryPost.Body, "Reviewer: bob") ||
		strings.Contains(registryPost.Body, "private note") {
		t.Fatalf("registry post body = %q, want sanitized auto approval log", registryPost.Body)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-membership-application-test", autoPartition, 10, "materialize auto membership events")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-membership-application-test", registryPartition, 10, "materialize registry membership events")
	autoApp, err := c.GetBoardMemberApplication(autoAppID)
	if err != nil {
		t.Fatalf("get auto membership application: %v", err)
	}
	if autoApp == nil || autoApp.Status != "approved" || autoApp.ReviewerID != bob.ID ||
		autoApp.ReviewNote != "auto-approved by board membership rules" ||
		autoApp.ReviewedAt != 4234 {
		t.Fatalf("auto membership application = %+v, want approved projected row", autoApp)
	}
	isMember, err := c.UserIsBoardMember("autoclub", bob.ID)
	if err != nil {
		t.Fatalf("check auto board membership: %v", err)
	}
	if !isMember {
		t.Fatal("expected auto-approved applicant to become a board member")
	}
	registryThread, err := c.GetThread("registry_approved_thr_" + autoAppID)
	if err != nil {
		t.Fatalf("get registry thread: %v", err)
	}
	if registryThread == nil || registryThread.Board != proto.RegistrySystemBoardID {
		t.Fatalf("registry thread = %+v, want projected registry approval thread", registryThread)
	}
	autoRetryReply := executor.ExecuteCommandLogRecord(ctx, autoRecord)
	if autoRetryReply.Err != nil || autoRetryReply.Result == nil || autoRetryReply.Result.ID != autoAppID {
		t.Fatalf("auto membership retry reply = %+v, want idempotent success after projection", autoRetryReply)
	}
	autoRetryEvents, err := executor.DecideCommandLogEvents(ctx, autoRecord, autoRetryReply)
	if err != nil {
		t.Fatalf("decide auto membership retry events: %v", err)
	}
	if len(autoRetryEvents) != 2 ||
		autoRetryEvents[0].Kind != proto.EvtBoardMemberApplicationSubmitted ||
		autoRetryEvents[1].Kind != proto.EvtBoardMemberApplicationReviewed {
		t.Fatalf("auto membership retry events = %+v, want stable application/review events after registry projection", autoRetryEvents)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardMembershipReview(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	manager := true
	if err := projections.SetBoardMember(c.DB, "general", alice.ID, true, BoardMemberPatch{CanManageMembers: &manager}); err != nil {
		t.Fatalf("seed delegated board member manager: %v", err)
	}
	if err := projections.InsertBoardMemberApplication(c.DB, "app_review_bob", "general", bob.ID, "private application note"); err != nil {
		t.Fatalf("insert bob membership application: %v", err)
	}
	if err := projections.InsertBoardMemberApplication(c.DB, "app_review_alice", "general", alice.ID, "self application note"); err != nil {
		t.Fatalf("insert alice membership application: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	reviewPartition := LogPartition{Kind: partitionReview, Key: "app_review_bob"}
	approvePayload := marshalCoreTestJSON(t, "marshal review approval payload", proto.ReviewBoardMembershipPayload{
		Application: " app_review_bob ",
		Status:      "approve",
		Title:       " resident ",
		Note:        " welcome aboard ",
	})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-review-membership-forbidden",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    approvePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "review membership without manager permission", "board member manager permission required")
	selfPayload := marshalCoreTestJSON(t, "marshal self review payload", proto.ReviewBoardMembershipPayload{
		Application: "app_review_alice",
		Status:      "approved",
	})
	selfReview := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionReview, Key: "app_review_alice"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-review-membership-self",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    selfPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, selfReview, proto.ErrForbidden, "delegated manager self review", "board moderator role required to review your own application")
	blacklistPayload := marshalCoreTestJSON(t, "marshal blacklist review payload", proto.ReviewBoardMembershipPayload{
		Application: "app_review_bob",
		Status:      "blacklisted",
	})
	blacklistDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-review-membership-blacklist-denied",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    blacklistPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, blacklistDenied, proto.ErrForbidden, "delegated manager blacklist review", "board moderator role required to blacklist membership applications")
	longTitlePayload := marshalCoreTestJSON(t, "marshal long title review payload", proto.ReviewBoardMembershipPayload{
		Application: "app_review_bob",
		Status:      "approved",
		Title:       strings.Repeat("x", 81),
	})
	longTitle := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-review-membership-long-title",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    longTitlePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, longTitle, proto.ErrValidationFailed, "long review title", validationMessage(strings.Repeat("x", proto.MaxBoardMemberTitleLength+1), proto.NormalizeBoardMemberTitle))
	longReviewNote := strings.Repeat("x", proto.MaxBoardNoteLength+1)
	longNotePayload := marshalCoreTestJSON(t, "marshal long note review payload", proto.ReviewBoardMembershipPayload{
		Application: "app_review_bob",
		Status:      "approved",
		Note:        longReviewNote,
	})
	longNote := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-review-membership-long-note",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    longNotePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, longNote, proto.ErrValidationFailed, "long review note", validationMessage(longReviewNote, proto.NormalizeBoardMembershipReviewNote))
	badStatusPayload := marshalCoreTestJSON(t, "marshal bad status review payload", proto.ReviewBoardMembershipPayload{
		Application: "app_review_bob",
		Status:      "maybe",
	})
	badStatus := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-review-membership-bad-status",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    badStatusPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, badStatus, proto.ErrValidationFailed, "bad review status", validationMessage("maybe", proto.NormalizeBoardMemberApplicationStatus))

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	record, boardEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "review membership", boardPartition, 0, 10, CommandLogRecord{
		Partition:  reviewPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-review-membership-approve",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    approvePayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, boardEvents, "review board events", proto.EvtBoardMemberApplicationReviewed)
	reviewEvent := requireNativeDecisionPayload[proto.BoardMemberApplicationReviewedPayload](t, boardEvents[0], "review event payload")
	if reviewEvent.Application != "app_review_bob" || reviewEvent.Board != "general" ||
		reviewEvent.User != bob.ID || reviewEvent.Status != "approved" ||
		reviewEvent.Title != "resident" || reviewEvent.Reviewer != alice.ID ||
		reviewEvent.ReviewNote != "welcome aboard" || reviewEvent.TS != 2234 {
		t.Fatalf("review event = %+v, want deterministic approval", reviewEvent)
	}
	registryPartition := LogPartition{Kind: partitionBoard, Key: proto.RegistrySystemBoardID}
	registryEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, registryPartition, 0, 10, "replay review registry events")
	if len(registryEvents) != 3 ||
		registryEvents[0].Kind != proto.EvtBoardCreated ||
		registryEvents[1].Kind != proto.EvtThreadNew ||
		registryEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("review registry events = %+v, want board/thread/post", registryEvents)
	}
	registryPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, registryEvents[2], "review registry post payload")
	if !strings.Contains(registryPost.Body, "Status: approved") ||
		!strings.Contains(registryPost.Body, "Board: General (general)") ||
		!strings.Contains(registryPost.Body, "Applicant: bob") ||
		!strings.Contains(registryPost.Body, "Reviewer: alice") ||
		strings.Contains(registryPost.Body, "private application note") ||
		strings.Contains(registryPost.Body, "welcome aboard") ||
		strings.Contains(registryPost.Body, "resident") {
		t.Fatalf("review registry post body = %q, want sanitized approval log", registryPost.Body)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-membership-review-test", boardPartition, 10, "materialize review board event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-membership-review-test", registryPartition, 10, "materialize review registry events")
	app, err := c.GetBoardMemberApplication("app_review_bob")
	if err != nil {
		t.Fatalf("get reviewed membership application: %v", err)
	}
	if app == nil || app.Status != "approved" || app.Title != "resident" ||
		app.ReviewerID != alice.ID || app.ReviewNote != "welcome aboard" ||
		app.ReviewedAt != 2234 {
		t.Fatalf("reviewed membership application = %+v, want approved row", app)
	}
	members, err := c.ListBoardMembers("general")
	if err != nil {
		t.Fatalf("list board members after review: %v", err)
	}
	var bobMember *projections.BoardMember
	for i := range members {
		if members[i].UserID == bob.ID {
			bobMember = &members[i]
			break
		}
	}
	if bobMember == nil || bobMember.Title != "resident" {
		t.Fatalf("bob member after review = %+v in %+v, want resident member", bobMember, members)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, record)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "app_review_bob" {
		t.Fatalf("review membership retry reply = %+v, want idempotent success after projection", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, record, retryReply)
	if err != nil {
		t.Fatalf("decide review membership retry events: %v", err)
	}
	if len(retryEvents) != 1 ||
		retryEvents[0].ID != stableCommandLogDecisionID("evt_", record, 0) ||
		retryEvents[0].Kind != proto.EvtBoardMemberApplicationReviewed {
		t.Fatalf("review membership retry events = %+v, want stable reviewed event", retryEvents)
	}
	retryReview := requireNativeDecisionPayload[proto.BoardMemberApplicationReviewedPayload](t, retryEvents[0], "review retry payload")
	if retryReview.Application != reviewEvent.Application ||
		retryReview.Status != reviewEvent.Status ||
		retryReview.Title != reviewEvent.Title ||
		retryReview.Reviewer != reviewEvent.Reviewer ||
		retryReview.ReviewNote != reviewEvent.ReviewNote ||
		retryReview.TS != reviewEvent.TS {
		t.Fatalf("review retry payload = %+v, want stable payload %+v", retryReview, reviewEvent)
	}
	duplicate := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     99,
		ActorID:    alice.ID,
		CID:        "cid-native-review-membership-duplicate",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    approvePayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, duplicate, proto.ErrConflict, "duplicate review membership command", "membership application is already reviewed")
}

func TestNativeCommandLogDecisionExecutorDrainsBasicCreateThreadThroughBrokerEvents(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	harness := newBrokerCommandEventTestHarness()
	commandLog := harness.commandLog
	eventStore := harness.eventStore
	transactionStore := harness.transactionStore
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	payload := marshalCoreTestJSON(t, "marshal payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native hello",
		Body:  "first post from broker decision",
	})
	record := produceNativeDecisionCommand(t, ctx, commandLog, "reply", CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	executor := NewCommandLogNativeDecisionExecutor(c)
	worker := NewCommandLogWorker(CommandLogWorkerConfig{
		Log:       commandLog,
		BatchSize: 10,
		Executor:  executor,
		Finalizer: CommandEventTransactionFinalizer{
			Transactions: transactionStore,
			Events:       executor,
		},
	})

	results := drainCommandLogWorkerOnce(t, ctx, worker, "drain once")
	if len(results) != 1 || results[0].Processed != 1 || results[0].Applied != 1 || results[0].LastOffset != record.Offset {
		t.Fatalf("results = %+v, want one native decision committed", results)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, partition, record.Offset, "command")
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 0, 10, "replay broker events")
	requireNativeDecisionEventKinds(t, events, "events", proto.EvtThreadNew, proto.EvtPostAppended)
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	postPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "post event payload")
	if threadPayload.ID == "" || postPayload.Thread != threadPayload.ID || threadPayload.Title != "Broker-native hello" {
		t.Fatalf("thread/post payloads = %+v / %+v, want linked broker-native thread", threadPayload, postPayload)
	}
	if events[0].PartitionOffset != 1 || events[1].PartitionOffset != 2 {
		t.Fatalf("event offsets = %d,%d; want 1,2", events[0].PartitionOffset, events[1].PartitionOffset)
	}
	materialized, err := projections.GetThread(c.DB, threadPayload.ID)
	if err != nil {
		t.Fatalf("get materialized thread: %v", err)
	}
	if materialized != nil {
		t.Fatalf("materialized thread = %+v, want broker decision to leave SQL projections untouched", materialized)
	}

	materializedResult := materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-create-thread-test", partition, 10, "materialize broker event partition")
	if materializedResult.StartedOffset != 0 || materializedResult.LastOffset != 2 || materializedResult.Applied != 2 {
		t.Fatalf("materialization result = %+v, want offsets 0->2 with two applied events", materializedResult)
	}
	materialized, err = projections.GetThread(c.DB, threadPayload.ID)
	if err != nil {
		t.Fatalf("get materialized thread after event-store projection: %v", err)
	}
	if materialized == nil || materialized.Title != "Broker-native hello" || materialized.PostCount != 1 || materialized.LastSeq != 2 {
		t.Fatalf("materialized thread = %+v, want broker-projected thread with one post", materialized)
	}
	post, err := projections.GetPost(c.DB, postPayload.ID)
	if err != nil {
		t.Fatalf("get materialized post: %v", err)
	}
	if post == nil || post.Thread != threadPayload.ID || post.Body != "first post from broker decision" || post.AuthorID != alice.ID {
		t.Fatalf("materialized post = %+v, want broker-projected first post", post)
	}
	secondResult := materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-create-thread-test", partition, 10, "second materialize broker event partition")
	if secondResult.StartedOffset != 2 || secondResult.LastOffset != 2 || secondResult.Applied != 0 {
		t.Fatalf("second materialization result = %+v, want checkpointed no-op", secondResult)
	}

	replyPayload := marshalCoreTestJSON(t, "marshal reply payload", proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "broker-native reply",
	})
	replyRecord := produceNativeDecisionCommand(t, ctx, commandLog, "reply", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post",
		Command:    proto.CmdAppendPost,
		Payload:    replyPayload,
		EnqueuedAt: 2234,
	})
	replyResults := drainCommandLogWorkerOnce(t, ctx, worker, "drain reply once")
	replyProcessed, replyApplied := totalCommandLogWorkerResults(replyResults)
	if replyProcessed != 1 || replyApplied != 1 {
		t.Fatalf("reply results = %+v, want one native appendPost decision committed", replyResults)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, LogPartition{Kind: partitionThread, Key: threadPayload.ID}, replyRecord.Offset, "reply")
	replyEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 2, 10, "replay broker reply event")
	if len(replyEvents) != 1 || replyEvents[0].Kind != proto.EvtPostAppended || replyEvents[0].PartitionOffset != 3 {
		t.Fatalf("reply events = %+v, want one post.appended at board offset 3", replyEvents)
	}
	replyPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, replyEvents[0], "reply payload")
	replyMaterialized := materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-create-thread-test", partition, 10, "materialize broker reply event partition")
	if replyMaterialized.StartedOffset != 2 || replyMaterialized.LastOffset != 3 || replyMaterialized.Applied != 1 {
		t.Fatalf("reply materialization result = %+v, want offsets 2->3 with one applied event", replyMaterialized)
	}
	replyPost, err := projections.GetPost(c.DB, replyPostPayload.ID)
	if err != nil {
		t.Fatalf("get materialized reply: %v", err)
	}
	if replyPost == nil || replyPost.Thread != threadPayload.ID || replyPost.Body != "broker-native reply" || replyPost.AuthorID != alice.ID {
		t.Fatalf("materialized reply = %+v, want broker-projected reply", replyPost)
	}
	materialized, err = projections.GetThread(c.DB, threadPayload.ID)
	if err != nil {
		t.Fatalf("get materialized thread after reply: %v", err)
	}
	if materialized == nil || materialized.PostCount != 2 || materialized.LastSeq != 3 {
		t.Fatalf("materialized thread after reply = %+v, want two posts at last seq 3", materialized)
	}

	directedPayload := marshalCoreTestJSON(t, "marshal directed reply payload", proto.AppendPostPayload{
		Thread:  threadPayload.ID,
		ReplyTo: postPayload.ID,
		Body:    "broker-native directed reply",
	})
	directedRecord := produceNativeDecisionCommand(t, ctx, commandLog, "directed reply", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    bob.ID,
		CID:        "cid-native-directed-reply",
		Command:    proto.CmdAppendPost,
		Payload:    directedPayload,
		EnqueuedAt: 3234,
	})
	directedResults := drainCommandLogWorkerOnce(t, ctx, worker, "drain directed reply once")
	directedProcessed, directedApplied := totalCommandLogWorkerResults(directedResults)
	if directedProcessed != 1 || directedApplied != 1 {
		t.Fatalf("directed reply results = %+v, want one native directed appendPost decision committed", directedResults)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, LogPartition{Kind: partitionThread, Key: threadPayload.ID}, directedRecord.Offset, "directed reply")
	directedEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 3, 10, "replay broker directed reply event")
	if len(directedEvents) != 1 || directedEvents[0].Kind != proto.EvtPostAppended || directedEvents[0].PartitionOffset != 4 {
		t.Fatalf("directed reply events = %+v, want one post.appended at board offset 4", directedEvents)
	}
	directedPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, directedEvents[0], "directed reply payload")
	if directedPostPayload.ReplyTo != postPayload.ID {
		t.Fatalf("directed reply event ReplyTo = %q, want root post %q", directedPostPayload.ReplyTo, postPayload.ID)
	}
	directedMaterialized := materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-create-thread-test", partition, 10, "materialize broker directed reply event partition")
	if directedMaterialized.StartedOffset != 3 || directedMaterialized.LastOffset != 4 || directedMaterialized.Applied != 1 {
		t.Fatalf("directed materialization result = %+v, want offsets 3->4 with one applied event", directedMaterialized)
	}
	directedPost, err := projections.GetPost(c.DB, directedPostPayload.ID)
	if err != nil {
		t.Fatalf("get materialized directed reply: %v", err)
	}
	if directedPost == nil || directedPost.Thread != threadPayload.ID || directedPost.Body != "broker-native directed reply" || directedPost.AuthorID != bob.ID || directedPost.ReplyTo != postPayload.ID {
		t.Fatalf("materialized directed reply = %+v, want broker-projected direct reply to root", directedPost)
	}
	if processed := drainOutboxJobsForTest(t, c); processed != 3 {
		t.Fatalf("processed outbox jobs = %d, want one job per projected post", processed)
	}
	notifications, err := c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("ListNotifications(alice): %v", err)
	}
	if len(notifications) != 1 || notifications[0].Kind != "reply" || notifications[0].PostID != directedPostPayload.ID || notifications[0].Actor != bob.Name {
		t.Fatalf("notifications = %+v, want native directed reply notification for alice", notifications)
	}

	nestedPayload := marshalCoreTestJSON(t, "marshal nested directed reply payload", proto.AppendPostPayload{
		Thread:  threadPayload.ID,
		ReplyTo: directedPostPayload.ID,
		Body:    "broker-native nested reply",
	})
	nestedRecord := produceNativeDecisionCommand(t, ctx, commandLog, "nested directed reply", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-nested-directed-reply",
		Command:    proto.CmdAppendPost,
		Payload:    nestedPayload,
		EnqueuedAt: 4234,
	})
	nestedResults := drainCommandLogWorkerOnce(t, ctx, worker, "drain nested directed reply once")
	_, nestedApplied := totalCommandLogWorkerResults(nestedResults)
	if nestedApplied != 1 {
		t.Fatalf("nested directed reply results = %+v, want one native appendPost decision committed", nestedResults)
	}
	requireCommandLogWorkerCommittedOffset(t, ctx, commandLog, LogPartition{Kind: partitionThread, Key: threadPayload.ID}, nestedRecord.Offset, "nested directed reply")
	nestedEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 4, 10, "replay broker nested directed reply event")
	if len(nestedEvents) != 1 || nestedEvents[0].Kind != proto.EvtPostAppended || nestedEvents[0].PartitionOffset != 5 {
		t.Fatalf("nested directed reply events = %+v, want one post.appended at board offset 5", nestedEvents)
	}
	nestedPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, nestedEvents[0], "nested directed reply payload")
	if nestedPostPayload.ReplyTo != postPayload.ID {
		t.Fatalf("nested directed reply event ReplyTo = %q, want flattened root post %q", nestedPostPayload.ReplyTo, postPayload.ID)
	}
}

func TestNativeCommandLogDecisionExecutorReusesExecutedDecisionForEvents(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")

	executor := NewCommandLogNativeDecisionExecutor(c)
	payload := marshalCoreTestJSON(t, "marshal create thread payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Cached native decision",
		Body:  "finalization should not re-read mutable board policy",
	})
	record := CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:     77,
		ActorID:    alice.ID,
		CID:        "cid-native-cached-create-thread",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: 1234,
	}
	reply := executor.ExecuteCommandLogRecord(ctx, record)
	if reply.Err != nil || reply.Result == nil {
		t.Fatalf("execute create thread reply = %+v, want successful deterministic ack", reply)
	}

	readOnly := true
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{ReadOnly: &readOnly}); err != nil {
		t.Fatalf("set board read-only: %v", err)
	}

	events, err := executor.DecideCommandLogEvents(ctx, record, reply)
	if err != nil {
		t.Fatalf("decide cached create thread events: %v", err)
	}
	requireNativeDecisionEventKinds(t, events, "cached events", proto.EvtThreadNew, proto.EvtPostAppended)
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "cached thread payload")
	if reply.Result.ID != threadPayload.ID {
		t.Fatalf("reply result id = %q, cached thread id = %q; want match", reply.Result.ID, threadPayload.ID)
	}

	_, err = executor.DecideCommandLogEvents(ctx, record, reply)
	requireErrorContains(t, err, "board is read-only")
}

func TestNativeCommandLogDecisionRejectsOverlongPostBodies(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	executor := NewCommandLogNativeDecisionExecutor(c)
	longBody := strings.Repeat("x", proto.MaxPostBodyLength+1)
	wantMessage := proto.ValidatePostBodyLength(longBody)
	tests := []struct {
		name      string
		command   proto.CommandName
		payload   any
		partition LogPartition
	}{
		{
			name:    "create thread",
			command: proto.CmdCreateThread,
			payload: proto.CreateThreadPayload{
				Board: "general",
				Title: "Long body",
				Body:  longBody,
			},
			partition: LogPartition{Kind: partitionBoard, Key: "general"},
		},
		{
			name:    "append post",
			command: proto.CmdAppendPost,
			payload: proto.AppendPostPayload{
				Thread: "thr_long_body",
				Body:   longBody,
			},
			partition: LogPartition{Kind: partitionThread, Key: "thr_long_body"},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := marshalCoreTestJSON(t, "marshal payload", tt.payload)
			reply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
				Partition:  tt.partition,
				Offset:     int64(i + 1),
				ActorID:    "alice",
				CID:        "cid-native-long-body-" + strings.ReplaceAll(tt.name, " ", "-"),
				Command:    tt.command,
				Payload:    raw,
				EnqueuedAt: 1234,
			})
			if reply.Err == nil || reply.Err.Code != proto.ErrValidationFailed || reply.Err.Message != wantMessage || reply.Err.Retryable {
				t.Fatalf("err = %+v, want terminal validation %q", reply.Err, wantMessage)
			}
		})
	}
}

func TestNativeCommandLogDecisionRejectsInvalidThreadTitles(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	executor := NewCommandLogNativeDecisionExecutor(c)
	longTitle := strings.Repeat("x", proto.MaxThreadTitleLength+1)
	tests := []struct {
		name      string
		command   proto.CommandName
		payload   any
		partition LogPartition
		want      string
	}{
		{
			name:    "set thread title blank",
			command: proto.CmdSetThreadTitle,
			payload: proto.SetThreadTitlePayload{
				Thread: "thr_invalid_title",
				Title:  " \t ",
			},
			partition: LogPartition{Kind: partitionThread, Key: "thr_invalid_title"},
			want:      proto.ValidateThreadTitle(" \t "),
		},
		{
			name:    "set thread title long",
			command: proto.CmdSetThreadTitle,
			payload: proto.SetThreadTitlePayload{
				Thread: "thr_invalid_title",
				Title:  longTitle,
			},
			partition: LogPartition{Kind: partitionThread, Key: "thr_invalid_title"},
			want:      proto.ValidateThreadTitle(longTitle),
		},
		{
			name:    "system notice long",
			command: proto.CmdPublishSystemNotice,
			payload: proto.PublishSystemNoticePayload{
				Title: longTitle,
				Body:  "body",
			},
			partition: LogPartition{Kind: partitionBoard, Key: partitionGlobal},
			want:      proto.ValidateThreadTitle(longTitle),
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := marshalCoreTestJSON(t, "marshal payload", tt.payload)
			reply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
				Partition:  tt.partition,
				Offset:     int64(i + 1),
				ActorID:    admin.ID,
				CID:        "cid-native-invalid-title-" + strings.ReplaceAll(tt.name, " ", "-"),
				Command:    tt.command,
				Payload:    raw,
				EnqueuedAt: 1234,
			})
			if reply.Err == nil || reply.Err.Code != proto.ErrValidationFailed || reply.Err.Message != tt.want || reply.Err.Retryable {
				t.Fatalf("err = %+v, want terminal validation %q", reply.Err, tt.want)
			}
		})
	}
}

func TestNativeCommandLogDecisionRejectsInvalidSystemNoticeSource(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	longSource := strings.Repeat("x", proto.MaxSystemNoticeSourceLength+1)
	raw := marshalCoreTestJSON(t, "marshal payload", proto.PublishSystemNoticePayload{
		Title:  "Notice",
		Body:   "body",
		Source: longSource,
	})
	reply := NewCommandLogNativeDecisionExecutor(c).ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: partitionGlobal},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-invalid-system-notice-source",
		Command:    proto.CmdPublishSystemNotice,
		Payload:    raw,
		EnqueuedAt: 1234,
	})
	want := proto.ValidateSystemNoticeSource(longSource)
	if reply.Err == nil || reply.Err.Code != proto.ErrValidationFailed || reply.Err.Message != want || reply.Err.Retryable {
		t.Fatalf("err = %+v, want terminal validation %q", reply.Err, want)
	}
}

func TestNativeCommandLogDecisionRejectsInvalidAttachmentMetadata(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	executor := NewCommandLogNativeDecisionExecutor(c)
	longFilename := strings.Repeat("x", proto.MaxAttachmentFilenameLength+1)
	longURL := strings.Repeat("x", proto.MaxAttachmentURLLength+1)
	tests := []struct {
		name      string
		command   proto.CommandName
		payload   any
		partition LogPartition
		want      string
	}{
		{
			name:    "create thread attachment url",
			command: proto.CmdCreateThread,
			payload: proto.CreateThreadPayload{
				Board: "general",
				Title: "Bad attachment",
				Body:  "body",
				Attachments: []proto.AttachmentPayload{{
					Filename: "proof.txt",
					URL:      longURL,
				}},
			},
			partition: LogPartition{Kind: partitionBoard, Key: "general"},
			want:      validationMessage(proto.AttachmentPayload{Filename: "proof.txt", URL: longURL}, proto.NormalizeAttachmentPayload),
		},
		{
			name:    "attach post filename",
			command: proto.CmdAttachPost,
			payload: proto.AttachPostPayload{
				Post:     "pst_bad_attachment",
				Filename: longFilename,
			},
			partition: LogPartition{Kind: partitionPost, Key: "pst_bad_attachment"},
			want:      validationMessage(proto.AttachmentPayload{Filename: longFilename}, proto.NormalizeAttachmentPayload),
		},
		{
			name:    "attach mail size",
			command: proto.CmdAttachMail,
			payload: proto.AttachMailPayload{
				Mail:      "mail_bad_attachment",
				Filename:  "proof.txt",
				SizeBytes: -1,
			},
			partition: LogPartition{Kind: partitionMail, Key: "mail_bad_attachment"},
			want:      validationMessage(proto.AttachmentPayload{Filename: "proof.txt", SizeBytes: -1}, proto.NormalizeAttachmentPayload),
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := marshalCoreTestJSON(t, "marshal payload", tt.payload)
			reply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
				Partition:  tt.partition,
				Offset:     int64(i + 1),
				ActorID:    admin.ID,
				CID:        "cid-native-invalid-attachment-" + strings.ReplaceAll(tt.name, " ", "-"),
				Command:    tt.command,
				Payload:    raw,
				EnqueuedAt: 1234,
			})
			if reply.Err == nil || reply.Err.Code != proto.ErrValidationFailed || reply.Err.Message != tt.want || reply.Err.Retryable {
				t.Fatalf("err = %+v, want terminal validation %q", reply.Err, tt.want)
			}
		})
	}
}

func TestNativeCommandLogDecisionRejectsAttachmentCountLimits(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	executor := NewCommandLogNativeDecisionExecutor(c)
	tests := []struct {
		name      string
		command   proto.CommandName
		payload   any
		partition LogPartition
		want      string
	}{
		{
			name:    "create thread attachments",
			command: proto.CmdCreateThread,
			payload: proto.CreateThreadPayload{
				Board:       "general",
				Title:       "Too many attachments",
				Body:        "body",
				Attachments: nativeDecisionTestAttachments(proto.MaxPostAttachments + 1),
			},
			partition: LogPartition{Kind: partitionBoard, Key: "general"},
			want:      proto.ValidatePostAttachmentCount(proto.MaxPostAttachments + 1),
		},
		{
			name:    "send mail attachments",
			command: proto.CmdSendMail,
			payload: proto.SendMailPayload{
				To:          []string{bob.Name},
				Subject:     "Too many attachments",
				Body:        "body",
				Attachments: nativeDecisionTestAttachments(proto.MaxMailAttachments + 1),
			},
			partition: LogPartition{Kind: partitionMail, Key: admin.ID},
			want:      proto.ValidateMailAttachmentCount(proto.MaxMailAttachments + 1),
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := marshalCoreTestJSON(t, "marshal payload", tt.payload)
			reply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
				Partition:  tt.partition,
				Offset:     int64(i + 1),
				ActorID:    admin.ID,
				CID:        "cid-native-attachment-count-" + strings.ReplaceAll(tt.name, " ", "-"),
				Command:    tt.command,
				Payload:    raw,
				EnqueuedAt: 1234,
			})
			if reply.Err == nil || reply.Err.Code != proto.ErrValidationFailed || reply.Err.Message != tt.want || reply.Err.Retryable {
				t.Fatalf("err = %+v, want terminal validation %q", reply.Err, tt.want)
			}
		})
	}
}

func TestNativeCommandLogDecisionRejectsOverlongModerationReasons(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	longReason := strings.Repeat("x", proto.MaxModerationReasonLength+1)
	_, want := proto.NormalizeModerationReason(longReason)
	tests := []struct {
		name    string
		command proto.CommandName
		payload any
	}{
		{
			name:    "sanction",
			command: proto.CmdSanctionUser,
			payload: proto.SanctionUserPayload{
				User:   alice.ID,
				Kind:   "mute",
				Reason: longReason,
			},
		},
		{
			name:    "clear sanction",
			command: proto.CmdClearUserSanction,
			payload: proto.ClearUserSanctionPayload{
				User:   alice.ID,
				Kind:   "mute",
				Reason: longReason,
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := marshalCoreTestJSON(t, "marshal payload", tt.payload)
			reply := NewCommandLogNativeDecisionExecutor(c).ExecuteCommandLogRecord(ctx, CommandLogRecord{
				Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
				Offset:     int64(i + 1),
				ActorID:    admin.ID,
				CID:        "cid-native-long-reason-" + strings.ReplaceAll(tt.name, " ", "-"),
				Command:    tt.command,
				Payload:    raw,
				EnqueuedAt: 1234,
			})
			if reply.Err == nil || reply.Err.Code != proto.ErrValidationFailed || reply.Err.Message != want || reply.Err.Retryable {
				t.Fatalf("err = %+v, want terminal validation %q", reply.Err, want)
			}
		})
	}
}

func nativeDecisionTestAttachments(count int) []proto.AttachmentPayload {
	attachments := make([]proto.AttachmentPayload, 0, count)
	for i := 0; i < count; i++ {
		attachments = append(attachments, proto.AttachmentPayload{
			Filename: fmt.Sprintf("proof-%02d.txt", i),
		})
	}
	return attachments
}

func validationMessage[T any](value T, normalize func(T) (T, string)) string {
	_, msg := normalize(value)
	return msg
}

func favoriteFolderNameValidationMessage(name string, required bool) string {
	_, msg := proto.NormalizeFavoriteFolderName(name, required)
	return msg
}

func statsSnapshotDateValidationMessage(date string, ts int64) string {
	_, _, msg := proto.NormalizeStatsSnapshotDate(date, ts)
	return msg
}

func TestNativeCommandLogDecisionExecutorProjectsPostBoardMail(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	registerNativeDecisionTestUser(t, c, "admin")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	expectMailErr := func(partition LogPartition, payload proto.PostBoardMailPayload, wantCode, wantMessage string) {
		t.Helper()
		raw := marshalCoreTestJSON(t, "marshal postBoardMail payload", payload)
		reply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
			Partition:  partition,
			Offset:     900,
			ActorID:    bob.ID,
			CID:        "cid-native-post-board-mail-reject-" + wantCode,
			Command:    proto.CmdPostBoardMail,
			Payload:    raw,
			EnqueuedAt: 1234,
		})
		if reply.Err == nil || reply.Err.Code != wantCode || reply.Err.Message != wantMessage || reply.Err.Retryable {
			t.Fatalf("postBoardMail reply = %+v, want terminal %s %q", reply, wantCode, wantMessage)
		}
	}
	expectMailErr(boardPartition, proto.PostBoardMailPayload{
		Board:   "general",
		Subject: "Blocked mail thread",
		Body:    "blocked from mail",
	}, proto.ErrForbidden, "board mail-in is disabled")

	mailInAllowed := true
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{MailInAllowed: &mailInAllowed}); err != nil {
		t.Fatalf("enable mail-in: %v", err)
	}
	createPayload := marshalCoreTestJSON(t, "marshal create mail payload", proto.PostBoardMailPayload{
		Board:       "general",
		Subject:     " Mail thread ",
		Body:        " posted from mail ",
		ContentType: "ansi-art",
	})
	createRecord, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create mail", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-board-mail-create",
		Command:    proto.CmdPostBoardMail,
		Payload:    createPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "create mail events", proto.EvtThreadNew, proto.EvtPostAppended)
	threadEvent := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "create mail thread payload")
	rootEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "create mail post payload")
	wantThreadID := stableCommandLogDecisionID("thr_", createRecord, 0)
	wantRootID := stableCommandLogDecisionID("pst_", createRecord, 1)
	if threadEvent.ID != wantThreadID || threadEvent.Board != "general" || threadEvent.Title != "Mail thread" || threadEvent.AuthorID != bob.ID || threadEvent.TS != 2234 {
		t.Fatalf("create mail thread = %+v, want deterministic mail-in thread", threadEvent)
	}
	if rootEvent.ID != wantRootID || rootEvent.Thread != wantThreadID || rootEvent.Body != "posted from mail" || rootEvent.RawBody != "posted from mail" ||
		rootEvent.AuthorID != bob.ID || rootEvent.ContentType != "ansi-art" || rootEvent.TS != 2234 {
		t.Fatalf("create mail root post = %+v, want deterministic mail-in root post", rootEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-post-board-mail-test", boardPartition, 10, "materialize create mail events")
	threads, err := c.ListThreads("general", 10, 0)
	if err != nil {
		t.Fatalf("list threads after create mail: %v", err)
	}
	if len(threads) == 0 || threads[0].ID != wantThreadID || threads[0].Title != "Mail thread" {
		t.Fatalf("threads after create mail = %+v, want projected mail-in thread", threads)
	}
	posts, err := c.ListPosts(wantThreadID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after create mail: %v", err)
	}
	if len(posts) != 1 || posts[0].ID != wantRootID || posts[0].Body != "posted from mail" || posts[0].ContentType != "ansi-art" {
		t.Fatalf("posts after create mail = %+v, want projected mail-in root post", posts)
	}
	retryCreateReply := executor.ExecuteCommandLogRecord(ctx, createRecord)
	if retryCreateReply.Err != nil || retryCreateReply.Result == nil || retryCreateReply.Result.ID != wantThreadID {
		t.Fatalf("create mail retry reply = %+v, want idempotent thread ack", retryCreateReply)
	}
	retryCreateEvents, err := executor.DecideCommandLogEvents(ctx, createRecord, retryCreateReply)
	if err != nil {
		t.Fatalf("decide create mail retry events: %v", err)
	}
	if len(retryCreateEvents) != 2 ||
		retryCreateEvents[0].ID != stableCommandLogDecisionID("evt_", createRecord, 0) ||
		retryCreateEvents[1].ID != stableCommandLogDecisionID("evt_", createRecord, 1) {
		t.Fatalf("create mail retry events = %+v, want stable event ids", retryCreateEvents)
	}

	replyPartition := LogPartition{Kind: partitionBoard, Key: wantThreadID}
	replyPayload := marshalCoreTestJSON(t, "marshal reply mail payload", proto.PostBoardMailPayload{
		Thread: wantThreadID,
		Body:   " mail reply ",
	})
	replyRecord, replyEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "reply mail", boardPartition, 2, 10, CommandLogRecord{
		Partition:  replyPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-board-mail-reply",
		Command:    proto.CmdPostBoardMail,
		Payload:    replyPayload,
		EnqueuedAt: 3234,
	})
	if len(replyEvents) != 1 || replyEvents[0].Kind != proto.EvtPostAppended || replyEvents[0].PartitionOffset != 3 {
		t.Fatalf("reply mail events = %+v, want one board-local post.appended", replyEvents)
	}
	replyEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, replyEvents[0], "reply mail payload")
	wantReplyID := stableCommandLogDecisionID("pst_", replyRecord, 0)
	if replyEvent.ID != wantReplyID || replyEvent.Thread != wantThreadID || replyEvent.Body != "mail reply" || replyEvent.RawBody != "mail reply" ||
		replyEvent.AuthorID != bob.ID || replyEvent.ContentType != "markup" || replyEvent.TS != 3234 {
		t.Fatalf("reply mail post = %+v, want deterministic mail-in reply", replyEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-post-board-mail-test", boardPartition, 10, "materialize reply mail event")
	posts, err = c.ListPosts(wantThreadID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after reply mail: %v", err)
	}
	if len(posts) != 2 || posts[1].ID != wantReplyID || posts[1].Body != "mail reply" {
		t.Fatalf("posts after reply mail = %+v, want projected mail-in reply", posts)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, replyRecord)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != wantReplyID {
		t.Fatalf("reply mail retry reply = %+v, want idempotent post ack", retryReply)
	}
	retryReplyEvents, err := executor.DecideCommandLogEvents(ctx, replyRecord, retryReply)
	if err != nil {
		t.Fatalf("decide reply mail retry events: %v", err)
	}
	if len(retryReplyEvents) != 1 || retryReplyEvents[0].ID != stableCommandLogDecisionID("evt_", replyRecord, 0) {
		t.Fatalf("reply mail retry events = %+v, want stable event id", retryReplyEvents)
	}

	pollBody := "[poll]\nMail poll?\nYes\nNo\n[/poll]"
	pollPayload := marshalCoreTestJSON(t, "marshal poll mail payload", proto.PostBoardMailPayload{
		Thread: wantThreadID,
		Body:   pollBody,
	})
	lowTrustPoll := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  replyPartition,
		Offset:     901,
		ActorID:    bob.ID,
		CID:        "cid-native-post-board-mail-poll-low-trust",
		Command:    proto.CmdPostBoardMail,
		Payload:    pollPayload,
		EnqueuedAt: 4234,
	})
	requireNativeDecisionTerminalError(t, lowTrustPoll, proto.ErrForbidden, "low-trust poll mail reply")
	if err := setNativeDecisionTestTrustLevel(c, bob.ID, 2); err != nil {
		t.Fatalf("raise bob trust level: %v", err)
	}
	pollRecord, pollEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "poll mail", boardPartition, 3, 10, CommandLogRecord{
		Partition:  replyPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-board-mail-poll",
		Command:    proto.CmdPostBoardMail,
		Payload:    pollPayload,
		EnqueuedAt: 4234,
	})
	if len(pollEvents) != 1 || pollEvents[0].Kind != proto.EvtPostAppended || pollEvents[0].PartitionOffset != 4 {
		t.Fatalf("poll mail events = %+v, want one board-local post.appended", pollEvents)
	}
	pollEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, pollEvents[0], "poll mail payload")
	wantPollPostID := stableCommandLogDecisionID("pst_", pollRecord, 0)
	if pollEvent.ID != wantPollPostID || pollEvent.Thread != wantThreadID || pollEvent.Body != "" || pollEvent.RawBody != pollBody ||
		pollEvent.AuthorID != bob.ID || pollEvent.TS != 4234 {
		t.Fatalf("poll mail post = %+v, want stripped body and raw poll body", pollEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-post-board-mail-test", boardPartition, 10, "materialize poll mail event")
	projectedPoll, err := c.GetPollByPostID(wantPollPostID)
	if err != nil {
		t.Fatalf("get mail poll: %v", err)
	}
	if projectedPoll == nil || projectedPoll.Question != "Mail poll?" {
		t.Fatalf("mail poll = %+v, want projected poll question", projectedPoll)
	}

	expectMailErr(LogPartition{Kind: partitionBoard, Key: "secret"}, proto.PostBoardMailPayload{
		Board:  "secret",
		Thread: wantThreadID,
		Body:   "wrong board",
	}, proto.ErrValidationFailed, "thread does not belong to board")
}

func TestNativeCommandLogDecisionExecutorProjectsPostMailToBoard(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	sourceMailID := "mail_native_post_to_board_source"
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin source mail seed: %v", err)
	}
	if err := projections.InsertMailMessage(tx, sourceMailID, alice.ID, "Campus plans", "Meet in the lab at six.", "", 1734, 1); err != nil {
		tx.Rollback() //nolint:errcheck
		t.Fatalf("insert source mail message: %v", err)
	}
	if err := projections.InsertMailCopy(tx, sourceMailID, alice.ID, "sender", "sent", true, false, 1734); err != nil {
		tx.Rollback() //nolint:errcheck
		t.Fatalf("insert source sender copy: %v", err)
	}
	if err := projections.InsertMailCopy(tx, sourceMailID, bob.ID, "recipient", "inbox", false, false, 1734); err != nil {
		tx.Rollback() //nolint:errcheck
		t.Fatalf("insert source recipient copy: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit source mail seed: %v", err)
	}
	sourceMail, err := c.GetMail(bob.ID, sourceMailID)
	if err != nil {
		t.Fatalf("get bob source mail: %v", err)
	}
	if sourceMail == nil || sourceMail.Subject != "Campus plans" {
		t.Fatalf("bob source mail = %+v, want visible mail copy", sourceMail)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	invisiblePayload := marshalCoreTestJSON(t, "marshal invisible post-mail payload", proto.PostMailToBoardPayload{
		Mail:  sourceMail.ID,
		Board: "general",
	})
	invisible := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-post-mail-to-board-invisible",
		Command:    proto.CmdPostMailToBoard,
		Payload:    invisiblePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, invisible, proto.ErrNotFound, "invisible source mail reply", "mail not found")

	createPayload := marshalCoreTestJSON(t, "marshal create post-mail payload", proto.PostMailToBoardPayload{
		Mail:    sourceMail.ID,
		Board:   "general",
		Subject: " Shared campus mail ",
		Note:    "Please discuss this one.",
	})
	createRecord, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create post-mail", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-mail-to-board-create",
		Command:    proto.CmdPostMailToBoard,
		Payload:    createPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "create post-mail events", proto.EvtThreadNew, proto.EvtPostAppended)
	threadEvent := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "create post-mail thread payload")
	rootEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "create post-mail root payload")
	wantThreadID := stableCommandLogDecisionID("thr_", createRecord, 0)
	wantRootID := stableCommandLogDecisionID("pst_", createRecord, 1)
	if threadEvent.ID != wantThreadID || threadEvent.Board != "general" || threadEvent.Title != "Shared campus mail" ||
		threadEvent.AuthorID != bob.ID || threadEvent.TS != 2234 {
		t.Fatalf("create post-mail thread = %+v, want deterministic mail board thread", threadEvent)
	}
	if rootEvent.ID != wantRootID || rootEvent.Thread != wantThreadID || rootEvent.AuthorID != bob.ID ||
		rootEvent.ContentType != "markup" || rootEvent.TS != 2234 {
		t.Fatalf("create post-mail root = %+v, want deterministic mail board post", rootEvent)
	}
	for _, want := range []string{"Please discuss this one.", "Posted from private mail.", "From: alice", "To: bob", "Subject: Campus plans", "Meet in the lab at six."} {
		if !strings.Contains(rootEvent.Body, want) || !strings.Contains(rootEvent.RawBody, want) {
			t.Fatalf("create post-mail body missing %q:\nbody=%s\nraw=%s", want, rootEvent.Body, rootEvent.RawBody)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-post-mail-to-board-test", boardPartition, 10, "materialize create post-mail events")
	threads, err := c.ListThreads("general", 10, 0)
	if err != nil {
		t.Fatalf("list threads after post-mail create: %v", err)
	}
	if len(threads) == 0 || threads[0].ID != wantThreadID || threads[0].Title != "Shared campus mail" {
		t.Fatalf("threads after post-mail create = %+v, want projected mail board thread", threads)
	}
	posts, err := c.ListPosts(wantThreadID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after post-mail create: %v", err)
	}
	if len(posts) != 1 || posts[0].ID != wantRootID || !strings.Contains(posts[0].Body, "Posted from private mail.") {
		t.Fatalf("posts after post-mail create = %+v, want projected mail board root", posts)
	}
	retryCreateReply := executor.ExecuteCommandLogRecord(ctx, createRecord)
	if retryCreateReply.Err != nil || retryCreateReply.Result == nil || retryCreateReply.Result.ID != wantThreadID {
		t.Fatalf("create post-mail retry reply = %+v, want idempotent thread ack", retryCreateReply)
	}
	retryCreateEvents, err := executor.DecideCommandLogEvents(ctx, createRecord, retryCreateReply)
	if err != nil {
		t.Fatalf("decide create post-mail retry events: %v", err)
	}
	if len(retryCreateEvents) != 2 ||
		retryCreateEvents[0].ID != stableCommandLogDecisionID("evt_", createRecord, 0) ||
		retryCreateEvents[1].ID != stableCommandLogDecisionID("evt_", createRecord, 1) {
		t.Fatalf("create post-mail retry events = %+v, want stable thread/post events", retryCreateEvents)
	}

	replyPartition := LogPartition{Kind: partitionBoard, Key: wantThreadID}
	replyPayload := marshalCoreTestJSON(t, "marshal reply post-mail payload", proto.PostMailToBoardPayload{
		Mail:   sourceMail.ID,
		Thread: wantThreadID,
		Note:   "Follow-up from mail.",
	})
	replyRecord, replyEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "reply post-mail", boardPartition, 2, 10, CommandLogRecord{
		Partition:  replyPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-mail-to-board-reply",
		Command:    proto.CmdPostMailToBoard,
		Payload:    replyPayload,
		EnqueuedAt: 3234,
	})
	if len(replyEvents) != 1 || replyEvents[0].Kind != proto.EvtPostAppended || replyEvents[0].PartitionOffset != 3 {
		t.Fatalf("reply post-mail events = %+v, want one board-local post.appended", replyEvents)
	}
	replyEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, replyEvents[0], "reply post-mail payload")
	wantReplyID := stableCommandLogDecisionID("pst_", replyRecord, 0)
	if replyEvent.ID != wantReplyID || replyEvent.Thread != wantThreadID || replyEvent.AuthorID != bob.ID ||
		replyEvent.ContentType != "markup" || replyEvent.TS != 3234 || !strings.Contains(replyEvent.Body, "Follow-up from mail.") {
		t.Fatalf("reply post-mail post = %+v, want deterministic mail board reply", replyEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-post-mail-to-board-test", boardPartition, 10, "materialize reply post-mail event")
	posts, err = c.ListPosts(wantThreadID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after post-mail reply: %v", err)
	}
	if len(posts) != 2 || posts[1].ID != wantReplyID || !strings.Contains(posts[1].Body, "Follow-up from mail.") {
		t.Fatalf("posts after post-mail reply = %+v, want projected mail board reply", posts)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, replyRecord)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != wantReplyID {
		t.Fatalf("reply post-mail retry reply = %+v, want idempotent post ack", retryReply)
	}
	retryReplyEvents, err := executor.DecideCommandLogEvents(ctx, replyRecord, retryReply)
	if err != nil {
		t.Fatalf("decide reply post-mail retry events: %v", err)
	}
	if len(retryReplyEvents) != 1 || retryReplyEvents[0].ID != stableCommandLogDecisionID("evt_", replyRecord, 0) {
		t.Fatalf("reply post-mail retry events = %+v, want stable post event", retryReplyEvents)
	}
}

func TestNativeCommandLogDecisionExecutorHonorsBoardPostingPolicy(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")
	if err := projections.SetBoardModerator(c.DB, "general", alice.ID, alice.ID, true, nil); err != nil {
		t.Fatalf("set alice board moderator: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)
	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	expectNativeErr := func(actor *User, partition LogPartition, command proto.CommandName, payload any, wantCode string) {
		t.Helper()
		raw := marshalCoreTestJSON(t, fmt.Sprintf("marshal %s payload", command), payload)
		reply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
			Partition:  partition,
			Offset:     900,
			ActorID:    actor.ID,
			CID:        "cid-native-policy-reject-" + string(command),
			Command:    command,
			Payload:    raw,
			EnqueuedAt: 1234,
		})
		if reply.Err == nil || reply.Err.Code != wantCode || reply.Err.Retryable {
			t.Fatalf("%s reply = %+v, want terminal %s", command, reply, wantCode)
		}
	}
	produceAndDrain := func(actor *User, partition LogPartition, command proto.CommandName, payload any, cid string, ts int64) CommandLogRecord {
		t.Helper()
		raw := marshalCoreTestJSON(t, fmt.Sprintf("marshal %s payload", command), payload)
		return produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, string(command), CommandLogRecord{
			Partition:  partition,
			ActorID:    actor.ID,
			CID:        cid,
			Command:    command,
			Payload:    raw,
			EnqueuedAt: ts,
		})
	}
	materializeBoard := func(wantApplied int) []*proto.Event {
		t.Helper()
		result := materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-policy-test", boardPartition, 100, "materialize board policy events")
		if result.Applied != wantApplied {
			t.Fatalf("materialization result = %+v, want %d applied events", result, wantApplied)
		}
		events := replayCommandLogWorkerPartition(t, ctx, eventStore, boardPartition, 0, 100, "replay board policy events")
		return events
	}

	readOnly := true
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{ReadOnly: &readOnly}); err != nil {
		t.Fatalf("set read-only board: %v", err)
	}
	createPayload := proto.CreateThreadPayload{
		Board: "general",
		Title: "Native read-only moderator topic",
		Body:  "moderator can write through read-only policy",
	}
	expectNativeErr(bob, boardPartition, proto.CmdCreateThread, createPayload, proto.ErrForbidden)
	produceAndDrain(alice, boardPartition, proto.CmdCreateThread, createPayload, "cid-native-policy-readonly-create", 1234)
	events := materializeBoard(2)
	requireNativeDecisionEventKinds(t, events, "events after read-only create", proto.EvtThreadNew, proto.EvtPostAppended)
	threadEvent := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	rootEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root event payload")
	if threadEvent.AuthorID != alice.ID || rootEvent.AuthorID != alice.ID {
		t.Fatalf("read-only moderator event authors = %+v / %+v, want alice", threadEvent, rootEvent)
	}

	threadPartition := LogPartition{Kind: partitionThread, Key: threadEvent.ID}
	noReply := true
	readOnly = false
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{ReadOnly: &readOnly, NoReply: &noReply}); err != nil {
		t.Fatalf("set no-reply board: %v", err)
	}
	expectNativeErr(bob, threadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadEvent.ID,
		Body:   "ordinary reply blocked by board no-reply",
	}, proto.ErrForbidden)
	produceAndDrain(alice, threadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadEvent.ID,
		Body:   "moderator no-reply board bypass",
	}, "cid-native-policy-noreply-board", 2234)
	events = materializeBoard(1)
	if len(events) != 3 || events[2].Kind != proto.EvtPostAppended {
		t.Fatalf("events after no-reply append = %+v, want moderator post.appended", events)
	}
	noReplyBypassEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[2], "no-reply bypass payload")
	if noReplyBypassEvent.AuthorID != alice.ID || noReplyBypassEvent.Body != "moderator no-reply board bypass" {
		t.Fatalf("no-reply bypass event = %+v, want alice moderator reply", noReplyBypassEvent)
	}

	if _, err := qExec(c.DB, `UPDATE threads SET locked=1 WHERE id=?`, threadEvent.ID); err != nil {
		t.Fatalf("lock thread directly: %v", err)
	}
	expectNativeErr(bob, threadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadEvent.ID,
		Body:   "ordinary reply blocked by lock",
	}, proto.ErrThreadLocked)
	produceAndDrain(alice, threadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadEvent.ID,
		Body:   "moderator locked thread bypass",
	}, "cid-native-policy-locked-thread", 3234)
	events = materializeBoard(1)
	if len(events) != 4 {
		t.Fatalf("events after locked append = %+v, want fourth event", events)
	}

	noReply = false
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{NoReply: &noReply}); err != nil {
		t.Fatalf("clear no-reply board: %v", err)
	}
	if _, err := qExec(c.DB, `UPDATE threads SET locked=0 WHERE id=?`, threadEvent.ID); err != nil {
		t.Fatalf("unlock thread directly: %v", err)
	}
	if _, err := qExec(c.DB, `UPDATE posts SET no_reply=1 WHERE id=?`, rootEvent.ID); err != nil {
		t.Fatalf("set root post no-reply directly: %v", err)
	}
	expectNativeErr(bob, threadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadEvent.ID,
		Body:   "ordinary reply blocked by root no-reply",
	}, proto.ErrForbidden)
	produceAndDrain(alice, threadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: threadEvent.ID,
		Body:   "moderator root no-reply bypass",
	}, "cid-native-policy-root-noreply", 4234)
	events = materializeBoard(1)
	if len(events) != 5 {
		t.Fatalf("events after root no-reply append = %+v, want fifth event", events)
	}

	if _, err := qExec(c.DB, `UPDATE posts SET no_reply=0 WHERE id=?`, rootEvent.ID); err != nil {
		t.Fatalf("clear root post no-reply directly: %v", err)
	}
	replyForParentNoReply := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[4], "root no-reply bypass payload")
	if _, err := qExec(c.DB, `UPDATE posts SET no_reply=1 WHERE id=?`, replyForParentNoReply.ID); err != nil {
		t.Fatalf("set parent reply no-reply directly: %v", err)
	}
	expectNativeErr(bob, threadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread:  threadEvent.ID,
		ReplyTo: replyForParentNoReply.ID,
		Body:    "ordinary directed reply blocked by article no-reply",
	}, proto.ErrForbidden)
	produceAndDrain(alice, threadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread:  threadEvent.ID,
		ReplyTo: replyForParentNoReply.ID,
		Body:    "moderator article no-reply bypass",
	}, "cid-native-policy-article-noreply", 5234)
	events = materializeBoard(1)
	if len(events) != 6 {
		t.Fatalf("events after article no-reply append = %+v, want sixth event", events)
	}
	articleNoReplyBypassEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[5], "article no-reply bypass payload")
	if articleNoReplyBypassEvent.ReplyTo != replyForParentNoReply.ID {
		t.Fatalf("article no-reply bypass ReplyTo = %q, want %q", articleNoReplyBypassEvent.ReplyTo, replyForParentNoReply.ID)
	}

	memberMode := true
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{MemberReadMode: &memberMode, MemberPostMode: &memberMode}); err != nil {
		t.Fatalf("set member board modes: %v", err)
	}
	memberCreatePayload := proto.CreateThreadPayload{
		Board: "general",
		Title: "Native member topic",
		Body:  "member can create through member board policy",
	}
	expectNativeErr(carol, boardPartition, proto.CmdCreateThread, memberCreatePayload, proto.ErrForbidden)
	if err := projections.SetBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{}); err != nil {
		t.Fatalf("set bob board member: %v", err)
	}
	produceAndDrain(bob, boardPartition, proto.CmdCreateThread, memberCreatePayload, "cid-native-policy-member-create", 6234)
	events = materializeBoard(2)
	if len(events) != 8 || events[6].Kind != proto.EvtThreadNew || events[7].Kind != proto.EvtPostAppended {
		t.Fatalf("events after member create = %+v, want second native thread", events)
	}
	memberThreadEvent := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[6], "member thread event payload")
	if memberThreadEvent.AuthorID != bob.ID {
		t.Fatalf("member thread event = %+v, want bob author", memberThreadEvent)
	}
	memberThreadPartition := LogPartition{Kind: partitionThread, Key: memberThreadEvent.ID}
	expectNativeErr(carol, memberThreadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: memberThreadEvent.ID,
		Body:   "non-member reply blocked",
	}, proto.ErrForbidden)
	produceAndDrain(bob, memberThreadPartition, proto.CmdAppendPost, proto.AppendPostPayload{
		Thread: memberThreadEvent.ID,
		Body:   "member reply through native policy",
	}, "cid-native-policy-member-append", 7234)
	events = materializeBoard(1)
	if len(events) != 9 {
		t.Fatalf("events after member append = %+v, want ninth event", events)
	}
	memberReplyEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[8], "member reply payload")
	if memberReplyEvent.AuthorID != bob.ID || memberReplyEvent.Body != "member reply through native policy" {
		t.Fatalf("member reply event = %+v, want bob member reply", memberReplyEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsMentionNotifications(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native mentions",
		Body:  "hello @bob from broker root",
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-mention",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mention-test", partition, 10, "materialize create events")
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed outbox jobs after create = %d, want root post job", processed)
	}
	notifications, err := c.ListNotifications(bob.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("ListNotifications(bob) after create: %v", err)
	}
	if len(notifications) != 1 || notifications[0].Kind != "mention" || notifications[0].Actor != alice.Name {
		t.Fatalf("notifications after create = %+v, want one broker-projected mention for bob", notifications)
	}

	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	appendPayload := marshalCoreTestJSON(t, "marshal append payload", proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "reply also says @bob",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "append", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-mention",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mention-test", partition, 10, "materialize append event")
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed outbox jobs after append = %d, want reply post job", processed)
	}
	notifications, err = c.ListNotifications(bob.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("ListNotifications(bob) after append: %v", err)
	}
	if len(notifications) != 2 ||
		notifications[0].Kind != "mention" ||
		notifications[1].Kind != "mention" ||
		notifications[0].PostID == notifications[1].PostID {
		t.Fatalf("notifications after append = %+v, want two distinct mention notifications for bob", notifications)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsWatchedThreadNotifications(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native watched thread",
		Body:  "root post",
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-watch",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-watch-test", partition, 10, "materialize create events")
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed outbox jobs after create = %d, want root post job", processed)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	if err := projections.SetThreadPref(c.DB, bob.ID, threadPayload.ID, "watch"); err != nil {
		t.Fatalf("set thread watch: %v", err)
	}

	appendPayload := marshalCoreTestJSON(t, "marshal append payload", proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "reply for a watcher",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "append", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-watch",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-watch-test", partition, 10, "materialize append event")
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed outbox jobs after append = %d, want reply post job", processed)
	}
	notifications, err := c.ListNotifications(bob.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("ListNotifications(bob): %v", err)
	}
	if len(notifications) != 1 ||
		notifications[0].Kind != "watched" ||
		notifications[0].ThreadID != threadPayload.ID ||
		notifications[0].Actor != alice.Name {
		t.Fatalf("notifications = %+v, want broker-projected watched-thread notification for bob", notifications)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsInlineAttachmentMetadata(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	attachmentsAllowed := true
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{AttachmentsAllowed: &attachmentsAllowed}); err != nil {
		t.Fatalf("enable attachments: %v", err)
	}

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native attachment metadata",
		Body:  "root with an inline attachment",
		Attachments: []proto.AttachmentPayload{{
			ID:          "client-root-attachment-id",
			Filename:    " root.png ",
			ContentType: " image/png ",
			SizeBytes:   123,
			URL:         " https://cdn.example/root.png ",
		}},
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-attachment",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-attachment-test", partition, 10, "materialize create events")
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root post payload")
	if len(rootPostPayload.Attachments) != 1 {
		t.Fatalf("root event attachments = %+v, want one attachment", rootPostPayload.Attachments)
	}
	rootAttachment := rootPostPayload.Attachments[0]
	if !strings.HasPrefix(rootAttachment.ID, "att_") || rootAttachment.ID == "client-root-attachment-id" {
		t.Fatalf("root attachment id = %q, want deterministic native att_ id", rootAttachment.ID)
	}
	if rootAttachment.Filename != "root.png" || rootAttachment.ContentType != "image/png" || rootAttachment.SizeBytes != 123 || rootAttachment.URL != "https://cdn.example/root.png" {
		t.Fatalf("root attachment = %+v, want normalized metadata", rootAttachment)
	}

	appendPayload := marshalCoreTestJSON(t, "marshal append payload", proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "reply with an inline attachment",
		Attachments: []proto.AttachmentPayload{{
			ID:          "client-reply-attachment-id",
			Filename:    "reply.txt",
			ContentType: "text/plain",
			SizeBytes:   456,
			URL:         "https://cdn.example/reply.txt",
		}},
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "append", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-attachment",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-attachment-test", partition, 10, "materialize append event")

	posts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("posts = %+v, want root and reply", posts)
	}
	if len(posts[0].Attachments) != 1 || posts[0].Attachments[0].ID != rootAttachment.ID || posts[0].Attachments[0].CreatedBy != alice.ID || posts[0].Attachments[0].Stored {
		t.Fatalf("root materialized attachments = %+v, want projected metadata without stored blob", posts[0].Attachments)
	}
	if len(posts[1].Attachments) != 1 {
		t.Fatalf("reply materialized attachments = %+v, want one attachment", posts[1].Attachments)
	}
	replyAttachment := posts[1].Attachments[0]
	if !strings.HasPrefix(replyAttachment.ID, "att_") || replyAttachment.ID == "client-reply-attachment-id" {
		t.Fatalf("reply attachment id = %q, want deterministic native att_ id", replyAttachment.ID)
	}
	if replyAttachment.PostID != posts[1].ID || replyAttachment.Filename != "reply.txt" || replyAttachment.ContentType != "text/plain" || replyAttachment.SizeBytes != 456 || replyAttachment.URL != "https://cdn.example/reply.txt" || replyAttachment.CreatedBy != alice.ID || replyAttachment.Stored {
		t.Fatalf("reply attachment = %+v, want projected metadata without stored blob", replyAttachment)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsAttachPostMetadata(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	attachmentsAllowed := true
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{AttachmentsAllowed: &attachmentsAllowed}); err != nil {
		t.Fatalf("enable attachments: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native standalone attachment metadata",
		Body:  "root post",
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-attach-post-create-thread",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-attach-post-test", partition, 10, "materialize create events")
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root post payload")

	missingStagedPayload := marshalCoreTestJSON(t, "marshal missing staged attach payload", proto.AttachPostPayload{
		ID:           "att_missing_staged",
		Post:         rootPostPayload.ID,
		Filename:     "missing.bin",
		ContentType:  "application/octet-stream",
		SizeBytes:    7,
		StagedBlobID: "missing-staged-blob",
	})
	missingStagedReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionPost, Key: rootPostPayload.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-attach-post-missing-staged",
		Command:    proto.CmdAttachPost,
		Payload:    missingStagedPayload,
		EnqueuedAt: 2234,
	})
	if missingStagedReply.Err == nil || !missingStagedReply.Err.Retryable || missingStagedReply.Err.Code != proto.ErrBlobStagingRequired {
		t.Fatalf("missing staged attach reply = %+v, want retryable blob staging failure", missingStagedReply)
	}

	attachPayload := marshalCoreTestJSON(t, "marshal attach payload", proto.AttachPostPayload{
		Post:        rootPostPayload.ID,
		Filename:    " proof.txt ",
		ContentType: " text/plain ",
		SizeBytes:   42,
	})
	attachPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}
	attachRecord, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "attach", partition, 0, 10, CommandLogRecord{
		Partition:  attachPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-attach-post-metadata",
		Command:    proto.CmdAttachPost,
		Payload:    attachPayload,
		EnqueuedAt: 2234,
	})
	if len(events) != 3 {
		t.Fatalf("events = %+v, want thread.new, post.appended, post.attachment_added", events)
	}
	attachmentPayload := requireNativeDecisionPayload[proto.PostAttachmentAddedPayload](t, events[2], "attachment event payload")
	expectedAttachmentID := stableCommandLogDecisionID("att_", attachRecord, 0)
	if attachmentPayload.ID != expectedAttachmentID || attachmentPayload.Post != rootPostPayload.ID || attachmentPayload.Thread != threadPayload.ID ||
		attachmentPayload.Filename != "proof.txt" || attachmentPayload.ContentType != "text/plain" || attachmentPayload.SizeBytes != 42 ||
		attachmentPayload.AuthorID != alice.ID || attachmentPayload.TS != 2234 {
		t.Fatalf("attachment event = %+v, want normalized deterministic metadata", attachmentPayload)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-attach-post-test", partition, 10, "materialize attach event")
	posts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(posts) != 1 || len(posts[0].Attachments) != 1 {
		t.Fatalf("posts = %+v, want root post with one attachment", posts)
	}
	attachment := posts[0].Attachments[0]
	if attachment.ID != expectedAttachmentID || attachment.PostID != rootPostPayload.ID || attachment.Filename != "proof.txt" ||
		attachment.ContentType != "text/plain" || attachment.SizeBytes != 42 || attachment.URL != "" ||
		attachment.CreatedBy != alice.ID || attachment.CreatedAt != 2234 || attachment.Stored {
		t.Fatalf("materialized attachment = %+v, want metadata-only projected attachment", attachment)
	}

	stagedAttachmentID := "att_native_staged"
	stagedBytes := []byte("blobbed")
	if err := c.StagePostAttachmentBlob(stagedAttachmentID, alice.ID, stagedBytes, "application/octet-stream"); err != nil {
		t.Fatalf("stage post attachment blob: %v", err)
	}
	stagedPayload := marshalCoreTestJSON(t, "marshal staged attach payload", proto.AttachPostPayload{
		ID:           stagedAttachmentID,
		Post:         rootPostPayload.ID,
		Filename:     "blob.bin",
		ContentType:  "application/octet-stream",
		SizeBytes:    int64(len(stagedBytes)),
		StagedBlobID: stagedAttachmentID,
	})
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "staged attach", partition, 0, 10, CommandLogRecord{
		Partition:  attachPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-attach-post-staged",
		Command:    proto.CmdAttachPost,
		Payload:    stagedPayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 4 {
		t.Fatalf("events after staged attach = %+v, want fourth post.attachment_added", events)
	}
	stagedEvent := requireNativeDecisionPayload[proto.PostAttachmentAddedPayload](t, events[3], "staged attachment event payload")
	if stagedEvent.ID != stagedAttachmentID || stagedEvent.StagedBlobID != stagedAttachmentID ||
		stagedEvent.Filename != "blob.bin" || stagedEvent.ContentType != "application/octet-stream" ||
		stagedEvent.SizeBytes != int64(len(stagedBytes)) || stagedEvent.AuthorID != alice.ID || stagedEvent.TS != 3234 {
		t.Fatalf("staged attachment event = %+v, want staged blob metadata", stagedEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-attach-post-test", partition, 10, "materialize staged attach event")
	storedBytes, storedType, err := c.GetAttachmentBlob(stagedAttachmentID)
	if err != nil {
		t.Fatalf("get staged attachment blob: %v", err)
	}
	if !bytes.Equal(storedBytes, stagedBytes) || storedType != "application/octet-stream" {
		t.Fatalf("stored staged blob = %q/%q, want %q/application/octet-stream", storedBytes, storedType, stagedBytes)
	}
	posts, err = c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after staged attach: %v", err)
	}
	if len(posts) != 1 || len(posts[0].Attachments) != 2 {
		t.Fatalf("posts after staged attach = %+v, want root post with two attachments", posts)
	}
	stagedAttachment := posts[0].Attachments[1]
	if stagedAttachment.ID != stagedAttachmentID || stagedAttachment.PostID != rootPostPayload.ID ||
		stagedAttachment.Filename != "blob.bin" || stagedAttachment.ContentType != "application/octet-stream" ||
		stagedAttachment.SizeBytes != int64(len(stagedBytes)) || stagedAttachment.CreatedBy != alice.ID ||
		stagedAttachment.CreatedAt != 3234 || !stagedAttachment.Stored {
		t.Fatalf("materialized staged attachment = %+v, want stored projected attachment", stagedAttachment)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsEditPost(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native edit",
		Body:  "original body",
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-edit-post-create-thread",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-edit-post-test", partition, 10, "materialize create events")
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root post payload")
	posts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after create: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts after create = %+v, want root post", posts)
	}
	originalVersion := posts[0].Version

	pollEditPayload := marshalCoreTestJSON(t, "marshal poll edit payload", proto.EditPostPayload{
		Post: rootPostPayload.ID,
		Body: "edited with poll [poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	})
	pollEditReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionPost, Key: rootPostPayload.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-edit-post-poll-markup",
		Command:    proto.CmdEditPost,
		Payload:    pollEditPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, pollEditReply, proto.ErrValidationFailed, "poll edit reply")

	editPayload := marshalCoreTestJSON(t, "marshal edit payload", proto.EditPostPayload{
		Post: rootPostPayload.ID,
		Body: "edited native editposttoken",
	})
	editPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "edit", partition, 0, 10, CommandLogRecord{
		Partition:  editPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-edit-post",
		Command:    proto.CmdEditPost,
		Payload:    editPayload,
		EnqueuedAt: 2234,
	})
	if len(events) != 3 {
		t.Fatalf("events = %+v, want thread.new, post.appended, post.edited", events)
	}
	editEvent := requireNativeDecisionPayload[proto.PostEditedPayload](t, events[2], "edit event payload")
	if editEvent.ID != rootPostPayload.ID || editEvent.Thread != threadPayload.ID ||
		editEvent.NewBody != "edited native editposttoken" || editEvent.Version != originalVersion+1 || editEvent.TS != 2234 {
		t.Fatalf("edit event = %+v, want deterministic edit payload", editEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-edit-post-test", partition, 10, "materialize edit event")
	posts, err = c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after edit: %v", err)
	}
	if len(posts) != 1 || posts[0].Body != "edited native editposttoken" ||
		posts[0].Version != originalVersion+1 || posts[0].UpdatedAt != 2234 {
		t.Fatalf("posts after edit = %+v, want materialized edited post", posts)
	}
	searchResults, err := c.SearchReadablePosts(alice, "editposttoken", "", 10)
	if err != nil {
		t.Fatalf("search after edit: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].ID != rootPostPayload.ID || searchResults[0].Body != "edited native editposttoken" {
		t.Fatalf("search results = %+v, want edited post indexed", searchResults)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsThreadTitleAndLock(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	archiveTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin archive board setup: %v", err)
	}
	if err := projections.InsertBoard(archiveTx, "archive", "archive", "Archive board", "", 1); err != nil {
		archiveTx.Rollback() //nolint:errcheck
		t.Fatalf("insert archive board: %v", err)
	}
	if err := archiveTx.Commit(); err != nil {
		t.Fatalf("commit archive board setup: %v", err)
	}

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native title",
		Body:  "root post",
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-thread-controls-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-thread-control-test", partition, 10, "materialize create events")
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")

	titlePayload := marshalCoreTestJSON(t, "marshal title payload", proto.SetThreadTitlePayload{
		Thread: threadPayload.ID,
		Title:  " broker-native renamed thread ",
	})
	threadPartition := LogPartition{Kind: partitionThread, Key: threadPayload.ID}
	titleRecord := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "title", CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-thread-title",
		Command:    proto.CmdSetThreadTitle,
		Payload:    titlePayload,
		EnqueuedAt: 2234,
	})

	movePayload := marshalCoreTestJSON(t, "marshal move payload", proto.MoveThreadPayload{Thread: threadPayload.ID, ToBoard: "archive"})
	moveDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  threadPartition,
		Offset:     titleRecord.Offset + 1,
		ActorID:    bob.ID,
		CID:        "cid-native-thread-move-denied",
		Command:    proto.CmdMoveThread,
		Payload:    movePayload,
		EnqueuedAt: 4234,
	})
	requireNativeDecisionTerminalError(t, moveDenied, proto.ErrForbidden, "move reply without permission")

	lockPayload := marshalCoreTestJSON(t, "marshal lock payload", proto.LockThreadPayload{Thread: threadPayload.ID, Locked: true})
	lockReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  threadPartition,
		Offset:     titleRecord.Offset + 1,
		ActorID:    bob.ID,
		CID:        "cid-native-thread-lock-denied",
		Command:    proto.CmdLockThread,
		Payload:    lockPayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, lockReply, proto.ErrForbidden, "lock reply without permission")
	canModerateThreads := true
	if err := projections.SetBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{CanModerateThreads: &canModerateThreads}); err != nil {
		t.Fatalf("grant bob thread moderation: %v", err)
	}
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "lock", CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-thread-lock",
		Command:    proto.CmdLockThread,
		Payload:    lockPayload,
		EnqueuedAt: 3234,
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "move", CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-thread-move",
		Command:    proto.CmdMoveThread,
		Payload:    movePayload,
		EnqueuedAt: 4234,
	})

	events = replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 0, 10, "replay thread control events")
	if len(events) != 5 {
		t.Fatalf("events = %+v, want thread.new, post.appended, thread.title_set, thread.locked, thread.moved", events)
	}
	titleEvent := requireNativeDecisionPayload[proto.ThreadTitleSetPayload](t, events[2], "title event payload")
	if titleEvent.Thread != threadPayload.ID || titleEvent.Title != "broker-native renamed thread" || titleEvent.By != alice.ID || titleEvent.TS != 2234 {
		t.Fatalf("title event = %+v, want normalized deterministic title event", titleEvent)
	}
	lockEvent := requireNativeDecisionPayload[proto.ThreadLockedPayload](t, events[3], "lock event payload")
	if lockEvent.Thread != threadPayload.ID || !lockEvent.Locked || lockEvent.By != bob.ID || lockEvent.TS != 3234 {
		t.Fatalf("lock event = %+v, want deterministic lock event", lockEvent)
	}
	moveEvent := requireNativeDecisionPayload[proto.ThreadMovedPayload](t, events[4], "move event payload")
	if moveEvent.Thread != threadPayload.ID || moveEvent.FromBoard != "general" || moveEvent.ToBoard != "archive" || moveEvent.By != bob.ID || moveEvent.TS != 4234 {
		t.Fatalf("move event = %+v, want deterministic move event", moveEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-thread-control-test", partition, 10, "materialize thread control events")
	thread, err := c.GetThread(threadPayload.ID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if thread == nil || thread.Title != "broker-native renamed thread" || !thread.Locked || thread.Board != "archive" || thread.UpdatedAt != 4234 {
		t.Fatalf("thread after title/lock/move = %+v, want materialized title, lock, and board move", thread)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsPostFlags(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native flags",
		Body:  "root post",
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-post-flags-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-post-flags-test", partition, 10, "materialize create events")
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root post payload")
	postPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}

	marked := true
	forbiddenPayload := marshalCoreTestJSON(t, "marshal forbidden flag payload", proto.SetPostFlagPayload{
		Post:   rootPostPayload.ID,
		Marked: &marked,
	})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-post-flags-forbidden",
		Command:    proto.CmdSetPostFlag,
		Payload:    forbiddenPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "forbidden flag reply")

	canCurate := true
	if err := projections.SetBoardMember(c.DB, "general", alice.ID, true, BoardMemberPatch{CanCurate: &canCurate}); err != nil {
		t.Fatalf("grant alice curate: %v", err)
	}
	recommended := true
	tex := true
	mailBack := true
	flagPayload := marshalCoreTestJSON(t, "marshal flag payload", proto.SetPostFlagPayload{
		Post:        rootPostPayload.ID,
		Marked:      &marked,
		Recommended: &recommended,
		TeX:         &tex,
		MailBack:    &mailBack,
	})
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "flag", partition, 0, 10, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-post-flags-curator-author",
		Command:    proto.CmdSetPostFlag,
		Payload:    flagPayload,
		EnqueuedAt: 2234,
	})
	if len(events) != 3 {
		t.Fatalf("events after flag = %+v, want one post.flags_set event", events)
	}
	flagEvent := requireNativeDecisionPayload[proto.PostFlagsSetPayload](t, events[2], "flag event payload")
	if flagEvent.ID != rootPostPayload.ID || flagEvent.Thread != rootPostPayload.Thread ||
		!flagEvent.Marked || !flagEvent.Recommended || flagEvent.NoReply || !flagEvent.TeX || !flagEvent.MailBack ||
		flagEvent.By != alice.ID || flagEvent.TS != 2234 {
		t.Fatalf("flag event = %+v, want curator/author flags", flagEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-post-flags-test", partition, 10, "materialize flag event")
	post, err := c.GetPost(rootPostPayload.ID)
	if err != nil {
		t.Fatalf("get post after flag: %v", err)
	}
	if post == nil || !post.Marked || !post.Recommended || post.NoReply || !post.TeX || !post.MailBack || post.UpdatedAt != 2234 {
		t.Fatalf("post after flag = %+v, want materialized curator/author flags", post)
	}

	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "no-op flag", partition, 0, 10, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-post-flags-noop",
		Command:    proto.CmdSetPostFlag,
		Payload:    flagPayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 3 {
		t.Fatalf("events after no-op = %+v, want no additional event", events)
	}

	canModerateThreads := true
	if err := projections.SetBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{CanModerateThreads: &canModerateThreads}); err != nil {
		t.Fatalf("grant bob thread moderation: %v", err)
	}
	noReply := true
	noReplyPayload := marshalCoreTestJSON(t, "marshal no-reply flag payload", proto.SetPostFlagPayload{
		Post:    rootPostPayload.ID,
		NoReply: &noReply,
	})
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "no-reply flag", partition, 0, 10, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-flags-no-reply",
		Command:    proto.CmdSetPostFlag,
		Payload:    noReplyPayload,
		EnqueuedAt: 4234,
	})
	if len(events) != 4 {
		t.Fatalf("events after no-reply = %+v, want second post.flags_set event", events)
	}
	noReplyEvent := requireNativeDecisionPayload[proto.PostFlagsSetPayload](t, events[3], "no-reply event payload")
	if noReplyEvent.ID != rootPostPayload.ID || !noReplyEvent.Marked || !noReplyEvent.Recommended ||
		!noReplyEvent.NoReply || !noReplyEvent.TeX || !noReplyEvent.MailBack || noReplyEvent.By != bob.ID || noReplyEvent.TS != 4234 {
		t.Fatalf("no-reply event = %+v, want merged moderated flags", noReplyEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-post-flags-test", partition, 10, "materialize no-reply event")
	post, err = c.GetPost(rootPostPayload.ID)
	if err != nil {
		t.Fatalf("get post after no-reply: %v", err)
	}
	if post == nil || !post.Marked || !post.Recommended || !post.NoReply || !post.TeX || !post.MailBack || post.UpdatedAt != 4234 {
		t.Fatalf("post after no-reply = %+v, want materialized merged flags", post)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsArticleMailBack(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Native mail-back",
		Body:  "root asks for mail-back",
	})
	_, boardEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-mailback-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mailback-test", boardPartition, 10, "materialize create events")
	if len(boardEvents) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", boardEvents)
	}
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, boardEvents[1], "root post payload")

	mailBack := true
	flagPayload := marshalCoreTestJSON(t, "marshal mail-back flag payload", proto.SetPostFlagPayload{
		Post:     rootPostPayload.ID,
		MailBack: &mailBack,
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "mail-back flag", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionPost, Key: rootPostPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-mailback-flag",
		Command:    proto.CmdSetPostFlag,
		Payload:    flagPayload,
		EnqueuedAt: 2234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mailback-test", boardPartition, 10, "materialize flag event")

	replyBody := "native mail-back reply"
	appendPayload := marshalCoreTestJSON(t, "marshal mail-back reply payload", proto.AppendPostPayload{
		Thread:  rootPostPayload.Thread,
		ReplyTo: rootPostPayload.ID,
		Body:    replyBody,
	})
	_, boardEvents = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "mail-back reply", boardPartition, 0, 10, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: rootPostPayload.Thread},
		ActorID:    bob.ID,
		CID:        "cid-native-mailback-reply",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 3234,
	})
	if len(boardEvents) != 4 || boardEvents[3].Kind != proto.EvtPostAppended {
		t.Fatalf("board events after mail-back reply = %+v, want reply post.appended", boardEvents)
	}
	replyPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, boardEvents[3], "reply post payload")
	userPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	mailEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, userPartition, 0, 10, "replay mail-back user events")
	requireNativeDecisionEventKinds(t, mailEvents, "mail-back user events", proto.EvtMailSent)
	mailPayload := requireNativeDecisionPayload[proto.MailSentPayload](t, mailEvents[0], "mail-back payload")
	if mailPayload.FromUserID != bob.ID || mailPayload.From != bob.Name || len(mailPayload.ToUserIDs) != 1 ||
		mailPayload.ToUserIDs[0] != alice.ID || mailPayload.SaveSent ||
		!strings.Contains(mailPayload.Subject, "Native mail-back") ||
		!strings.Contains(mailPayload.Body, replyBody) ||
		!strings.Contains(mailPayload.Body, rootPostPayload.ID) ||
		!strings.Contains(mailPayload.Body, replyPostPayload.ID) {
		t.Fatalf("mail-back payload = %+v, want automatic inbox-only article mail", mailPayload)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mailback-test", boardPartition, 10, "materialize reply post event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mailback-test", userPartition, 10, "materialize mail-back event")
	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice inbox: %v", err)
	}
	if len(aliceInbox) != 1 || aliceInbox[0].FromName != bob.Name ||
		!strings.Contains(aliceInbox[0].Subject, "Native mail-back") ||
		!strings.Contains(aliceInbox[0].Body, replyBody) {
		t.Fatalf("alice inbox after native mail-back = %+v, want one bob mail-back", aliceInbox)
	}
	bobSent, err := c.ListMail(bob.ID, "sent", 10, 0, false)
	if err != nil {
		t.Fatalf("list bob sent: %v", err)
	}
	if len(bobSent) != 0 {
		t.Fatalf("bob sent after native mail-back = %+v, want no sent copy", bobSent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsRelayDeliveries(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	bob := registerNativeDecisionTestUser(t, c, "bob")
	relayEnabled := true
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{RelayEnabled: &relayEnabled}); err != nil {
		t.Fatalf("enable relay board setting: %v", err)
	}

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Native relay topic",
		Body:  "first relay body",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "relay create", CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-relay-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-relay-test", boardPartition, 10, "materialize relay create events")
	boardEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, boardPartition, 0, 10, "replay relay create events")
	if len(boardEvents) != 2 {
		t.Fatalf("relay create events = %+v, want thread.new and post.appended", boardEvents)
	}
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, boardEvents[1], "root post payload")
	deliveries, err := c.ListRelayDeliveries("pending", 10, 0)
	if err != nil {
		t.Fatalf("list relay deliveries after create: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("relay deliveries after create = %+v, want one pending root delivery", deliveries)
	}
	if deliveries[0].ID != "relay_"+rootPostPayload.ID ||
		deliveries[0].BoardID != "general" ||
		deliveries[0].ThreadID != rootPostPayload.Thread ||
		deliveries[0].PostID != rootPostPayload.ID ||
		deliveries[0].AuthorID != bob.ID ||
		deliveries[0].AuthorName != bob.Name ||
		deliveries[0].Title != "Native relay topic" ||
		deliveries[0].Body != "first relay body" ||
		deliveries[0].Status != "pending" ||
		deliveries[0].CreatedAt != 1234 ||
		deliveries[0].UpdatedAt != 1234 {
		t.Fatalf("root relay delivery = %+v, want projected pending root delivery", deliveries[0])
	}

	appendPayload := marshalCoreTestJSON(t, "marshal append payload", proto.AppendPostPayload{
		Thread: rootPostPayload.Thread,
		Body:   "second relay body",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "relay reply", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: rootPostPayload.Thread},
		ActorID:    bob.ID,
		CID:        "cid-native-relay-reply",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-relay-test", boardPartition, 10, "materialize relay reply event")
	boardEvents = replayCommandLogWorkerPartition(t, ctx, eventStore, boardPartition, 0, 10, "replay relay events after reply")
	if len(boardEvents) != 3 {
		t.Fatalf("relay events after reply = %+v, want one reply post.appended", boardEvents)
	}
	replyPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, boardEvents[2], "reply post payload")
	deliveries, err = c.ListRelayDeliveries("pending", 10, 0)
	if err != nil {
		t.Fatalf("list relay deliveries after reply: %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("relay deliveries after reply = %+v, want root and reply deliveries", deliveries)
	}
	if deliveries[1].ID != "relay_"+replyPostPayload.ID ||
		deliveries[1].BoardID != "general" ||
		deliveries[1].ThreadID != rootPostPayload.Thread ||
		deliveries[1].PostID != replyPostPayload.ID ||
		deliveries[1].AuthorID != bob.ID ||
		deliveries[1].AuthorName != bob.Name ||
		deliveries[1].Title != "Native relay topic" ||
		deliveries[1].Body != "second relay body" ||
		deliveries[1].Status != "pending" ||
		deliveries[1].CreatedAt != 2234 ||
		deliveries[1].UpdatedAt != 2234 {
		t.Fatalf("reply relay delivery = %+v, want projected pending reply delivery", deliveries[1])
	}
}

func TestNativeCommandLogDecisionExecutorProjectsContentFilterReviews(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin filter setup: %v", err)
	}
	if err := projections.UpsertContentFilter(tx, "filter_native", "classified", "global", true, admin.ID, 1000); err != nil {
		tx.Rollback()
		t.Fatalf("upsert content filter: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit filter setup: %v", err)
	}

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	filterPartition := LogPartition{Kind: partitionBoard, Key: proto.ContentFilterSystemBoardID}
	createPayload := marshalCoreTestJSON(t, "marshal filtered create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Native content filter",
		Body:  "this classified body should enter review",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "filtered create", CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-content-filter-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	boardEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, boardPartition, 0, 10, "replay filtered create board events")
	// The content-filter flag event carries the reporter + reason, so it must
	// not appear on the board partition (reporter leak — M8); the board keeps
	// only the public thread/post events and the flag lands on the global
	// (moderation) partition.
	requireNativeDecisionEventKinds(t, boardEvents, "filtered create board events", proto.EvtThreadNew, proto.EvtPostAppended)
	for _, e := range boardEvents {
		if e.Kind == proto.EvtPostFlagged {
			t.Fatalf("post.flagged must not appear on the board partition (reporter leak): %+v", boardEvents)
		}
	}
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, boardEvents[1], "root post payload")
	globalPartition := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}
	globalEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, globalPartition, 0, 10, "replay global events")
	var filterPayload *proto.PostFlaggedPayload
	for _, e := range globalEvents {
		if e.Kind != proto.EvtPostFlagged {
			continue
		}
		p := requireNativeDecisionPayload[proto.PostFlaggedPayload](t, e, "filter payload")
		filterPayload = p
	}
	if filterPayload == nil {
		t.Fatalf("post.flagged not found on the moderation/global partition; global=%+v", globalEvents)
	}
	if filterPayload.Kind != "content_filter" || filterPayload.PostID != rootPostPayload.ID ||
		filterPayload.Thread != rootPostPayload.Thread || filterPayload.Reporter != bob.ID ||
		!strings.Contains(filterPayload.Reason, "filter_native") {
		t.Fatalf("content filter payload = %+v, want durable content-filter review event", filterPayload)
	}
	filterEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, filterPartition, 0, 10, "replay generated filter events")
	if len(filterEvents) != 3 ||
		filterEvents[0].Kind != proto.EvtBoardCreated ||
		filterEvents[1].Kind != proto.EvtThreadNew ||
		filterEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("generated filter events = %+v, want board/thread/post log events", filterEvents)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-content-filter-test", boardPartition, 10, "materialize filtered create board events")
	// post.flagged is scoped moderation-only → global partition (M8); materialize
	// it there so the content-filter review is built.
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-content-filter-test", globalPartition, 10, "materialize filtered flag event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-content-filter-test", filterPartition, 10, "materialize generated filter events")
	reviews, err := c.ListModerationReviews("open", 10, 0)
	if err != nil {
		t.Fatalf("list moderation reviews: %v", err)
	}
	if len(reviews) != 1 || reviews[0].ID != filterPayload.ReviewID ||
		reviews[0].Kind != "content_filter" || reviews[0].TargetID != rootPostPayload.ID ||
		reviews[0].Reporter != bob.ID || !strings.Contains(reviews[0].Reason, "filter_native") {
		t.Fatalf("reviews after native filtered create = %+v, want open content-filter review", reviews)
	}
	filterBoard, err := c.GetBoard(proto.ContentFilterSystemBoardID)
	if err != nil {
		t.Fatalf("get filter board: %v", err)
	}
	if filterBoard == nil || filterBoard.Name != "Filter" {
		t.Fatalf("filter board = %+v, want generated Filter board", filterBoard)
	}
	filterThreads, err := c.ListThreads(proto.ContentFilterSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list filter threads: %v", err)
	}
	if len(filterThreads) != 1 || filterThreads[0].ID != "filter_thr_"+filterPayload.ReviewID {
		t.Fatalf("filter threads after create = %+v, want generated review thread", filterThreads)
	}
	filterPosts, err := c.ListPosts(filterThreads[0].ID, 10, 0)
	if err != nil {
		t.Fatalf("list filter posts: %v", err)
	}
	if len(filterPosts) != 1 {
		t.Fatalf("filter posts after create = %+v, want generated review post", filterPosts)
	}
	for _, want := range []string{"Status: opened", "Filter: filter_native", "Board: general", "Public author: bob"} {
		if !strings.Contains(filterPosts[0].Body, want) {
			t.Fatalf("generated filter body missing %q:\n%s", want, filterPosts[0].Body)
		}
	}
	for _, secret := range []string{"classified", "this classified body"} {
		if strings.Contains(filterPosts[0].Body, secret) {
			t.Fatalf("generated filter body leaked %q:\n%s", secret, filterPosts[0].Body)
		}
	}

	appendPayload := marshalCoreTestJSON(t, "marshal filtered append payload", proto.AppendPostPayload{
		Thread: rootPostPayload.Thread,
		Body:   "another classified reply should also enter review",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "filtered append", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: rootPostPayload.Thread},
		ActorID:    bob.ID,
		CID:        "cid-native-content-filter-append",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-content-filter-test", boardPartition, 10, "materialize filtered append board events")
	// The append's post.flagged is also scoped moderation-only → global (M8).
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-content-filter-test", globalPartition, 10, "materialize filtered append flag event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-content-filter-test", filterPartition, 10, "materialize filtered append filter events")
	reviews, err = c.ListModerationReviews("open", 10, 0)
	if err != nil {
		t.Fatalf("list moderation reviews after append: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("reviews after filtered append = %+v, want two content-filter reviews", reviews)
	}
	filterThreads, err = c.ListThreads(proto.ContentFilterSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list filter threads after append: %v", err)
	}
	if len(filterThreads) != 2 {
		t.Fatalf("filter threads after append = %+v, want generated log per review", filterThreads)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsContentFilterSettings(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	filterPayload := marshalCoreTestJSON(t, "marshal scoped filter payload", proto.SetContentFilterPayload{
		ID:      "native_filter_policy",
		Pattern: "classified",
		Scope:   "general",
	})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-filter-denied",
		Command:    proto.CmdSetContentFilter,
		Payload:    filterPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "set content filter as non-admin")
	longPattern := strings.Repeat("x", proto.MaxContentFilterPatternLength+1)
	longPatternPayload := marshalCoreTestJSON(t, "marshal long pattern payload", proto.SetContentFilterPayload{
		ID:      "long_pattern_filter",
		Pattern: longPattern,
		Scope:   "general",
	})
	longPatternReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-filter-long-pattern",
		Command:    proto.CmdSetContentFilter,
		Payload:    longPatternPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, longPatternReply, proto.ErrValidationFailed, "set content filter long pattern", proto.ValidateContentFilterPattern(longPattern))
	missingScopePayload := marshalCoreTestJSON(t, "marshal missing scope payload", proto.SetContentFilterPayload{
		ID:      "missing_scope_filter",
		Pattern: "classified",
		Scope:   "missing",
	})
	missingScope := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-filter-missing-scope",
		Command:    proto.CmdSetContentFilter,
		Payload:    missingScopePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missingScope, proto.ErrNotFound, "set content filter for missing scope")

	_, boardEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "scoped filter", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-filter-set",
		Command:    proto.CmdSetContentFilter,
		Payload:    filterPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionEventKinds(t, boardEvents, "scoped filter events", proto.EvtContentFilterSet)
	setPayload := requireNativeDecisionPayload[proto.ContentFilterSetPayload](t, boardEvents[0], "scoped filter payload")
	if setPayload.ID != "native_filter_policy" || setPayload.Pattern != "classified" ||
		setPayload.Scope != "general" || !setPayload.Active || setPayload.By != admin.ID || setPayload.TS != 1234 {
		t.Fatalf("scoped filter payload = %+v, want deterministic content filter event", setPayload)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-content-filter-setting-test", boardPartition, 10, "materialize scoped filter event")
	filters, err := c.ListContentFilters("general", true, 10, 0)
	if err != nil {
		t.Fatalf("list scoped content filters: %v", err)
	}
	if len(filters) != 1 || filters[0].ID != "native_filter_policy" || !filters[0].Active ||
		filters[0].CreatedBy != admin.ID || filters[0].UpdatedAt != 1234 {
		t.Fatalf("scoped filters after materialization = %+v, want active native filter", filters)
	}

	inactive := false
	updatePayload := marshalCoreTestJSON(t, "marshal inactive filter payload", proto.SetContentFilterPayload{
		ID:      "native_filter_policy",
		Pattern: "classified",
		Scope:   "general",
		Active:  &inactive,
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "inactive filter", CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-filter-update",
		Command:    proto.CmdSetContentFilter,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-content-filter-setting-test", boardPartition, 10, "materialize inactive filter event")
	filters, err = c.ListContentFilters("general", true, 10, 0)
	if err != nil {
		t.Fatalf("list inactive scoped content filters: %v", err)
	}
	if len(filters) != 1 || filters[0].Active || filters[0].UpdatedAt != 2234 {
		t.Fatalf("scoped filters after inactive update = %+v, want inactive native filter", filters)
	}

	globalPayload := marshalCoreTestJSON(t, "marshal global filter payload", proto.SetContentFilterPayload{
		Pattern: "global secret",
	})
	globalCommandPartition := LogPartition{Kind: partitionBoard, Key: partitionGlobal}
	globalEventPartition := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}
	_, globalEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "global filter", globalEventPartition, 0, 10, CommandLogRecord{
		Partition:  globalCommandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-filter-global",
		Command:    proto.CmdSetContentFilter,
		Payload:    globalPayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionEventKinds(t, globalEvents, "global filter events", proto.EvtContentFilterSet)
	globalSetPayload := requireNativeDecisionPayload[proto.ContentFilterSetPayload](t, globalEvents[0], "global filter payload")
	if !strings.HasPrefix(globalSetPayload.ID, "filter_") || globalSetPayload.Pattern != "global secret" ||
		globalSetPayload.Scope != "global" || !globalSetPayload.Active || globalSetPayload.By != admin.ID {
		t.Fatalf("global filter payload = %+v, want generated global content filter", globalSetPayload)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-content-filter-setting-test", globalEventPartition, 10, "materialize global filter event")
	globalFilters, err := c.ListContentFilters("global", true, 10, 0)
	if err != nil {
		t.Fatalf("list global content filters: %v", err)
	}
	if len(globalFilters) != 1 || globalFilters[0].ID != globalSetPayload.ID || globalFilters[0].UpdatedAt != 3234 {
		t.Fatalf("global filters after materialization = %+v, want generated global filter", globalFilters)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsUserSanctions(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	accountPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	sanctionTS := nowMS()
	sanctionPayload := marshalCoreTestJSON(t, "marshal sanction payload", proto.SanctionUserPayload{
		User:        alice.ID,
		Kind:        "mute",
		Scope:       "general",
		DurationSec: 60,
		Reason:      " cooldown ",
	})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  accountPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-sanction-denied",
		Command:    proto.CmdSanctionUser,
		Payload:    sanctionPayload,
		EnqueuedAt: sanctionTS,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "sanction as non-moderator")
	adminTargetPayload := marshalCoreTestJSON(t, "marshal admin target sanction payload", proto.SanctionUserPayload{User: admin.ID, Kind: "mute", Scope: "global"})
	adminTarget := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: admin.ID},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-sanction-admin-target",
		Command:    proto.CmdSanctionUser,
		Payload:    adminTargetPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, adminTarget, proto.ErrForbidden, "sanction admin target")

	_, accountEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "sanction", 0, 10, CommandLogRecord{
		Partition:  accountPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-sanction-user",
		Command:    proto.CmdSanctionUser,
		Payload:    sanctionPayload,
		EnqueuedAt: sanctionTS,
	})
	requireNativeDecisionEventKinds(t, accountEvents, "account sanction events", proto.EvtUserSanctioned)
	sanctionEvent := requireNativeDecisionPayload[proto.UserSanctionedPayload](t, accountEvents[0], "sanction payload")
	if sanctionEvent.User != alice.ID || sanctionEvent.Kind != "mute" || sanctionEvent.Scope != "general" ||
		sanctionEvent.DurationSec != 60 || sanctionEvent.By != admin.ID || sanctionEvent.Reason != "cooldown" || sanctionEvent.TS != sanctionTS {
		t.Fatalf("sanction event = %+v, want deterministic board mute", sanctionEvent)
	}
	denypostPartition := LogPartition{Kind: partitionBoard, Key: proto.DenyPostSystemBoardID}
	denypostEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, denypostPartition, 0, 10, "replay denypost events")
	if len(denypostEvents) != 3 ||
		denypostEvents[0].Kind != proto.EvtBoardCreated ||
		denypostEvents[1].Kind != proto.EvtThreadNew ||
		denypostEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("denypost events = %+v, want board/thread/post generated record", denypostEvents)
	}
	denyThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, denypostEvents[1], "denypost thread payload")
	if !strings.Contains(denyThread.Title, "Board posting denied: alice on general") {
		t.Fatalf("denypost thread title = %q, want board posting denial", denyThread.Title)
	}
	denyPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, denypostEvents[2], "denypost post payload")
	for _, want := range []string{"# Board posting denied", "- Action: board posting denied", "- User: alice", "(general)", "- Kind: mute", "- Actor: admin", "- Reason: cooldown"} {
		if !strings.Contains(denyPost.Body, want) {
			t.Fatalf("denypost body missing %q:\n%s", want, denyPost.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-user-sanction-test", accountPartition, 10, "materialize sanction account event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-user-sanction-test", denypostPartition, 10, "materialize denypost events")
	if kind, ok := projections.ActiveSanction(c.DB, alice.ID, "general"); !ok || kind != "mute" {
		t.Fatalf("active general sanction = %q,%v; want mute,true", kind, ok)
	}
	denypostThreads, err := c.ListThreads(proto.DenyPostSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list denypost threads: %v", err)
	}
	if len(denypostThreads) != 1 {
		t.Fatalf("denypost threads after materialization = %+v, want one generated thread", denypostThreads)
	}

	secretTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin secret board setup: %v", err)
	}
	if err := projections.InsertBoard(secretTx, "secret", "Secret", "Private board", "", 1); err != nil {
		secretTx.Rollback() //nolint:errcheck
		t.Fatalf("insert secret board: %v", err)
	}
	if err := secretTx.Commit(); err != nil {
		t.Fatalf("commit secret board: %v", err)
	}
	memberRead := true
	if err := projections.SetBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read mode: %v", err)
	}
	secretPayload := marshalCoreTestJSON(t, "marshal secret sanction payload", proto.SanctionUserPayload{
		User:   alice.ID,
		Kind:   "ban",
		Scope:  "secret",
		Reason: "private board",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "secret sanction", CommandLogRecord{
		Partition:  accountPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-sanction-secret",
		Command:    proto.CmdSanctionUser,
		Payload:    secretPayload,
		EnqueuedAt: 2234,
	})
	denypostEvents = replayCommandLogWorkerPartition(t, ctx, eventStore, denypostPartition, 0, 10, "replay denypost events after private sanction")
	if len(denypostEvents) != 3 {
		t.Fatalf("denypost events after private sanction = %+v, want no private-board generated record", denypostEvents)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-user-sanction-test", accountPartition, 10, "materialize secret sanction account event")
	if kind, ok := projections.ActiveSanction(c.DB, alice.ID, "secret"); !ok || kind != "ban" {
		t.Fatalf("active secret sanction = %q,%v; want ban,true", kind, ok)
	}

	clearPayload := marshalCoreTestJSON(t, "marshal clear sanction payload", proto.ClearUserSanctionPayload{
		User:   alice.ID,
		Kind:   "mute",
		Scope:  "general",
		Reason: " served ",
	})
	clearDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  accountPartition,
		Offset:     3,
		ActorID:    bob.ID,
		CID:        "cid-native-clear-sanction-denied",
		Command:    proto.CmdClearUserSanction,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, clearDenied, proto.ErrForbidden, "clear sanction as non-moderator")
	clearRecord, accountEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "clear sanction", 0, 10, CommandLogRecord{
		Partition:  accountPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-clear-sanction",
		Command:    proto.CmdClearUserSanction,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	if len(accountEvents) != 3 || accountEvents[2].Kind != proto.EvtUserSanctionCleared {
		t.Fatalf("clear sanction account events = %+v, want third user.sanction_cleared", accountEvents)
	}
	clearEvent := requireNativeDecisionPayload[proto.UserSanctionClearedPayload](t, accountEvents[2], "clear sanction payload")
	if clearEvent.User != alice.ID || clearEvent.Kind != "mute" || clearEvent.Scope != "general" ||
		clearEvent.By != admin.ID || clearEvent.Reason != "served" || clearEvent.TS != 3234 {
		t.Fatalf("clear sanction event = %+v, want deterministic board mute clear", clearEvent)
	}
	undenypostPartition := LogPartition{Kind: partitionBoard, Key: proto.UndenyPostSystemBoardID}
	undenypostEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, undenypostPartition, 0, 10, "replay undenypost events")
	if len(undenypostEvents) != 3 ||
		undenypostEvents[0].Kind != proto.EvtBoardCreated ||
		undenypostEvents[1].Kind != proto.EvtThreadNew ||
		undenypostEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("undenypost events = %+v, want board/thread/post generated record", undenypostEvents)
	}
	undenyBoard := requireNativeDecisionPayload[proto.BoardCreatedPayload](t, undenypostEvents[0], "undenypost board payload")
	undenyThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, undenypostEvents[1], "undenypost thread payload")
	if !strings.Contains(undenyThread.Title, "Board posting restored: alice on general") {
		t.Fatalf("undenypost thread title = %q, want board posting restore", undenyThread.Title)
	}
	undenyPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, undenypostEvents[2], "undenypost post payload")
	for _, want := range []string{"# Board posting restored", "- Action: board posting restored", "- User: alice", "(general)", "- Kind: mute", "- Actor: admin", "- Reason: served"} {
		if !strings.Contains(undenyPost.Body, want) {
			t.Fatalf("undenypost body missing %q:\n%s", want, undenyPost.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-user-sanction-test", accountPartition, 10, "materialize clear sanction account event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-user-sanction-test", undenypostPartition, 10, "materialize undenypost events")
	if kind, ok := projections.ActiveSanction(c.DB, alice.ID, "general"); ok {
		t.Fatalf("active general sanction after clear = %q,%v; want none", kind, ok)
	}
	if kind, ok := projections.ActiveSanction(c.DB, alice.ID, "secret"); !ok || kind != "ban" {
		t.Fatalf("active secret sanction after clear = %q,%v; want ban,true", kind, ok)
	}
	undenypostThreads, err := c.ListThreads(proto.UndenyPostSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list undenypost threads: %v", err)
	}
	if len(undenypostThreads) != 1 {
		t.Fatalf("undenypost threads after materialization = %+v, want one generated thread", undenypostThreads)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, clearRecord)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != alice.ID {
		t.Fatalf("clear sanction retry reply = %+v, want idempotent success after projection", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, clearRecord, retryReply)
	if err != nil {
		t.Fatalf("decide clear sanction retry events: %v", err)
	}
	if len(retryEvents) != 4 {
		t.Fatalf("clear sanction retry events = %+v, want same clear plus audit events", retryEvents)
	}
	wantRetryKinds := []proto.EventKind{proto.EvtUserSanctionCleared, proto.EvtBoardCreated, proto.EvtThreadNew, proto.EvtPostAppended}
	for i, event := range retryEvents {
		if event.ID != stableCommandLogDecisionID("evt_", clearRecord, i) {
			t.Fatalf("retry event %d id = %q, want stable retry id", i, event.ID)
		}
		if event.Kind != wantRetryKinds[i] {
			t.Fatalf("retry event %d kind = %s, want %s", i, event.Kind, wantRetryKinds[i])
		}
	}
	retryBoard := requireNativeDecisionPayload[proto.BoardCreatedPayload](t, retryEvents[1], "clear sanction retry board payload")
	if retryBoard.ID != undenyBoard.ID || retryBoard.Position != undenyBoard.Position || retryBoard.Name != undenyBoard.Name {
		t.Fatalf("clear sanction retry board = %+v, want stable board upsert %+v", retryBoard, undenyBoard)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBlessUser(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	selfPayload := marshalCoreTestJSON(t, "marshal self blessing payload", proto.BlessUserPayload{User: "alice"})
	self := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "alice"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-bless-self",
		Command:    proto.CmdBlessUser,
		Payload:    selfPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, self, proto.ErrValidationFailed, "self blessing")
	longBlessingMessage := strings.Repeat("x", proto.MaxBlessingMessageLength+1)
	longMessagePayload := marshalCoreTestJSON(t, "marshal long blessing payload", proto.BlessUserPayload{
		User:    "bob",
		Message: longBlessingMessage,
	})
	longMessage := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "bob"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-bless-long-message",
		Command:    proto.CmdBlessUser,
		Payload:    longMessagePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, longMessage, proto.ErrValidationFailed, "long blessing message", validationMessage(longBlessingMessage, proto.NormalizeBlessingMessage))
	if err := projections.SetUserRelationship(c.DB, bob.ID, carol.ID, "ignore", "", true); err != nil {
		t.Fatalf("set bob ignores carol: %v", err)
	}
	ignoredPayload := marshalCoreTestJSON(t, "marshal ignored blessing payload", proto.BlessUserPayload{User: "bob"})
	ignored := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "bob"},
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-bless-ignored",
		Command:    proto.CmdBlessUser,
		Payload:    ignoredPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, ignored, proto.ErrForbidden, "ignored blessing")

	commandPartition := LogPartition{Kind: partitionUser, Key: "bob"}
	blessPayload := marshalCoreTestJSON(t, "marshal blessing payload", proto.BlessUserPayload{
		User:    "bob",
		Message: " Good luck on finals. ",
	})
	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	_, accountEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "blessing", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-bless-user",
		Command:    proto.CmdBlessUser,
		Payload:    blessPayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionEventKinds(t, accountEvents, "account blessing events", proto.EvtUserBlessed)
	blessingEvent := requireNativeDecisionPayload[proto.UserBlessedPayload](t, accountEvents[0], "blessing payload")
	if blessingEvent.ID == "" || blessingEvent.FromUserID != alice.ID || blessingEvent.From != "alice" ||
		blessingEvent.ToUserID != bob.ID || blessingEvent.To != "bob" || blessingEvent.Message != "Good luck on finals." || blessingEvent.TS != 3234 {
		t.Fatalf("blessing event = %+v, want deterministic alice->bob blessing", blessingEvent)
	}
	blessingPartition := LogPartition{Kind: partitionBoard, Key: proto.BlessingSystemBoardID}
	blessingBoardEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, blessingPartition, 0, 10, "replay Blessing board events")
	if len(blessingBoardEvents) != 3 ||
		blessingBoardEvents[0].Kind != proto.EvtBoardCreated ||
		blessingBoardEvents[1].Kind != proto.EvtThreadNew ||
		blessingBoardEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("Blessing board events = %+v, want board/thread/post generated record", blessingBoardEvents)
	}
	blessingThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, blessingBoardEvents[1], "blessing thread payload")
	if !strings.Contains(blessingThread.Title, "alice -> bob") {
		t.Fatalf("blessing thread title = %q, want alice -> bob", blessingThread.Title)
	}
	blessingPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, blessingBoardEvents[2], "blessing post payload")
	for _, want := range []string{"# Blessing for bob", "- From: alice", "- To: bob", "Good luck on finals.", "Generated public blessing record"} {
		if !strings.Contains(blessingPost.Body, want) {
			t.Fatalf("blessing post body missing %q:\n%s", want, blessingPost.Body)
		}
	}

	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-blessing-test", eventPartition, 10, "materialize blessing account event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-blessing-test", blessingPartition, 10, "materialize Blessing board events")
	blessings, err := c.ListBlessings(10, 0)
	if err != nil {
		t.Fatalf("list blessings: %v", err)
	}
	if len(blessings) != 1 || blessings[0].ID != blessingEvent.ID || blessings[0].FromName != "alice" || blessings[0].ToName != "bob" {
		t.Fatalf("materialized blessings = %+v, want alice blessing bob", blessings)
	}
	threads, err := c.ListThreads(proto.BlessingSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list Blessing threads: %v", err)
	}
	if len(threads) != 1 || !strings.Contains(threads[0].Title, "alice -> bob") {
		t.Fatalf("materialized Blessing threads = %+v, want generated blessing thread", threads)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsSendMail(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	if err := projections.SetUserRelationship(c.DB, alice.ID, bob.ID, "ignore", "", true); err != nil {
		t.Fatalf("set alice ignores bob: %v", err)
	}
	blockedPayload := marshalCoreTestJSON(t, "marshal blocked mail payload", proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Blocked",
		Body:    "please read",
	})
	blocked := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: bob.ID},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-send-mail-blocked",
		Command:    proto.CmdSendMail,
		Payload:    blockedPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, blocked, proto.ErrForbidden, "send mail blocked by ignore")
	if err := projections.SetUserRelationship(c.DB, alice.ID, bob.ID, "ignore", "", false); err != nil {
		t.Fatalf("clear alice ignores bob: %v", err)
	}
	if err := projections.SetUserRelationship(c.DB, bob.ID, alice.ID, "friend", "", true); err != nil {
		t.Fatalf("set bob friends alice: %v", err)
	}
	if err := projections.SetMailGroup(c.DB, bob.ID, "grp_lab", "lab", []string{alice.ID, carol.ID}); err != nil {
		t.Fatalf("set bob mail group: %v", err)
	}

	commandPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	payload := marshalCoreTestJSON(t, "marshal send mail payload", proto.SendMailPayload{
		ToGroups: []string{"lab", "friends"},
		Subject:  " Campus plans ",
		Body:     " Meet in the lab at six. ",
		Attachments: []proto.AttachmentPayload{{
			Filename:    " plan.txt ",
			ContentType: "text/plain",
			SizeBytes:   12,
			URL:         " https://example.edu/plan.txt ",
		}},
	})
	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "send mail", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-send-mail",
		Command:    proto.CmdSendMail,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "send mail events", proto.EvtMailSent)
	mailEvent := requireNativeDecisionPayload[proto.MailSentPayload](t, events[0], "mail event payload")
	if mailEvent.ID == "" || mailEvent.FromUserID != bob.ID || mailEvent.From != "bob" ||
		mailEvent.Subject != "Campus plans" || mailEvent.Body != "Meet in the lab at six." ||
		!mailEvent.SaveSent || mailEvent.TS != 2234 {
		t.Fatalf("mail event = %+v, want deterministic bob mail", mailEvent)
	}
	if len(mailEvent.ToUserIDs) != 2 || mailEvent.ToUserIDs[0] != alice.ID || mailEvent.ToUserIDs[1] != carol.ID ||
		len(mailEvent.To) != 2 || mailEvent.To[0] != "alice" || mailEvent.To[1] != "carol" {
		t.Fatalf("mail recipients = %+v/%+v, want deduplicated alice and carol", mailEvent.ToUserIDs, mailEvent.To)
	}
	if len(mailEvent.Attachments) != 1 || mailEvent.Attachments[0].ID == "" ||
		mailEvent.Attachments[0].Filename != "plan.txt" || mailEvent.Attachments[0].URL != "https://example.edu/plan.txt" {
		t.Fatalf("mail attachments = %+v, want trimmed deterministic attachment", mailEvent.Attachments)
	}

	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-send-mail-test", eventPartition, 10, "materialize send mail event")
	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice inbox: %v", err)
	}
	if len(aliceInbox) != 1 || aliceInbox[0].ID != mailEvent.ID || aliceInbox[0].FromName != "bob" || aliceInbox[0].Read {
		t.Fatalf("alice inbox = %+v, want unread bob mail", aliceInbox)
	}
	if len(aliceInbox[0].Attachments) != 1 || aliceInbox[0].Attachments[0].ID != mailEvent.Attachments[0].ID ||
		aliceInbox[0].Attachments[0].Filename != "plan.txt" || aliceInbox[0].Attachments[0].Stored {
		t.Fatalf("alice mail attachments = %+v, want metadata-only attachment", aliceInbox[0].Attachments)
	}
	carolInbox, err := c.ListMail(carol.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list carol inbox: %v", err)
	}
	if len(carolInbox) != 1 || carolInbox[0].ID != mailEvent.ID {
		t.Fatalf("carol inbox = %+v, want same native mail", carolInbox)
	}
	bobSent, err := c.ListMail(bob.ID, "sent", 10, 0, false)
	if err != nil {
		t.Fatalf("list bob sent: %v", err)
	}
	if len(bobSent) != 1 || bobSent[0].ID != mailEvent.ID || !bobSent[0].Read {
		t.Fatalf("bob sent = %+v, want read sent copy", bobSent)
	}

	replyPayload := marshalCoreTestJSON(t, "marshal reply mail payload", proto.SendMailPayload{
		To:      []string{"bob"},
		Subject: "Re: Campus plans",
		Body:    "See you there.",
		ReplyTo: mailEvent.ID,
	})
	replyPartition := LogPartition{Kind: partitionMail, Key: alice.ID}
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "reply mail", CommandLogRecord{
		Partition:  replyPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-send-mail-reply",
		Command:    proto.CmdSendMail,
		Payload:    replyPayload,
		EnqueuedAt: 3234,
	})
	replyEventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-send-mail-test", replyEventPartition, 10, "materialize reply mail event")
	bobInbox, err := c.ListMail(bob.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list bob inbox: %v", err)
	}
	if len(bobInbox) != 1 || bobInbox[0].FromName != "alice" || bobInbox[0].ParentID != mailEvent.ID {
		t.Fatalf("bob inbox = %+v, want alice reply to original native mail", bobInbox)
	}

	toAllPayload := marshalCoreTestJSON(t, "marshal mail-all payload", proto.SendMailPayload{
		ToAll:   true,
		Subject: "Campus bulletin",
		Body:    "Maintenance at midnight.",
	})
	nonAdminToAll := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: bob.ID},
		Offset:     2,
		ActorID:    bob.ID,
		CID:        "cid-native-send-mail-all-forbidden",
		Command:    proto.CmdSendMail,
		Payload:    toAllPayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, nonAdminToAll, proto.ErrForbidden, "non-admin mail-all")
	adminPartition := LogPartition{Kind: partitionMail, Key: admin.ID}
	adminEventPartition := LogPartition{Kind: partitionUser, Key: admin.ID}
	_, adminEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "mail-all", adminEventPartition, 0, 10, CommandLogRecord{
		Partition:  adminPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-send-mail-all",
		Command:    proto.CmdSendMail,
		Payload:    toAllPayload,
		EnqueuedAt: 4234,
	})
	requireNativeDecisionEventKinds(t, adminEvents, "admin mail events", proto.EvtMailSent)
	mailAllEvent := requireNativeDecisionPayload[proto.MailSentPayload](t, adminEvents[0], "mail-all payload")
	if len(mailAllEvent.ToUserIDs) != 3 || mailAllEvent.ToUserIDs[0] != alice.ID || mailAllEvent.ToUserIDs[1] != bob.ID || mailAllEvent.ToUserIDs[2] != carol.ID {
		t.Fatalf("mail-all recipients = %+v, want all non-admin users by name", mailAllEvent.ToUserIDs)
	}
	sysmailPartition := LogPartition{Kind: partitionBoard, Key: proto.SysmailSystemBoardID}
	sysmailEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, sysmailPartition, 0, 10, "replay sysmail events")
	requireNativeDecisionEventKinds(t, sysmailEvents, "sysmail events", proto.EvtBoardCreated, proto.EvtThreadNew, proto.EvtPostAppended)
	sysmailPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, sysmailEvents[2], "sysmail post payload")
	for _, want := range []string{"# Sysop mail: Campus bulletin", "- Recipients: 3 users", "Maintenance at midnight.", "Generated restricted sysop mail record"} {
		if !strings.Contains(sysmailPost.Body, want) {
			t.Fatalf("sysmail body missing %q:\n%s", want, sysmailPost.Body)
		}
	}
}

func TestNativeCommandLogDecisionExecutorRejectsOversizedSendMailFanout(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	sender := registerNativeDecisionTestUser(t, c, "sender")
	const oversizedFanout = 51
	recipients := make([]string, 0, oversizedFanout)
	for i := 0; i < oversizedFanout; i++ {
		user := registerNativeDecisionTestUser(t, c, fmt.Sprintf("fanout%02d", i))
		recipients = append(recipients, user.Name)
	}

	executor := NewCommandLogNativeDecisionExecutor(c)
	payload := marshalCoreTestJSON(t, "marshal oversized send mail payload", proto.SendMailPayload{
		To:      recipients,
		Subject: "Too many",
		Body:    "This should stay bounded.",
	})
	reply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: sender.ID},
		Offset:     1,
		ActorID:    sender.ID,
		CID:        "cid-native-send-mail-over-fanout",
		Command:    proto.CmdSendMail,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, reply, proto.ErrValidationFailed, "oversized send-mail fanout", "too many recipients in one message")
}

func TestNativeCommandLogDecisionExecutorProjectsAttachMail(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sourcePartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	sourcePayload := marshalCoreTestJSON(t, "marshal source mail payload", proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Campus plans",
		Body:    "Meet in the lab at six.",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "source mail", CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    bob.ID,
		CID:        "cid-native-attach-mail-source",
		Command:    proto.CmdSendMail,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	})
	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, eventPartition, 0, 10, "replay source mail events")
	requireNativeDecisionEventKinds(t, events, "source mail events", proto.EvtMailSent)
	sourceMail := requireNativeDecisionPayload[proto.MailSentPayload](t, events[0], "source mail payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-attach-mail-test", eventPartition, 10, "materialize source mail event")

	attachPartition := LogPartition{Kind: partitionMail, Key: sourceMail.ID}
	unauthorizedPayload := marshalCoreTestJSON(t, "marshal unauthorized attach payload", proto.AttachMailPayload{
		Mail:        sourceMail.ID,
		Filename:    "not-mine.txt",
		ContentType: "text/plain",
		SizeBytes:   1,
	})
	unauthorized := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  attachPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-attach-mail-forbidden",
		Command:    proto.CmdAttachMail,
		Payload:    unauthorizedPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, unauthorized, proto.ErrForbidden, "unauthorized attach mail")

	attachmentID := "matt_native_mail_blob"
	if err := c.StageMailAttachmentBlob(attachmentID, bob.ID, []byte("lab bytes"), "text/plain"); err != nil {
		t.Fatalf("stage mail attachment blob: %v", err)
	}
	attachPayload := marshalCoreTestJSON(t, "marshal attach mail payload", proto.AttachMailPayload{
		ID:           attachmentID,
		Mail:         sourceMail.ID,
		Filename:     " lab.txt ",
		ContentType:  "text/plain",
		SizeBytes:    int64(len("lab bytes")),
		StagedBlobID: attachmentID,
	})
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "attach mail", eventPartition, 0, 10, CommandLogRecord{
		Partition:  attachPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-attach-mail",
		Command:    proto.CmdAttachMail,
		Payload:    attachPayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtMailAttachmentAdded {
		t.Fatalf("attach mail events = %+v, want mail.sent then mail.attachment_added", events)
	}
	attachmentEvent := requireNativeDecisionPayload[proto.MailAttachmentAddedPayload](t, events[1], "attach mail payload")
	if attachmentEvent.ID != attachmentID || attachmentEvent.Mail != sourceMail.ID ||
		attachmentEvent.Filename != "lab.txt" || attachmentEvent.StagedBlobID != attachmentID ||
		attachmentEvent.AuthorID != bob.ID || attachmentEvent.TS != 3234 {
		t.Fatalf("attach mail event = %+v, want deterministic bob attachment", attachmentEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-attach-mail-test", eventPartition, 10, "materialize attach mail event")
	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice inbox: %v", err)
	}
	if len(aliceInbox) != 1 || len(aliceInbox[0].Attachments) != 1 ||
		aliceInbox[0].Attachments[0].ID != attachmentID || aliceInbox[0].Attachments[0].Filename != "lab.txt" ||
		!aliceInbox[0].Attachments[0].Stored {
		t.Fatalf("alice mail attachments = %+v, want promoted native attachment", aliceInbox)
	}
	data, contentType, err := c.GetMailAttachmentBlob(attachmentID)
	if err != nil {
		t.Fatalf("get mail attachment blob: %v", err)
	}
	if string(data) != "lab bytes" || contentType != "text/plain" {
		t.Fatalf("mail attachment blob = %q %q, want staged bytes", string(data), contentType)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsForwardMail(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sourcePartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	sourcePayload := marshalCoreTestJSON(t, "marshal source mail payload", proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Campus plans",
		Body:    "Meet in the lab at six.",
		Attachments: []proto.AttachmentPayload{{
			Filename:    "plan.txt",
			ContentType: "text/plain",
			SizeBytes:   12,
			URL:         "https://example.edu/plan.txt",
		}},
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "source mail", CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    bob.ID,
		CID:        "cid-native-forward-source",
		Command:    proto.CmdSendMail,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	})
	sourceEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	sourceEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, sourceEventPartition, 0, 10, "replay source mail event")
	requireNativeDecisionEventKinds(t, sourceEvents, "source mail events", proto.EvtMailSent)
	sourceMail := requireNativeDecisionPayload[proto.MailSentPayload](t, sourceEvents[0], "source mail payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-forward-mail-test", sourceEventPartition, 10, "materialize source mail event")

	forwardPayload := marshalCoreTestJSON(t, "marshal forward mail payload", proto.ForwardMailPayload{
		Mail: sourceMail.ID,
		To:   []string{"carol"},
		Note: "Please see this.",
	})
	forwardPartition := LogPartition{Kind: partitionMail, Key: sourceMail.ID}
	missingCopy := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  forwardPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-forward-missing-copy",
		Command:    proto.CmdForwardMail,
		Payload:    forwardPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, missingCopy, proto.ErrNotFound, "forward without mail copy")

	forwardEventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "forward mail", forwardEventPartition, 0, 10, CommandLogRecord{
		Partition:  forwardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-forward-mail",
		Command:    proto.CmdForwardMail,
		Payload:    forwardPayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionEventKinds(t, events, "forward mail events", proto.EvtMailSent)
	forwardEvent := requireNativeDecisionPayload[proto.MailSentPayload](t, events[0], "forward mail payload")
	if forwardEvent.ID == "" || forwardEvent.FromUserID != alice.ID || forwardEvent.From != "alice" ||
		len(forwardEvent.ToUserIDs) != 1 || forwardEvent.ToUserIDs[0] != carol.ID ||
		forwardEvent.Subject != "Fwd: Campus plans" || !forwardEvent.SaveSent || forwardEvent.TS != 3234 {
		t.Fatalf("forward mail event = %+v, want deterministic alice forward", forwardEvent)
	}
	for _, want := range []string{"Please see this.", "----- Forwarded mail -----", "From: bob", "To: alice", "Subject: Campus plans", "Attachments: plan.txt", "Meet in the lab at six."} {
		if !strings.Contains(forwardEvent.Body, want) {
			t.Fatalf("forward mail body missing %q:\n%s", want, forwardEvent.Body)
		}
	}

	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-forward-mail-test", forwardEventPartition, 10, "materialize forward mail event")
	carolInbox, err := c.ListMail(carol.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list carol inbox: %v", err)
	}
	if len(carolInbox) != 1 || carolInbox[0].ID != forwardEvent.ID || carolInbox[0].FromName != "alice" ||
		!strings.Contains(carolInbox[0].Body, "----- Forwarded mail -----") {
		t.Fatalf("carol inbox = %+v, want forwarded native mail", carolInbox)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsMailPostAuthor(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload := marshalCoreTestJSON(t, "marshal source thread payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Original article",
		Body:  "source body for excerpt",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "source thread", CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-mail-post-author-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	})
	sourceEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, sourcePartition, 0, 10, "replay source thread events")
	if len(sourceEvents) != 2 {
		t.Fatalf("source thread events = %+v, want thread and post", sourceEvents)
	}
	sourceThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, sourceEvents[0], "source thread payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mail-post-author-test", sourcePartition, 10, "materialize source thread events")
	sourcePosts, err := c.ListPosts(sourceThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}

	mailPayload := marshalCoreTestJSON(t, "marshal mail-post-author payload", proto.MailPostAuthorPayload{
		Post: sourcePosts[0].ID,
		Body: "Could you share the notes?",
	})
	mailPartition := LogPartition{Kind: partitionPost, Key: sourcePosts[0].ID}
	mailEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "mail-post-author", mailEventPartition, 0, 10, CommandLogRecord{
		Partition:  mailPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-post-author",
		Command:    proto.CmdMailPostAuthor,
		Payload:    mailPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "mail-post-author events", proto.EvtMailSent)
	mailEvent := requireNativeDecisionPayload[proto.MailSentPayload](t, events[0], "mail-post-author payload")
	if mailEvent.ID == "" || mailEvent.FromUserID != bob.ID || len(mailEvent.ToUserIDs) != 1 ||
		mailEvent.ToUserIDs[0] != alice.ID || mailEvent.Subject != "Re: Original article" || mailEvent.TS != 2234 {
		t.Fatalf("mail-post-author event = %+v, want bob mail to alice", mailEvent)
	}
	for _, want := range []string{"Could you share the notes?", "Sent from article reading context.", "Board: general", "Thread: Original article", "Article author: alice", "Mail author: bob", "source body for excerpt"} {
		if !strings.Contains(mailEvent.Body, want) {
			t.Fatalf("mail-post-author body missing %q:\n%s", want, mailEvent.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mail-post-author-test", mailEventPartition, 10, "materialize mail-post-author event")
	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice inbox: %v", err)
	}
	if len(aliceInbox) != 1 || aliceInbox[0].ID != mailEvent.ID || aliceInbox[0].FromName != "bob" {
		t.Fatalf("alice inbox = %+v, want native article-author mail", aliceInbox)
	}

	secretTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin secret board setup: %v", err)
	}
	if err := projections.InsertBoard(secretTx, "secret", "Secret", "Members only", "", 2); err != nil {
		secretTx.Rollback() //nolint:errcheck
		t.Fatalf("insert secret board: %v", err)
	}
	if err := secretTx.Commit(); err != nil {
		t.Fatalf("commit secret board setup: %v", err)
	}
	memberRead := true
	if err := projections.SetBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read mode: %v", err)
	}
	secretPayload := marshalCoreTestJSON(t, "marshal private source payload", proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private article",
		Body:  "hidden source",
	})
	secretPartition := LogPartition{Kind: partitionBoard, Key: "secret"}
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "private source", CommandLogRecord{
		Partition:  secretPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-mail-post-author-private-source",
		Command:    proto.CmdCreateThread,
		Payload:    secretPayload,
		EnqueuedAt: 3234,
	})
	secretEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, secretPartition, 0, 10, "replay private source events")
	if len(secretEvents) != 2 {
		t.Fatalf("private source events = %+v, want thread and post", secretEvents)
	}
	secretThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, secretEvents[0], "private source thread payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mail-post-author-test", secretPartition, 10, "materialize private source events")
	secretPosts, err := c.ListPosts(secretThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list private source posts: %v", err)
	}
	if len(secretPosts) != 1 {
		t.Fatalf("private source posts = %+v, want one", secretPosts)
	}
	privateMailPayload := marshalCoreTestJSON(t, "marshal private mail-post-author payload", proto.MailPostAuthorPayload{
		Post: secretPosts[0].ID,
		Body: "Can I see this?",
	})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionPost, Key: secretPosts[0].ID},
		Offset:     2,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-post-author-private",
		Command:    proto.CmdMailPostAuthor,
		Payload:    privateMailPayload,
		EnqueuedAt: 4234,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "private board mail-post-author denied")
}

func TestNativeCommandLogDecisionExecutorProjectsCreateDigestDirectory(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	payload := marshalCoreTestJSON(t, "marshal digest directory payload", proto.CreateDigestDirectoryPayload{
		Board: "general",
		Kind:  "archive",
		Path:  " faq/empty/ ",
	})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-directory-forbidden",
		Command:    proto.CmdCreateDigestDirectory,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "create digest directory as non-curator", "board curator permission required")
	record, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "digest directory", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-directory",
		Command:    proto.CmdCreateDigestDirectory,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "digest directory events", proto.EvtDigestDirectorySet)
	directoryEvent := requireNativeDecisionPayload[proto.DigestDirectorySetPayload](t, events[0], "digest directory payload")
	if directoryEvent.ID != stableCommandLogDecisionID("dir_", record, 0) || directoryEvent.Board != "general" ||
		directoryEvent.Kind != "archive" || directoryEvent.Path != "faq/empty" ||
		directoryEvent.CreatedBy != admin.ID || directoryEvent.TS != 2234 {
		t.Fatalf("digest directory event = %+v, want deterministic archive directory", directoryEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-directory-test", partition, 10, "materialize digest directory event")
	tree, err := c.ListDigestPathTree("general", "archive")
	if err != nil {
		t.Fatalf("list digest path tree: %v", err)
	}
	nodes := map[string]projections.DigestPathNode{}
	for _, node := range tree {
		nodes[node.Path] = node
	}
	if !nodes["faq/empty"].Explicit || nodes["faq/empty"].EntryCount != 0 || nodes["faq/empty"].ParentPath != "faq" {
		t.Fatalf("digest archive tree = %+v, want explicit faq/empty directory", tree)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, record)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != directoryEvent.ID {
		t.Fatalf("digest directory retry reply = %+v, want idempotent existing directory id", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, record, retryReply)
	if err != nil {
		t.Fatalf("decide digest directory retry events: %v", err)
	}
	if len(retryEvents) != 1 || retryEvents[0].ID != stableCommandLogDecisionID("evt_", record, 0) ||
		retryEvents[0].Kind != proto.EvtDigestDirectorySet {
		t.Fatalf("digest directory retry events = %+v, want stable directory event", retryEvents)
	}
	retryDirectory := requireNativeDecisionPayload[proto.DigestDirectorySetPayload](t, retryEvents[0], "digest directory retry payload")
	if retryDirectory.ID != directoryEvent.ID || retryDirectory.Board != directoryEvent.Board ||
		retryDirectory.Kind != directoryEvent.Kind || retryDirectory.Path != directoryEvent.Path {
		t.Fatalf("digest directory retry payload = %+v, want stable payload %+v", retryDirectory, directoryEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsCopyDigestPath(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	if _, err := projections.UpsertDigestEntry(c.DB, "dig_copy_source_post", "general", "post", "post_copy_source", "archive", "Copy source post", "faq/howto", "Source note", admin.ID); err != nil {
		t.Fatalf("upsert source digest entry: %v", err)
	}
	if _, err := projections.UpsertDigestEntry(c.DB, "dig_copy_source_thread", "general", "thread", "thread_copy_source", "archive", "Copy source thread", "faq/howto/deep", "Thread note", admin.ID); err != nil {
		t.Fatalf("upsert nested source digest entry: %v", err)
	}
	if _, err := projections.UpsertDigestDirectory(c.DB, "dir_copy_empty", "general", "archive", "faq/empty", admin.ID); err != nil {
		t.Fatalf("upsert source digest directory: %v", err)
	}
	if _, err := projections.UpsertDigestEntry(c.DB, "dig_copy_conflict", "general", "post", "post_copy_source", "archive", "Conflicting copy target", "conflict/howto", "", admin.ID); err != nil {
		t.Fatalf("upsert conflicting digest entry: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	payload := marshalCoreTestJSON(t, "marshal copy digest path payload", proto.CopyDigestPathPayload{
		Board:    "general",
		FromPath: " faq/ ",
		ToPath:   " faq-copy/ ",
	})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-copy-forbidden",
		Command:    proto.CmdCopyDigestPath,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "copy digest path as non-curator", "board curator permission required")
	conflictPayload := marshalCoreTestJSON(t, "marshal conflicting copy digest path payload", proto.CopyDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "conflict",
	})
	conflict := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-copy-conflict",
		Command:    proto.CmdCopyDigestPath,
		Payload:    conflictPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, conflict, proto.ErrConflict, "conflicting digest path copy", "digest path copy would overwrite an existing entry")

	record, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "copy digest path", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-copy",
		Command:    proto.CmdCopyDigestPath,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "copy digest path events", proto.EvtDigestPathCopied)
	copyEvent := requireNativeDecisionPayload[proto.DigestPathCopiedPayload](t, events[0], "copy digest path payload")
	wantEntryIDs := []string{stableCommandLogDecisionID("dig_", record, 0), stableCommandLogDecisionID("dig_", record, 1)}
	wantDirectoryIDs := []string{stableCommandLogDecisionID("dir_", record, 0)}
	if copyEvent.Board != "general" || copyEvent.Kind != "archive" ||
		copyEvent.FromPath != "faq" || copyEvent.ToPath != "faq-copy" ||
		copyEvent.Count != 3 || copyEvent.CreatedBy != admin.ID || copyEvent.TS != 2234 ||
		!slices.Equal(copyEvent.EntryIDs, wantEntryIDs) ||
		!slices.Equal(copyEvent.DirectoryIDs, wantDirectoryIDs) {
		t.Fatalf("copy digest path event = %+v, want deterministic archive subtree copy", copyEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-copy-test", partition, 10, "materialize copy digest path event")
	var copiedID, copiedTarget, copiedTitle, copiedBy string
	if err := qQueryRow(c.DB,
		`SELECT id, target_id, title, created_by
		   FROM digest_entries
		  WHERE board_id=? AND kind=? AND path=?`,
		"general", "archive", "faq-copy/howto",
	).Scan(&copiedID, &copiedTarget, &copiedTitle, &copiedBy); err != nil {
		t.Fatalf("read copied digest entry: %v", err)
	}
	if copiedID != wantEntryIDs[0] || copiedTarget != "post_copy_source" ||
		copiedTitle != "Copy source post" || copiedBy != admin.ID {
		t.Fatalf("copied digest entry = id=%q target=%q title=%q by=%q, want copied source post entry", copiedID, copiedTarget, copiedTitle, copiedBy)
	}
	var nestedCopiedID, nestedCopiedTarget string
	if err := qQueryRow(c.DB,
		`SELECT id, target_id
		   FROM digest_entries
		  WHERE board_id=? AND kind=? AND path=?`,
		"general", "archive", "faq-copy/howto/deep",
	).Scan(&nestedCopiedID, &nestedCopiedTarget); err != nil {
		t.Fatalf("read nested copied digest entry: %v", err)
	}
	if nestedCopiedID != wantEntryIDs[1] || nestedCopiedTarget != "thread_copy_source" {
		t.Fatalf("nested copied digest entry = id=%q target=%q, want copied source thread entry", nestedCopiedID, nestedCopiedTarget)
	}
	tree, err := c.ListDigestPathTree("general", "archive")
	if err != nil {
		t.Fatalf("list digest path tree after copy: %v", err)
	}
	nodes := map[string]projections.DigestPathNode{}
	for _, node := range tree {
		nodes[node.Path] = node
	}
	if !nodes["faq-copy/empty"].Explicit || nodes["faq-copy/empty"].EntryCount != 0 {
		t.Fatalf("digest archive tree after copy = %+v, want copied explicit empty directory", tree)
	}
	retryReply := executor.ExecuteCommandLogRecord(ctx, record)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "general:archive:3" {
		t.Fatalf("copy digest path retry reply = %+v, want idempotent count result", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, record, retryReply)
	if err != nil {
		t.Fatalf("decide copy digest path retry events: %v", err)
	}
	if len(retryEvents) != 1 || retryEvents[0].ID != stableCommandLogDecisionID("evt_", record, 0) ||
		retryEvents[0].Kind != proto.EvtDigestPathCopied {
		t.Fatalf("copy digest path retry events = %+v, want stable copied event", retryEvents)
	}
	retryCopy := requireNativeDecisionPayload[proto.DigestPathCopiedPayload](t, retryEvents[0], "copy digest path retry payload")
	if retryCopy.Count != copyEvent.Count || !slices.Equal(retryCopy.EntryIDs, copyEvent.EntryIDs) ||
		!slices.Equal(retryCopy.DirectoryIDs, copyEvent.DirectoryIDs) {
		t.Fatalf("copy digest path retry payload = %+v, want stable payload %+v", retryCopy, copyEvent)
	}

	descendantPayload := marshalCoreTestJSON(t, "marshal descendant copy digest path payload", proto.CopyDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "faq/nested-copy",
	})
	descendantRecord := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "descendant copy digest path", CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-copy-descendant",
		Command:    proto.CmdCopyDigestPath,
		Payload:    descendantPayload,
		EnqueuedAt: 3234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-copy-test", partition, 10, "materialize descendant copy digest path event")
	descendantRetryReply := executor.ExecuteCommandLogRecord(ctx, descendantRecord)
	if descendantRetryReply.Err != nil || descendantRetryReply.Result == nil || descendantRetryReply.Result.ID != "general:archive:3" {
		t.Fatalf("descendant copy retry reply = %+v, want original source count despite copied descendants", descendantRetryReply)
	}
	descendantRetryEvents, err := executor.DecideCommandLogEvents(ctx, descendantRecord, descendantRetryReply)
	if err != nil {
		t.Fatalf("decide descendant copy retry events: %v", err)
	}
	requireNativeDecisionEventKinds(t, descendantRetryEvents, "descendant copy retry events", proto.EvtDigestPathCopied)
	descendantRetryCopy := requireNativeDecisionPayload[proto.DigestPathCopiedPayload](t, descendantRetryEvents[0], "descendant copy retry payload")
	if descendantRetryCopy.Count != 3 ||
		!slices.Equal(descendantRetryCopy.EntryIDs, []string{stableCommandLogDecisionID("dig_", descendantRecord, 0), stableCommandLogDecisionID("dig_", descendantRecord, 1)}) ||
		!slices.Equal(descendantRetryCopy.DirectoryIDs, []string{stableCommandLogDecisionID("dir_", descendantRecord, 0)}) {
		t.Fatalf("descendant copy retry payload = %+v, want stable original source count", descendantRetryCopy)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsMoveAndDeleteDigestPath(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	if _, err := projections.UpsertDigestEntry(c.DB, "dig_move_source_post", "general", "post", "post_move_source", "archive", "Move source post", "faq/howto", "Source note", admin.ID); err != nil {
		t.Fatalf("upsert source digest entry: %v", err)
	}
	if _, err := projections.UpsertDigestEntry(c.DB, "dig_move_source_thread", "general", "thread", "thread_move_source", "archive", "Move source thread", "faq/howto/deep", "Thread note", admin.ID); err != nil {
		t.Fatalf("upsert nested source digest entry: %v", err)
	}
	if _, err := projections.UpsertDigestDirectory(c.DB, "dir_move_empty", "general", "archive", "faq/empty", admin.ID); err != nil {
		t.Fatalf("upsert source digest directory: %v", err)
	}
	if _, err := projections.UpsertDigestEntry(c.DB, "dig_move_conflict", "general", "post", "post_move_source", "archive", "Conflicting move target", "conflict/howto", "", admin.ID); err != nil {
		t.Fatalf("upsert conflicting digest entry: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	movePayload := marshalCoreTestJSON(t, "marshal move digest path payload", proto.MoveDigestPathPayload{
		Board:    " general ",
		Kind:     "archive",
		FromPath: " faq/ ",
		ToPath:   " faq-moved/ ",
	})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-move-forbidden",
		Command:    proto.CmdMoveDigestPath,
		Payload:    movePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "move digest path as non-curator", "board curator permission required")
	invalidPayload := marshalCoreTestJSON(t, "marshal invalid move digest path payload", proto.MoveDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "faq/nested",
	})
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-move-invalid",
		Command:    proto.CmdMoveDigestPath,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, invalid, proto.ErrValidationFailed, "invalid digest path move", "cannot move an archive path into itself")
	conflictPayload := marshalCoreTestJSON(t, "marshal conflicting move digest path payload", proto.MoveDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "conflict",
	})
	conflict := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-move-conflict",
		Command:    proto.CmdMoveDigestPath,
		Payload:    conflictPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, conflict, proto.ErrConflict, "conflicting digest path move", "digest path move would overwrite an existing entry")

	moveRecord, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "move digest path", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-move",
		Command:    proto.CmdMoveDigestPath,
		Payload:    movePayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "move digest path events", proto.EvtDigestPathMoved)
	moveEvent := requireNativeDecisionPayload[proto.DigestPathMovedPayload](t, events[0], "move digest path payload")
	if moveEvent.Board != "general" || moveEvent.Kind != "archive" ||
		moveEvent.FromPath != "faq" || moveEvent.ToPath != "faq-moved" ||
		moveEvent.Count != 3 || moveEvent.By != admin.ID || moveEvent.TS != 2234 {
		t.Fatalf("move digest path event = %+v, want deterministic archive subtree move", moveEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-move-delete-test", partition, 10, "materialize move digest path event")
	movedEntries, err := c.ListDigestEntries("general", "archive", "faq-moved/howto", 10, 0)
	if err != nil {
		t.Fatalf("list moved digest entries: %v", err)
	}
	if len(movedEntries) != 1 || movedEntries[0].ID != "dig_move_source_post" {
		t.Fatalf("moved digest entries = %+v, want source post under moved path", movedEntries)
	}
	oldEntries, err := c.ListDigestEntries("general", "archive", "faq/howto", 10, 0)
	if err != nil {
		t.Fatalf("list old digest entries: %v", err)
	}
	if len(oldEntries) != 0 {
		t.Fatalf("old digest entries = %+v, want none after move", oldEntries)
	}
	var moveCount int
	if err := qQueryRow(c.DB, `SELECT count FROM digest_path_mutations WHERE event_id=? AND action=?`, stableCommandLogDecisionID("evt_", moveRecord, 0), "move").Scan(&moveCount); err != nil {
		t.Fatalf("lookup move digest path mutation: %v", err)
	}
	if moveCount != 3 {
		t.Fatalf("move digest path mutation count = %d, want 3", moveCount)
	}
	retryMoveReply := executor.ExecuteCommandLogRecord(ctx, moveRecord)
	if retryMoveReply.Err != nil || retryMoveReply.Result == nil || retryMoveReply.Result.ID != "general:archive:3" {
		t.Fatalf("move digest path retry reply = %+v, want tombstone-backed count result", retryMoveReply)
	}
	retryMoveEvents, err := executor.DecideCommandLogEvents(ctx, moveRecord, retryMoveReply)
	if err != nil {
		t.Fatalf("decide move digest path retry events: %v", err)
	}
	if len(retryMoveEvents) != 1 || retryMoveEvents[0].ID != stableCommandLogDecisionID("evt_", moveRecord, 0) ||
		retryMoveEvents[0].Kind != proto.EvtDigestPathMoved {
		t.Fatalf("move digest path retry events = %+v, want stable moved event", retryMoveEvents)
	}
	retryMove := requireNativeDecisionPayload[proto.DigestPathMovedPayload](t, retryMoveEvents[0], "move digest path retry payload")
	if retryMove.Count != moveEvent.Count || retryMove.FromPath != moveEvent.FromPath ||
		retryMove.ToPath != moveEvent.ToPath || retryMove.By != moveEvent.By || retryMove.TS != moveEvent.TS {
		t.Fatalf("move digest path retry payload = %+v, want stable payload %+v", retryMove, moveEvent)
	}

	deletePayload := marshalCoreTestJSON(t, "marshal delete digest path payload", proto.DeleteDigestPathPayload{
		Board: "general",
		Kind:  "archive",
		Path:  "faq-moved",
	})
	deleteRecord, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "delete digest path", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-delete-path",
		Command:    proto.CmdDeleteDigestPath,
		Payload:    deletePayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtDigestPathDeleted {
		t.Fatalf("delete digest path events = %+v, want path moved then deleted", events)
	}
	deleteEvent := requireNativeDecisionPayload[proto.DigestPathDeletedPayload](t, events[1], "delete digest path payload")
	if deleteEvent.Board != "general" || deleteEvent.Kind != "archive" ||
		deleteEvent.Path != "faq-moved" || deleteEvent.Count != 3 ||
		deleteEvent.By != admin.ID || deleteEvent.TS != 3234 {
		t.Fatalf("delete digest path event = %+v, want deterministic archive subtree deletion", deleteEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-move-delete-test", partition, 10, "materialize delete digest path event")
	deletedEntries, err := c.ListDigestEntries("general", "archive", "faq-moved/howto", 10, 0)
	if err != nil {
		t.Fatalf("list deleted digest entries: %v", err)
	}
	if len(deletedEntries) != 0 {
		t.Fatalf("deleted digest entries = %+v, want none", deletedEntries)
	}
	tree, err := c.ListDigestPathTree("general", "archive")
	if err != nil {
		t.Fatalf("list digest path tree after delete: %v", err)
	}
	for _, node := range tree {
		if strings.HasPrefix(node.Path, "faq-moved") {
			t.Fatalf("digest archive tree after delete = %+v, want moved subtree gone", tree)
		}
	}
	var deleteCount int
	if err := qQueryRow(c.DB, `SELECT count FROM digest_path_mutations WHERE event_id=? AND action=?`, stableCommandLogDecisionID("evt_", deleteRecord, 0), "delete").Scan(&deleteCount); err != nil {
		t.Fatalf("lookup delete digest path mutation: %v", err)
	}
	if deleteCount != 3 {
		t.Fatalf("delete digest path mutation count = %d, want 3", deleteCount)
	}
	retryDeleteReply := executor.ExecuteCommandLogRecord(ctx, deleteRecord)
	if retryDeleteReply.Err != nil || retryDeleteReply.Result == nil || retryDeleteReply.Result.ID != "general:archive:3" {
		t.Fatalf("delete digest path retry reply = %+v, want tombstone-backed count result", retryDeleteReply)
	}
	retryDeleteEvents, err := executor.DecideCommandLogEvents(ctx, deleteRecord, retryDeleteReply)
	if err != nil {
		t.Fatalf("decide delete digest path retry events: %v", err)
	}
	if len(retryDeleteEvents) != 1 || retryDeleteEvents[0].ID != stableCommandLogDecisionID("evt_", deleteRecord, 0) ||
		retryDeleteEvents[0].Kind != proto.EvtDigestPathDeleted {
		t.Fatalf("delete digest path retry events = %+v, want stable deleted event", retryDeleteEvents)
	}
	retryDelete := requireNativeDecisionPayload[proto.DigestPathDeletedPayload](t, retryDeleteEvents[0], "delete digest path retry payload")
	if retryDelete.Count != deleteEvent.Count || retryDelete.Path != deleteEvent.Path ||
		retryDelete.By != deleteEvent.By || retryDelete.TS != deleteEvent.TS {
		t.Fatalf("delete digest path retry payload = %+v, want stable payload %+v", retryDelete, deleteEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDigestCuration(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload := marshalCoreTestJSON(t, "marshal source thread payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Curatable topic",
		Body:  "First public body.",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "source thread", CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-curation-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	})
	sourceEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, sourcePartition, 0, 10, "replay source events")
	if len(sourceEvents) != 2 {
		t.Fatalf("source events = %+v, want thread and post", sourceEvents)
	}
	sourceThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, sourceEvents[0], "source thread payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-curation-test", sourcePartition, 10, "materialize source events")
	sourcePosts, err := c.ListPosts(sourceThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}

	postPayload := marshalCoreTestJSON(t, "marshal curate post payload", proto.CuratePostPayload{
		Post: sourcePosts[0].ID,
		Kind: "DiGeSt",
		Path: "/faq/",
		Note: "Useful local note.",
	})
	postPartition := LogPartition{Kind: partitionPost, Key: sourcePosts[0].ID}
	invalidKindPayload := marshalCoreTestJSON(t, "marshal invalid digest kind payload", proto.CuratePostPayload{
		Post: sourcePosts[0].ID,
		Kind: "unknown",
	})
	invalidKind := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-curation-invalid-kind",
		Command:    proto.CmdCuratePost,
		Payload:    invalidKindPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, invalidKind, proto.ErrValidationFailed, "invalid digest kind", validationMessage("unknown", proto.NormalizeDigestKind))
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-curation-forbidden",
		Command:    proto.CmdCuratePost,
		Payload:    postPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "curate post as non-curator", proto.DigestCurationPermissionMessage("digest"))
	postRecord := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "curate post", CommandLogRecord{
		Partition:  postPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-curation-post",
		Command:    proto.CmdCuratePost,
		Payload:    postPayload,
		EnqueuedAt: 2234,
	})
	generalEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, sourcePartition, 0, 10, "replay general events after curate post")
	if len(generalEvents) != 3 || generalEvents[2].Kind != proto.EvtDigestEntryUpserted {
		t.Fatalf("general events after curate post = %+v, want digest entry event", generalEvents)
	}
	postDigest := requireNativeDecisionPayload[proto.DigestEntryUpsertedPayload](t, generalEvents[2], "post digest payload")
	wantPostTitle := fmt.Sprintf("Curatable topic #%d", sourcePosts[0].CreatedSeq)
	if postDigest.ID != stableCommandLogDecisionID("dig_", postRecord, 0) || postDigest.Board != "general" ||
		postDigest.TargetKind != "post" || postDigest.TargetID != sourcePosts[0].ID ||
		postDigest.Kind != "digest" || postDigest.Title != wantPostTitle ||
		postDigest.Path != "faq" || postDigest.Note != "Useful local note." ||
		postDigest.CreatedBy != admin.ID || postDigest.TS != 2234 {
		t.Fatalf("post digest event = %+v, want deterministic post digest", postDigest)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-curation-test", sourcePartition, 10, "materialize post digest event")
	digestEntries, err := c.ListDigestEntries("general", "digest", "faq", 10, 0)
	if err != nil {
		t.Fatalf("list digest entries: %v", err)
	}
	if len(digestEntries) != 1 || digestEntries[0].ID != postDigest.ID || digestEntries[0].Title != wantPostTitle {
		t.Fatalf("digest entries = %+v, want projected post digest", digestEntries)
	}
	retryPostReply := executor.ExecuteCommandLogRecord(ctx, postRecord)
	if retryPostReply.Err != nil || retryPostReply.Result == nil || retryPostReply.Result.ID != postDigest.ID {
		t.Fatalf("curate post retry reply = %+v, want projected entry id", retryPostReply)
	}
	retryPostEvents, err := executor.DecideCommandLogEvents(ctx, postRecord, retryPostReply)
	if err != nil {
		t.Fatalf("decide curate post retry events: %v", err)
	}
	if len(retryPostEvents) != 1 || retryPostEvents[0].ID != stableCommandLogDecisionID("evt_", postRecord, 0) ||
		retryPostEvents[0].Kind != proto.EvtDigestEntryUpserted {
		t.Fatalf("curate post retry events = %+v, want stable digest event", retryPostEvents)
	}
	retryPostDigest := requireNativeDecisionPayload[proto.DigestEntryUpsertedPayload](t, retryPostEvents[0], "curate post retry payload")
	if retryPostDigest.ID != postDigest.ID || retryPostDigest.Kind != "digest" || retryPostDigest.Path != "faq" {
		t.Fatalf("curate post retry payload = %+v, want stable digest payload %+v", retryPostDigest, postDigest)
	}

	recommendPayload := marshalCoreTestJSON(t, "marshal recommended thread payload", proto.CurateThreadPayload{
		Thread: sourceThread.ID,
		Kind:   "recommended",
		Title:  "Front page pick",
		Path:   "frontpage",
		Note:   "Worth reading.",
	})
	threadPartition := LogPartition{Kind: partitionThread, Key: sourceThread.ID}
	recommendRecord := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "recommended thread", CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-curation-recommend",
		Command:    proto.CmdCurateThread,
		Payload:    recommendPayload,
		EnqueuedAt: 3234,
	})
	generalEvents = replayCommandLogWorkerPartition(t, ctx, eventStore, sourcePartition, 0, 10, "replay general events after recommended curation")
	if len(generalEvents) != 4 || generalEvents[3].Kind != proto.EvtDigestEntryUpserted {
		t.Fatalf("general events after recommended curation = %+v, want digest entry event", generalEvents)
	}
	recommendedDigest := requireNativeDecisionPayload[proto.DigestEntryUpsertedPayload](t, generalEvents[3], "recommended digest payload")
	if recommendedDigest.ID != stableCommandLogDecisionID("dig_", recommendRecord, 0) ||
		recommendedDigest.TargetKind != "thread" || recommendedDigest.TargetID != sourceThread.ID ||
		recommendedDigest.Kind != "recommended" || recommendedDigest.Title != "Front page pick" ||
		recommendedDigest.Path != "frontpage" || recommendedDigest.Note != "Worth reading." {
		t.Fatalf("recommended digest event = %+v, want deterministic thread recommendation", recommendedDigest)
	}
	recommendPartition := LogPartition{Kind: partitionBoard, Key: projections.DigestMirrorRecommendedBoardID}
	recommendEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, recommendPartition, 0, 10, "replay recommend mirror events")
	requireNativeDecisionEventKinds(t, recommendEvents, "recommend mirror events", proto.EvtBoardCreated, proto.EvtThreadNew, proto.EvtPostAppended)
	recommendThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, recommendEvents[1], "recommend mirror thread payload")
	if recommendThread.ID != "recommend_thr_"+recommendedDigest.ID || recommendThread.Board != projections.DigestMirrorRecommendedBoardID ||
		recommendThread.Title != "Front page pick" || recommendThread.AuthorID != admin.ID || recommendThread.TS != 3234 {
		t.Fatalf("recommend mirror thread = %+v, want deterministic mirror thread", recommendThread)
	}
	recommendPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, recommendEvents[2], "recommend mirror post payload")
	for _, want := range []string{"Front page pick", "Board: General (general)", "Kind: recommended", "Path: frontpage", "Note: Worth reading.", "From: alice", "First public body."} {
		if !strings.Contains(recommendPost.Body, want) {
			t.Fatalf("recommend mirror body missing %q:\n%s", want, recommendPost.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-curation-test", sourcePartition, 10, "materialize recommended digest event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-curation-test", recommendPartition, 10, "materialize recommend mirror events")
	recommendThreads, err := c.ListThreads(projections.DigestMirrorRecommendedBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list recommend threads: %v", err)
	}
	if len(recommendThreads) != 1 || recommendThreads[0].ID != "recommend_thr_"+recommendedDigest.ID {
		t.Fatalf("recommend threads = %+v, want generated mirror thread", recommendThreads)
	}
	retryRecommendReply := executor.ExecuteCommandLogRecord(ctx, recommendRecord)
	if retryRecommendReply.Err != nil || retryRecommendReply.Result == nil || retryRecommendReply.Result.ID != recommendedDigest.ID {
		t.Fatalf("recommended retry reply = %+v, want projected entry id", retryRecommendReply)
	}
	retryRecommendEvents, err := executor.DecideCommandLogEvents(ctx, recommendRecord, retryRecommendReply)
	if err != nil {
		t.Fatalf("decide recommended retry events: %v", err)
	}
	requireNativeDecisionEventKinds(t, retryRecommendEvents, "recommended retry events", proto.EvtDigestEntryUpserted)

	announcementPayload := marshalCoreTestJSON(t, "marshal announcement thread payload", proto.CurateThreadPayload{
		Thread: sourceThread.ID,
		Kind:   "announcement",
		Title:  "Public announcement",
		Note:   "Campus-wide note.",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "announcement thread", CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-curation-announcement",
		Command:    proto.CmdCurateThread,
		Payload:    announcementPayload,
		EnqueuedAt: 4234,
	})
	generalEvents = replayCommandLogWorkerPartition(t, ctx, eventStore, sourcePartition, 0, 10, "replay general events after announcement curation")
	if len(generalEvents) != 5 || generalEvents[4].Kind != proto.EvtDigestEntryUpserted {
		t.Fatalf("general events after announcement curation = %+v, want digest entry event", generalEvents)
	}
	announcementDigest := requireNativeDecisionPayload[proto.DigestEntryUpsertedPayload](t, generalEvents[4], "announcement digest payload")
	if announcementDigest.Kind != "announcement" || announcementDigest.Title != "Public announcement" ||
		announcementDigest.Note != "Campus-wide note." {
		t.Fatalf("announcement digest event = %+v, want deterministic announcement", announcementDigest)
	}
	announcementPartition := LogPartition{Kind: partitionBoard, Key: projections.DigestMirrorAnnouncementBoardID}
	announcementEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, announcementPartition, 0, 10, "replay announcement mirror events")
	requireNativeDecisionEventKinds(t, announcementEvents, "announcement mirror events", proto.EvtBoardCreated, proto.EvtThreadNew, proto.EvtPostAppended)
	announcementThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, announcementEvents[1], "announcement mirror thread payload")
	if announcementThread.ID != "ann_thr_"+announcementDigest.ID || announcementThread.Board != projections.DigestMirrorAnnouncementBoardID ||
		announcementThread.Title != "Public announcement" || announcementThread.AuthorID != admin.ID || announcementThread.TS != 4234 {
		t.Fatalf("announcement mirror thread = %+v, want deterministic mirror thread", announcementThread)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDigestEntryMaintenance(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload := marshalCoreTestJSON(t, "marshal source thread payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Digest maintenance source",
		Body:  "Original source body.",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "source thread", CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-maintenance-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	})
	sourceEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, sourcePartition, 0, 10, "replay source events")
	if len(sourceEvents) != 2 {
		t.Fatalf("source events = %+v, want thread and post", sourceEvents)
	}
	maintenanceAfter := sourceEvents[len(sourceEvents)-1].PartitionOffset
	if maintenanceAfter <= 0 {
		maintenanceAfter = int64(len(sourceEvents))
	}
	sourceThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, sourceEvents[0], "source thread payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-maintenance-test", sourcePartition, 10, "materialize source events")
	sourcePosts, err := c.ListPosts(sourceThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}
	entryID, err := projections.UpsertDigestEntry(c.DB, "dig_maintenance", "general", "post", sourcePosts[0].ID, "archive", "Original title", "faq/original", "Original note", admin.ID)
	if err != nil {
		t.Fatalf("upsert digest entry: %v", err)
	}
	if _, err := projections.UpsertDigestEntry(c.DB, "dig_maintenance_conflict", "general", "post", sourcePosts[0].ID, "archive", "Conflict title", "faq/conflict", "", admin.ID); err != nil {
		t.Fatalf("upsert conflicting digest entry: %v", err)
	}
	commandPartition := LogPartition{Kind: partitionBoard, Key: entryID}

	newTitle := "Edited archive title"
	newPath := "faq/edited"
	newNote := "Edited note"
	updatePayload := marshalCoreTestJSON(t, "marshal update digest payload", proto.UpdateDigestEntryPayload{
		Entry: entryID,
		Title: &newTitle,
		Path:  &newPath,
		Note:  &newNote,
	})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-maintenance-forbidden",
		Command:    proto.CmdUpdateDigestEntry,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "update digest as non-curator", "board curator permission required")
	emptyTitle := ""
	emptyTitlePayload := marshalCoreTestJSON(t, "marshal empty-title update payload", proto.UpdateDigestEntryPayload{
		Entry: entryID,
		Title: &emptyTitle,
	})
	invalidTitle := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-empty-title",
		Command:    proto.CmdUpdateDigestEntry,
		Payload:    emptyTitlePayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, invalidTitle, proto.ErrValidationFailed, "empty-title digest update", "title is required")
	conflictPath := "faq/conflict"
	conflictPayload := marshalCoreTestJSON(t, "marshal conflict update payload", proto.UpdateDigestEntryPayload{
		Entry: entryID,
		Path:  &conflictPath,
	})
	conflict := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-conflict",
		Command:    proto.CmdUpdateDigestEntry,
		Payload:    conflictPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, conflict, proto.ErrConflict, "conflicting digest update", "digest entry already exists at that path")

	updateRecord := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "update digest", CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-update",
		Command:    proto.CmdUpdateDigestEntry,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	eventPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, eventPartition, maintenanceAfter, 10, "replay update digest events")
	requireNativeDecisionEventKinds(t, events, "update digest events", proto.EvtDigestEntryUpdated)
	updateEvent := requireNativeDecisionPayload[proto.DigestEntryUpdatedPayload](t, events[0], "update digest payload")
	if updateEvent.ID != entryID || updateEvent.Board != "general" || updateEvent.TargetKind != "post" ||
		updateEvent.TargetID != sourcePosts[0].ID || updateEvent.Kind != "archive" ||
		updateEvent.Title != newTitle || updateEvent.Path != newPath || updateEvent.Note != newNote ||
		updateEvent.By != admin.ID || updateEvent.TS != 2234 {
		t.Fatalf("update digest event = %+v, want deterministic metadata update", updateEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-maintenance-test", eventPartition, 10, "materialize update digest event")
	updatedEntries, err := c.ListDigestEntries("general", "archive", newPath, 10, 0)
	if err != nil {
		t.Fatalf("list updated digest entries: %v", err)
	}
	if len(updatedEntries) != 1 || updatedEntries[0].ID != entryID || updatedEntries[0].Title != newTitle || updatedEntries[0].Note != newNote {
		t.Fatalf("updated digest entries = %+v, want projected metadata update", updatedEntries)
	}
	retryUpdateReply := executor.ExecuteCommandLogRecord(ctx, updateRecord)
	if retryUpdateReply.Err != nil || retryUpdateReply.Result == nil || retryUpdateReply.Result.ID != entryID {
		t.Fatalf("update digest retry reply = %+v, want projected entry id", retryUpdateReply)
	}
	retryUpdateEvents, err := executor.DecideCommandLogEvents(ctx, updateRecord, retryUpdateReply)
	if err != nil {
		t.Fatalf("decide update digest retry events: %v", err)
	}
	if len(retryUpdateEvents) != 1 || retryUpdateEvents[0].ID != stableCommandLogDecisionID("evt_", updateRecord, 0) ||
		retryUpdateEvents[0].Kind != proto.EvtDigestEntryUpdated {
		t.Fatalf("update digest retry events = %+v, want stable digest update event", retryUpdateEvents)
	}
	retryUpdatePayload := requireNativeDecisionPayload[proto.DigestEntryUpdatedPayload](t, retryUpdateEvents[0], "update digest retry payload")
	if retryUpdatePayload.ID != updateEvent.ID || retryUpdatePayload.Title != updateEvent.Title ||
		retryUpdatePayload.Path != updateEvent.Path || retryUpdatePayload.Note != updateEvent.Note {
		t.Fatalf("update digest retry payload = %+v, want stable payload %+v", retryUpdatePayload, updateEvent)
	}

	bodyPayload := marshalCoreTestJSON(t, "marshal set digest body payload", proto.SetDigestEntryBodyPayload{
		Entry: entryID,
		Body:  "Edited archive body.",
	})
	bodyRecord := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "set digest body", CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-body",
		Command:    proto.CmdSetDigestEntryBody,
		Payload:    bodyPayload,
		EnqueuedAt: 3234,
	})
	events = replayCommandLogWorkerPartition(t, ctx, eventStore, eventPartition, maintenanceAfter, 10, "replay body digest events")
	if len(events) != 2 || events[1].Kind != proto.EvtDigestEntryBodySet {
		t.Fatalf("body digest events = %+v, want digest.entry_body_set", events)
	}
	bodyEvent := requireNativeDecisionPayload[proto.DigestEntryBodySetPayload](t, events[1], "body digest payload")
	if bodyEvent.ID != entryID || bodyEvent.Board != "general" || bodyEvent.Kind != "archive" ||
		bodyEvent.Body != "Edited archive body." || !bodyEvent.Edited || bodyEvent.By != admin.ID || bodyEvent.TS != 3234 {
		t.Fatalf("body digest event = %+v, want deterministic body edit", bodyEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-maintenance-test", eventPartition, 10, "materialize body digest event")
	export, err := c.GetDigestExport(entryID)
	if err != nil {
		t.Fatalf("get edited digest export: %v", err)
	}
	if export == nil || !export.Entry.BodyEdited || export.Body != "Edited archive body." {
		t.Fatalf("edited digest export = %+v, want edited body", export)
	}
	retryBodyReply := executor.ExecuteCommandLogRecord(ctx, bodyRecord)
	if retryBodyReply.Err != nil || retryBodyReply.Result == nil || retryBodyReply.Result.ID != entryID {
		t.Fatalf("set digest body retry reply = %+v, want projected entry id", retryBodyReply)
	}
	retryBodyEvents, err := executor.DecideCommandLogEvents(ctx, bodyRecord, retryBodyReply)
	if err != nil {
		t.Fatalf("decide set digest body retry events: %v", err)
	}
	if len(retryBodyEvents) != 1 || retryBodyEvents[0].ID != stableCommandLogDecisionID("evt_", bodyRecord, 0) ||
		retryBodyEvents[0].Kind != proto.EvtDigestEntryBodySet {
		t.Fatalf("set digest body retry events = %+v, want stable body event", retryBodyEvents)
	}

	resetPayload := marshalCoreTestJSON(t, "marshal reset digest body payload", proto.SetDigestEntryBodyPayload{
		Entry: entryID,
		Body:  "ignored on reset",
		Reset: true,
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "reset digest body", CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-reset",
		Command:    proto.CmdSetDigestEntryBody,
		Payload:    resetPayload,
		EnqueuedAt: 4234,
	})
	events = replayCommandLogWorkerPartition(t, ctx, eventStore, eventPartition, maintenanceAfter, 10, "replay reset digest events")
	if len(events) != 3 || events[2].Kind != proto.EvtDigestEntryBodySet {
		t.Fatalf("reset digest events = %+v, want digest.entry_body_set reset", events)
	}
	resetEvent := requireNativeDecisionPayload[proto.DigestEntryBodySetPayload](t, events[2], "reset digest payload")
	if resetEvent.ID != entryID || resetEvent.Body != "" || resetEvent.Edited || resetEvent.By != admin.ID || resetEvent.TS != 4234 {
		t.Fatalf("reset digest event = %+v, want deterministic reset", resetEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-maintenance-test", eventPartition, 10, "materialize reset digest event")
	export, err = c.GetDigestExport(entryID)
	if err != nil {
		t.Fatalf("get reset digest export: %v", err)
	}
	if export == nil || export.Entry.BodyEdited || export.Body != "Original source body." {
		t.Fatalf("reset digest export = %+v, want source body restored", export)
	}

	removePayload := marshalCoreTestJSON(t, "marshal remove digest payload", proto.RemoveDigestEntryPayload{
		Entry: entryID,
	})
	removeRecord := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "remove digest", CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-remove",
		Command:    proto.CmdRemoveDigestEntry,
		Payload:    removePayload,
		EnqueuedAt: 5234,
	})
	events = replayCommandLogWorkerPartition(t, ctx, eventStore, eventPartition, maintenanceAfter, 10, "replay remove digest events")
	if len(events) != 4 || events[3].Kind != proto.EvtDigestEntryRemoved {
		t.Fatalf("remove digest events = %+v, want digest.entry_removed", events)
	}
	removeEvent := requireNativeDecisionPayload[proto.DigestEntryRemovedPayload](t, events[3], "remove digest payload")
	if removeEvent.ID != entryID || removeEvent.Board != "general" || removeEvent.Kind != "archive" ||
		removeEvent.By != admin.ID || removeEvent.TS != 5234 {
		t.Fatalf("remove digest event = %+v, want deterministic removal", removeEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-maintenance-test", eventPartition, 10, "materialize remove digest event")
	removedEntries, err := c.ListDigestEntries("general", "archive", newPath, 10, 0)
	if err != nil {
		t.Fatalf("list removed digest entries: %v", err)
	}
	if len(removedEntries) != 0 {
		t.Fatalf("removed digest entries = %+v, want none", removedEntries)
	}
	removedExport, err := c.GetDigestExport(entryID)
	if err != nil {
		t.Fatalf("get removed digest export: %v", err)
	}
	if removedExport != nil {
		t.Fatalf("removed digest export = %+v, want nil", removedExport)
	}
	var tombstoneBoard, tombstoneKind, tombstoneBy string
	var tombstoneAt int64
	if err := qQueryRow(c.DB, `SELECT board_id, kind, removed_by, removed_at FROM digest_entry_removals WHERE id=?`, entryID).
		Scan(&tombstoneBoard, &tombstoneKind, &tombstoneBy, &tombstoneAt); err != nil {
		t.Fatalf("lookup digest removal tombstone: %v", err)
	}
	if tombstoneBoard != "general" || tombstoneKind != "archive" || tombstoneBy != admin.ID || tombstoneAt != 5234 {
		t.Fatalf("digest removal tombstone = %q/%q/%q/%d, want general/archive/admin/5234", tombstoneBoard, tombstoneKind, tombstoneBy, tombstoneAt)
	}
	retryRemoveReply := executor.ExecuteCommandLogRecord(ctx, removeRecord)
	if retryRemoveReply.Err != nil || retryRemoveReply.Result == nil || retryRemoveReply.Result.ID != entryID {
		t.Fatalf("remove digest retry reply = %+v, want tombstone-backed success after projection", retryRemoveReply)
	}
	retryRemoveEvents, err := executor.DecideCommandLogEvents(ctx, removeRecord, retryRemoveReply)
	if err != nil {
		t.Fatalf("decide remove digest retry events: %v", err)
	}
	if len(retryRemoveEvents) != 1 || retryRemoveEvents[0].ID != stableCommandLogDecisionID("evt_", removeRecord, 0) ||
		retryRemoveEvents[0].Kind != proto.EvtDigestEntryRemoved {
		t.Fatalf("remove digest retry events = %+v, want stable removal event", retryRemoveEvents)
	}
	retryRemovePayload := requireNativeDecisionPayload[proto.DigestEntryRemovedPayload](t, retryRemoveEvents[0], "remove digest retry payload")
	if retryRemovePayload.ID != removeEvent.ID || retryRemovePayload.Board != removeEvent.Board ||
		retryRemovePayload.Kind != removeEvent.Kind || retryRemovePayload.By != removeEvent.By ||
		retryRemovePayload.TS != removeEvent.TS {
		t.Fatalf("remove digest retry payload = %+v, want stable payload %+v", retryRemovePayload, removeEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsSendDigestEntryMail(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload := marshalCoreTestJSON(t, "marshal source thread payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Archive source",
		Body:  "Useful digest body.",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "source thread", CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-mail-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	})
	sourceEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, sourcePartition, 0, 10, "replay source thread events")
	if len(sourceEvents) != 2 {
		t.Fatalf("source thread events = %+v, want thread and post", sourceEvents)
	}
	sourceThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, sourceEvents[0], "source thread payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-mail-test", sourcePartition, 10, "materialize source thread events")
	sourcePosts, err := c.ListPosts(sourceThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}
	entryID, err := projections.UpsertDigestEntry(c.DB, "dig_native", "general", "post", sourcePosts[0].ID, "archive", "Archive child", "faq/howto", "Useful digest note", bob.ID)
	if err != nil {
		t.Fatalf("upsert digest entry: %v", err)
	}
	export, err := c.GetDigestExport(entryID)
	if err != nil {
		t.Fatalf("get digest export: %v", err)
	}
	if export == nil || !strings.Contains(FormatDigestExportText(export), "Useful digest body.") {
		t.Fatalf("digest export = %+v, want source body", export)
	}

	mailPayload := marshalCoreTestJSON(t, "marshal digest mail payload", proto.SendDigestEntryMailPayload{
		Entry: entryID,
		To:    []string{"alice"},
		Note:  "Please keep this one.",
	})
	mailPartition := LogPartition{Kind: partitionMail, Key: entryID}
	mailEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "digest mail", mailEventPartition, 0, 10, CommandLogRecord{
		Partition:  mailPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-digest-mail",
		Command:    proto.CmdSendDigestEntryMail,
		Payload:    mailPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "digest mail events", proto.EvtMailSent)
	mailEvent := requireNativeDecisionPayload[proto.MailSentPayload](t, events[0], "digest mail payload")
	if mailEvent.ID == "" || mailEvent.FromUserID != bob.ID || len(mailEvent.ToUserIDs) != 1 ||
		mailEvent.ToUserIDs[0] != alice.ID || mailEvent.Subject != "Archive: Archive child" || mailEvent.TS != 2234 {
		t.Fatalf("digest mail event = %+v, want bob mail to alice", mailEvent)
	}
	for _, want := range []string{"Please keep this one.", "Archive child", "Board: General (general)", "Kind: archive", "Path: faq/howto", "Note: Useful digest note", "Useful digest body."} {
		if !strings.Contains(mailEvent.Body, want) {
			t.Fatalf("digest mail body missing %q:\n%s", want, mailEvent.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-mail-test", mailEventPartition, 10, "materialize digest mail event")
	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice inbox: %v", err)
	}
	if len(aliceInbox) != 1 || aliceInbox[0].ID != mailEvent.ID || !strings.Contains(aliceInbox[0].Body, "Useful digest body.") {
		t.Fatalf("alice inbox = %+v, want native digest mail", aliceInbox)
	}

	secretTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin secret board setup: %v", err)
	}
	if err := projections.InsertBoard(secretTx, "secret", "Secret", "Members only", "", 2); err != nil {
		secretTx.Rollback() //nolint:errcheck
		t.Fatalf("insert secret board: %v", err)
	}
	if err := secretTx.Commit(); err != nil {
		t.Fatalf("commit secret board setup: %v", err)
	}
	memberRead := true
	if err := projections.SetBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read mode: %v", err)
	}
	secretPayload := marshalCoreTestJSON(t, "marshal private source payload", proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private archive source",
		Body:  "hidden digest body",
	})
	secretPartition := LogPartition{Kind: partitionBoard, Key: "secret"}
	_, secretEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "private source", 0, 10, CommandLogRecord{
		Partition:  secretPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-mail-private-source",
		Command:    proto.CmdCreateThread,
		Payload:    secretPayload,
		EnqueuedAt: 3234,
	})
	if len(secretEvents) != 2 {
		t.Fatalf("private source events = %+v, want thread and post", secretEvents)
	}
	secretThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, secretEvents[0], "private source thread payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-digest-mail-test", secretPartition, 10, "materialize private source events")
	secretPosts, err := c.ListPosts(secretThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list private source posts: %v", err)
	}
	if len(secretPosts) != 1 {
		t.Fatalf("private source posts = %+v, want one", secretPosts)
	}
	secretEntryID, err := projections.UpsertDigestEntry(c.DB, "dig_secret_native", "secret", "post", secretPosts[0].ID, "archive", "Secret archive", "", "", admin.ID)
	if err != nil {
		t.Fatalf("upsert private digest entry: %v", err)
	}
	privateMailPayload := marshalCoreTestJSON(t, "marshal private digest mail payload", proto.SendDigestEntryMailPayload{
		Entry: secretEntryID,
		To:    []string{"alice"},
	})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: secretEntryID},
		Offset:     2,
		ActorID:    bob.ID,
		CID:        "cid-native-digest-mail-private",
		Command:    proto.CmdSendDigestEntryMail,
		Payload:    privateMailPayload,
		EnqueuedAt: 4234,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "private digest mail denied")
}

func TestNativeCommandLogDecisionExecutorProjectsSetMailGroup(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	commandPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	invalidPayload := marshalCoreTestJSON(t, "marshal invalid mail group payload", proto.SetMailGroupPayload{
		Name:    "lab",
		Members: []string{"bob"},
	})
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group-invalid",
		Command:    proto.CmdSetMailGroup,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, invalid, proto.ErrValidationFailed, "invalid mail group decision")

	payload := marshalCoreTestJSON(t, "marshal mail group payload", proto.SetMailGroupPayload{
		Name:    " lab ",
		Members: []string{"alice", "carol", "alice"},
	})
	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "mail group", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group",
		Command:    proto.CmdSetMailGroup,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "mail group events", proto.EvtMailGroupSet)
	groupEvent := requireNativeDecisionPayload[proto.MailGroupSetPayload](t, events[0], "mail group payload")
	if groupEvent.ID == "" || groupEvent.OwnerID != bob.ID || groupEvent.Name != "lab" ||
		len(groupEvent.MemberIDs) != 2 || groupEvent.MemberIDs[0] != alice.ID ||
		groupEvent.MemberIDs[1] != carol.ID || groupEvent.TS != 2234 {
		t.Fatalf("mail group event = %+v, want normalized deduped group", groupEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mail-group-test", eventPartition, 10, "materialize mail group event")
	groups, err := c.ListMailGroups(bob.ID)
	if err != nil {
		t.Fatalf("list mail groups: %v", err)
	}
	var lab *projections.MailGroup
	for i := range groups {
		if groups[i].ID == groupEvent.ID {
			lab = &groups[i]
			break
		}
	}
	if lab == nil || lab.Name != "lab" || len(lab.Members) != 2 ||
		lab.Members[0].UserID != alice.ID || lab.Members[1].UserID != carol.ID {
		t.Fatalf("mail groups after projection = %+v, want lab with alice/carol", groups)
	}

	invalidDeletePayload := marshalCoreTestJSON(t, "marshal invalid delete mail group payload", proto.DeleteMailGroupPayload{Group: " "})
	invalidDelete := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: "missing"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group-delete-invalid",
		Command:    proto.CmdDeleteMailGroup,
		Payload:    invalidDeletePayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, invalidDelete, proto.ErrValidationFailed, "invalid delete mail group decision", "group is required")

	missingDeletePayload := marshalCoreTestJSON(t, "marshal missing delete mail group payload", proto.DeleteMailGroupPayload{Group: "missing"})
	missingDelete := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: "missing"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group-delete-missing",
		Command:    proto.CmdDeleteMailGroup,
		Payload:    missingDeletePayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, missingDelete, proto.ErrNotFound, "missing delete mail group decision", "mail group not found")

	deletePayload := marshalCoreTestJSON(t, "marshal delete mail group payload", proto.DeleteMailGroupPayload{Group: groupEvent.ID})
	deletePartition := LogPartition{Kind: partitionMail, Key: groupEvent.ID}
	deleteRecord, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "delete mail group", eventPartition, 0, 10, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group-delete",
		Command:    proto.CmdDeleteMailGroup,
		Payload:    deletePayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtMailGroupDeleted {
		t.Fatalf("delete mail group events = %+v, want set then deleted", events)
	}
	deletedEvent := requireNativeDecisionPayload[proto.MailGroupDeletedPayload](t, events[1], "delete mail group payload")
	if deletedEvent.ID != groupEvent.ID || deletedEvent.OwnerID != bob.ID || deletedEvent.TS != 3234 {
		t.Fatalf("delete mail group event = %+v, want stable deleted group", deletedEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-mail-group-test", eventPartition, 10, "materialize delete mail group event")
	groups, err = c.ListMailGroups(bob.ID)
	if err != nil {
		t.Fatalf("list mail groups after delete: %v", err)
	}
	for _, group := range groups {
		if group.ID == groupEvent.ID {
			t.Fatalf("mail groups after delete = %+v, want deleted group removed", groups)
		}
	}
	var deletedGroupID string
	if err := qQueryRow(c.DB, `SELECT group_id FROM mail_group_deletions WHERE event_id=?`, stableCommandLogDecisionID("evt_", deleteRecord, 0)).Scan(&deletedGroupID); err != nil {
		t.Fatalf("lookup mail group deletion tombstone: %v", err)
	}
	if deletedGroupID != groupEvent.ID {
		t.Fatalf("mail group deletion tombstone group = %q, want %q", deletedGroupID, groupEvent.ID)
	}
	retryDeleteReply := executor.ExecuteCommandLogRecord(ctx, deleteRecord)
	if retryDeleteReply.Err != nil || retryDeleteReply.Result == nil || retryDeleteReply.Result.ID != groupEvent.ID {
		t.Fatalf("delete mail group retry reply = %+v, want tombstone-backed ack", retryDeleteReply)
	}
	retryDeleteEvents, err := executor.DecideCommandLogEvents(ctx, deleteRecord, retryDeleteReply)
	if err != nil {
		t.Fatalf("decide delete mail group retry events: %v", err)
	}
	if len(retryDeleteEvents) != 1 || retryDeleteEvents[0].ID != stableCommandLogDecisionID("evt_", deleteRecord, 0) ||
		retryDeleteEvents[0].Kind != proto.EvtMailGroupDeleted {
		t.Fatalf("delete mail group retry events = %+v, want stable deleted event", retryDeleteEvents)
	}
	retryDeleted := requireNativeDecisionPayload[proto.MailGroupDeletedPayload](t, retryDeleteEvents[0], "delete mail group retry payload")
	if retryDeleted.ID != deletedEvent.ID || retryDeleted.OwnerID != deletedEvent.OwnerID || retryDeleted.TS != deletedEvent.TS {
		t.Fatalf("delete mail group retry payload = %+v, want %+v", retryDeleted, deletedEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsUpdateMail(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sendPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	sendPayload := marshalCoreTestJSON(t, "marshal send mail payload", proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Campus plans",
		Body:    "Meet by the old terminal.",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "send mail", CommandLogRecord{
		Partition:  sendPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-update-mail-send",
		Command:    proto.CmdSendMail,
		Payload:    sendPayload,
		EnqueuedAt: 1234,
	})
	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, eventPartition, 0, 10, "replay send mail events")
	requireNativeDecisionEventKinds(t, events, "send mail events", proto.EvtMailSent)
	mailEvent := requireNativeDecisionPayload[proto.MailSentPayload](t, events[0], "send mail payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-update-mail-test", eventPartition, 10, "materialize send mail event")

	updatePartition := LogPartition{Kind: partitionMail, Key: mailEvent.ID}
	emptyPayload := marshalCoreTestJSON(t, "marshal empty update mail payload", proto.UpdateMailPayload{Mail: mailEvent.ID})
	empty := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  updatePartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-update-mail-empty",
		Command:    proto.CmdUpdateMail,
		Payload:    emptyPayload,
		EnqueuedAt: 1734,
	})
	requireNativeDecisionTerminalError(t, empty, proto.ErrValidationFailed, "empty update mail decision")
	read := true
	carolPayload := marshalCoreTestJSON(t, "marshal carol update mail payload", proto.UpdateMailPayload{Mail: mailEvent.ID, Read: &read})
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  updatePartition,
		Offset:     2,
		ActorID:    carol.ID,
		CID:        "cid-native-update-mail-missing",
		Command:    proto.CmdUpdateMail,
		Payload:    carolPayload,
		EnqueuedAt: 1834,
	})
	requireNativeDecisionTerminalError(t, missing, proto.ErrNotFound, "missing update mail decision")
	invalidMailbox := "bad.mailbox"
	invalidMailboxPayload := marshalCoreTestJSON(t, "marshal invalid mailbox update payload", proto.UpdateMailPayload{
		Mail:    mailEvent.ID,
		Mailbox: &invalidMailbox,
	})
	invalidMailboxReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  updatePartition,
		Offset:     3,
		ActorID:    alice.ID,
		CID:        "cid-native-update-mail-invalid-mailbox",
		Command:    proto.CmdUpdateMail,
		Payload:    invalidMailboxPayload,
		EnqueuedAt: 1934,
	})
	requireNativeDecisionTerminalError(t, invalidMailboxReply, proto.ErrValidationFailed, "invalid mailbox update decision", validationMessage(invalidMailbox, proto.NormalizeMailbox))

	kept := true
	mailbox := "Keep"
	updatePayload := marshalCoreTestJSON(t, "marshal update mail payload", proto.UpdateMailPayload{
		Mail:    mailEvent.ID,
		Mailbox: &mailbox,
		Read:    &read,
		Kept:    &kept,
	})
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "update mail", eventPartition, 0, 10, CommandLogRecord{
		Partition:  updatePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-update-mail",
		Command:    proto.CmdUpdateMail,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtMailCopyUpdated {
		t.Fatalf("update mail events = %+v, want mail.sent then mail.copy_updated", events)
	}
	updateEvent := requireNativeDecisionPayload[proto.MailCopyUpdatedPayload](t, events[1], "update mail payload")
	if updateEvent.Mail != mailEvent.ID || updateEvent.UserID != alice.ID || updateEvent.Mailbox == nil ||
		*updateEvent.Mailbox != "keep" || updateEvent.Read == nil || !*updateEvent.Read ||
		updateEvent.Kept == nil || !*updateEvent.Kept || updateEvent.TS != 2234 {
		t.Fatalf("update mail event = %+v, want normalized keep/read/kept update", updateEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-update-mail-test", eventPartition, 10, "materialize update mail event")
	aliceKeep, err := c.ListMail(alice.ID, "keep", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice keep mail: %v", err)
	}
	if len(aliceKeep) != 1 || aliceKeep[0].ID != mailEvent.ID || !aliceKeep[0].Read || !aliceKeep[0].Kept {
		t.Fatalf("alice keep mail = %+v, want read kept mail", aliceKeep)
	}
	unread, err := c.CountUnreadMail(alice.ID)
	if err != nil {
		t.Fatalf("count alice unread mail: %v", err)
	}
	if unread != 0 {
		t.Fatalf("alice unread mail = %d, want 0", unread)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDeleteMail(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sendPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	sendPayload := marshalCoreTestJSON(t, "marshal send mail payload", proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Campus plans",
		Body:    "Please archive this.",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "send mail", CommandLogRecord{
		Partition:  sendPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-delete-mail-send",
		Command:    proto.CmdSendMail,
		Payload:    sendPayload,
		EnqueuedAt: 1234,
	})
	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, eventPartition, 0, 10, "replay send mail events")
	requireNativeDecisionEventKinds(t, events, "send mail events", proto.EvtMailSent)
	mailEvent := requireNativeDecisionPayload[proto.MailSentPayload](t, events[0], "send mail payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-delete-mail-test", eventPartition, 10, "materialize send mail event")

	deletePartition := LogPartition{Kind: partitionMail, Key: mailEvent.ID}
	deletePayload := marshalCoreTestJSON(t, "marshal delete mail payload", proto.DeleteMailPayload{Mail: mailEvent.ID})
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  deletePartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-delete-mail-missing",
		Command:    proto.CmdDeleteMail,
		Payload:    deletePayload,
		EnqueuedAt: 1834,
	})
	requireNativeDecisionTerminalError(t, missing, proto.ErrNotFound, "missing delete mail decision")

	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "delete mail", eventPartition, 0, 10, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-mail",
		Command:    proto.CmdDeleteMail,
		Payload:    deletePayload,
		EnqueuedAt: 2234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtMailCopyUpdated {
		t.Fatalf("delete mail events = %+v, want mail.sent then mail.copy_updated", events)
	}
	deleteEvent := requireNativeDecisionPayload[proto.MailCopyUpdatedPayload](t, events[1], "delete mail payload")
	if deleteEvent.Mail != mailEvent.ID || deleteEvent.UserID != alice.ID || deleteEvent.Mailbox == nil ||
		*deleteEvent.Mailbox != "trash" || deleteEvent.Read != nil || deleteEvent.Kept != nil || deleteEvent.TS != 2234 {
		t.Fatalf("delete mail event = %+v, want trash mailbox update only", deleteEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-delete-mail-test", eventPartition, 10, "materialize delete mail event")
	aliceTrash, err := c.ListMail(alice.ID, "trash", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice trash mail: %v", err)
	}
	if len(aliceTrash) != 1 || aliceTrash[0].ID != mailEvent.ID {
		t.Fatalf("alice trash mail = %+v, want deleted mail", aliceTrash)
	}
	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice inbox mail: %v", err)
	}
	if len(aliceInbox) != 0 {
		t.Fatalf("alice inbox mail after delete = %+v, want empty", aliceInbox)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDeleteMailRange(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sendPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	for i, subject := range []string{"Campus one", "Campus two"} {
		payload := marshalCoreTestJSON(t, fmt.Sprintf("marshal send mail payload %d", i), proto.SendMailPayload{
			To:      []string{"alice"},
			Subject: subject,
			Body:    "Please archive this.",
		})
		produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, fmt.Sprintf("send mail %d", i), CommandLogRecord{
			Partition:  sendPartition,
			ActorID:    bob.ID,
			CID:        fmt.Sprintf("cid-native-delete-mail-range-send-%d", i),
			Command:    proto.CmdSendMail,
			Payload:    payload,
			EnqueuedAt: int64(1234 + i),
		})
	}

	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, eventPartition, 0, 10, "replay send mail events")
	requireNativeDecisionEventKinds(t, events, "send mail events", proto.EvtMailSent, proto.EvtMailSent)
	firstMail := requireNativeDecisionPayload[proto.MailSentPayload](t, events[0], "first send mail payload")
	secondMail := requireNativeDecisionPayload[proto.MailSentPayload](t, events[1], "second send mail payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-delete-mail-range-test", eventPartition, 10, "materialize send mail events")

	rangePartition := LogPartition{Kind: partitionMail, Key: firstMail.ID}
	missingPayload := marshalCoreTestJSON(t, "marshal missing range payload", proto.DeleteMailRangePayload{Mail: []string{firstMail.ID, "missing_mail"}})
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  rangePartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-mail-range-missing",
		Command:    proto.CmdDeleteMailRange,
		Payload:    missingPayload,
		EnqueuedAt: 1834,
	})
	requireNativeDecisionTerminalError(t, missing, proto.ErrNotFound, "missing range delete decision")
	emptyRangePayload := marshalCoreTestJSON(t, "marshal empty range payload", proto.DeleteMailRangePayload{})
	emptyRange := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  rangePartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-mail-range-empty",
		Command:    proto.CmdDeleteMailRange,
		Payload:    emptyRangePayload,
		EnqueuedAt: 1834,
	})
	requireNativeDecisionTerminalError(t, emptyRange, proto.ErrValidationFailed, "empty range delete decision", validationMessage(nil, proto.NormalizeMailRangeIDs))

	rangePayload := marshalCoreTestJSON(t, "marshal range payload", proto.DeleteMailRangePayload{Mail: []string{firstMail.ID, secondMail.ID, firstMail.ID}})
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "range delete", eventPartition, 0, 10, CommandLogRecord{
		Partition:  rangePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-mail-range",
		Command:    proto.CmdDeleteMailRange,
		Payload:    rangePayload,
		EnqueuedAt: 2234,
	})
	if len(events) != 4 || events[2].Kind != proto.EvtMailCopyUpdated || events[3].Kind != proto.EvtMailCopyUpdated {
		t.Fatalf("range delete events = %+v, want two sends then two copy updates", events)
	}
	deleted := map[string]bool{}
	for _, event := range events[2:] {
		updateEvent := requireNativeDecisionPayload[proto.MailCopyUpdatedPayload](t, event, "range delete payload")
		if updateEvent.UserID != alice.ID || updateEvent.Mailbox == nil || *updateEvent.Mailbox != "trash" ||
			updateEvent.Read != nil || updateEvent.Kept != nil || updateEvent.TS != 2234 {
			t.Fatalf("range delete event = %+v, want trash update only", updateEvent)
		}
		deleted[updateEvent.Mail] = true
	}
	if !deleted[firstMail.ID] || !deleted[secondMail.ID] || len(deleted) != 2 {
		t.Fatalf("range delete event ids = %+v, want both unique mails", deleted)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-delete-mail-range-test", eventPartition, 10, "materialize range delete events")
	aliceTrash, err := c.ListMail(alice.ID, "trash", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice trash mail: %v", err)
	}
	trashIDs := map[string]bool{}
	for _, item := range aliceTrash {
		trashIDs[item.ID] = true
	}
	if len(aliceTrash) != 2 || !trashIDs[firstMail.ID] || !trashIDs[secondMail.ID] {
		t.Fatalf("alice trash mail = %+v, want both range-deleted mails", aliceTrash)
	}
	aliceInbox, err := c.ListMail(alice.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list alice inbox mail: %v", err)
	}
	if len(aliceInbox) != 0 {
		t.Fatalf("alice inbox mail after range delete = %+v, want empty", aliceInbox)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDirectMessages(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	if err := projections.SetDirectMessageSettings(c.DB, alice.ID, "friends"); err != nil {
		t.Fatalf("set alice direct-message policy: %v", err)
	}
	blockedPayload := marshalCoreTestJSON(t, "marshal blocked direct message payload", proto.SendDirectMessagePayload{
		To:   "alice",
		Body: "blocked short ping",
	})
	blocked := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "alice"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-blocked",
		Command:    proto.CmdSendDirectMessage,
		Payload:    blockedPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, blocked, proto.ErrForbidden, "direct message blocked by friends policy")
	if err := projections.SetUserRelationship(c.DB, alice.ID, bob.ID, "friend", "", true); err != nil {
		t.Fatalf("set alice friends bob: %v", err)
	}

	commandPartition := LogPartition{Kind: partitionUser, Key: "alice"}
	payload := marshalCoreTestJSON(t, "marshal direct message payload", proto.SendDirectMessagePayload{
		To:   "alice",
		Body: " Short ping ",
	})
	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "direct message", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message",
		Command:    proto.CmdSendDirectMessage,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "direct message events", proto.EvtDirectMessageSent)
	dmEvent := requireNativeDecisionPayload[proto.DirectMessageSentPayload](t, events[0], "direct message payload")
	if dmEvent.ID == "" || dmEvent.FromUserID != bob.ID || dmEvent.From != "bob" ||
		dmEvent.ToUserID != alice.ID || dmEvent.To != "alice" || dmEvent.Body != "Short ping" || dmEvent.TS != 2234 {
		t.Fatalf("direct message event = %+v, want deterministic bob->alice message", dmEvent)
	}
	if dmEvent.ConversationID != proto.DirectConversationID(bob.ID, alice.ID) {
		t.Fatalf("conversation id = %q, want %q", dmEvent.ConversationID, proto.DirectConversationID(bob.ID, alice.ID))
	}

	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-direct-message-test", eventPartition, 10, "materialize direct message event")
	messages, err := c.ListDirectMessages(alice.ID, bob.ID, 10, 0)
	if err != nil {
		t.Fatalf("list alice direct messages: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != dmEvent.ID || messages[0].Mine || messages[0].Read || messages[0].Body != "Short ping" {
		t.Fatalf("materialized direct messages = %+v, want unread incoming message", messages)
	}
	unread, err := c.CountUnreadDirectMessages(alice.ID)
	if err != nil {
		t.Fatalf("count unread direct messages: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unread direct messages = %d, want 1", unread)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDirectMessageRead(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sendPartition := LogPartition{Kind: partitionUser, Key: "alice"}
	sendPayload := marshalCoreTestJSON(t, "marshal direct message payload", proto.SendDirectMessagePayload{
		To:   "alice",
		Body: "read me",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "direct message", CommandLogRecord{
		Partition:  sendPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-read-send",
		Command:    proto.CmdSendDirectMessage,
		Payload:    sendPayload,
		EnqueuedAt: 1234,
	})

	senderEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, senderEventPartition, 0, 10, "replay direct message send events")
	requireNativeDecisionEventKinds(t, events, "direct message send events", proto.EvtDirectMessageSent)
	dmEvent := requireNativeDecisionPayload[proto.DirectMessageSentPayload](t, events[0], "direct message payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-direct-message-read-test", senderEventPartition, 10, "materialize direct message send event")
	unread, err := c.CountUnreadDirectMessages(alice.ID)
	if err != nil {
		t.Fatalf("count unread direct messages after send: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unread direct messages after send = %d, want 1", unread)
	}

	readPartition := LogPartition{Kind: partitionUser, Key: dmEvent.ID}
	readPayload := marshalCoreTestJSON(t, "marshal direct message read payload", proto.MarkDirectMessageReadPayload{Message: dmEvent.ID})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  readPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-read-sender",
		Command:    proto.CmdMarkDirectMessageRead,
		Payload:    readPayload,
		EnqueuedAt: 1734,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrNotFound, "sender direct-message read decision")

	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "direct message read", senderEventPartition, 0, 10, CommandLogRecord{
		Partition:  readPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-read",
		Command:    proto.CmdMarkDirectMessageRead,
		Payload:    readPayload,
		EnqueuedAt: 2234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtDirectMessageRead {
		t.Fatalf("direct message read events = %+v, want send then read", events)
	}
	readEvent := requireNativeDecisionPayload[proto.DirectMessageReadPayload](t, events[1], "direct message read payload")
	if readEvent.MessageID != dmEvent.ID || readEvent.UserID != alice.ID || readEvent.ReadAt != 2234 || readEvent.TS != 2234 {
		t.Fatalf("direct message read event = %+v, want alice read at event timestamp", readEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-direct-message-read-test", senderEventPartition, 10, "materialize direct message read event")
	messages, err := c.ListDirectMessages(alice.ID, bob.ID, 10, 0)
	if err != nil {
		t.Fatalf("list direct messages after read: %v", err)
	}
	if len(messages) != 1 || !messages[0].Read {
		t.Fatalf("direct messages after read = %+v, want read incoming message", messages)
	}
	unread, err = c.CountUnreadDirectMessages(alice.ID)
	if err != nil {
		t.Fatalf("count unread direct messages after read: %v", err)
	}
	if unread != 0 {
		t.Fatalf("unread direct messages after read = %d, want 0", unread)
	}

	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "no-op direct message read", senderEventPartition, 0, 10, CommandLogRecord{
		Partition:  readPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-read-noop",
		Command:    proto.CmdMarkDirectMessageRead,
		Payload:    readPayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 {
		t.Fatalf("events after no-op direct message read = %+v, want no additional event", events)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDirectMessageDelete(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	sendPayload := marshalCoreTestJSON(t, "marshal direct message payload", proto.SendDirectMessagePayload{
		To:   "alice",
		Body: "delete me",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "direct message", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "alice"},
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-delete-send",
		Command:    proto.CmdSendDirectMessage,
		Payload:    sendPayload,
		EnqueuedAt: 1234,
	})

	senderEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, senderEventPartition, 0, 10, "replay direct message send events")
	requireNativeDecisionEventKinds(t, events, "direct message send events", proto.EvtDirectMessageSent)
	dmEvent := requireNativeDecisionPayload[proto.DirectMessageSentPayload](t, events[0], "direct message payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-direct-message-delete-test", senderEventPartition, 10, "materialize direct message send event")

	deletePartition := LogPartition{Kind: partitionUser, Key: dmEvent.ID}
	deletePayload := marshalCoreTestJSON(t, "marshal direct message delete payload", proto.DeleteDirectMessagePayload{Message: dmEvent.ID})
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "recipient direct message delete", senderEventPartition, 0, 10, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-delete-recipient",
		Command:    proto.CmdDeleteDirectMessage,
		Payload:    deletePayload,
		EnqueuedAt: 2234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtDirectMessageDeleted {
		t.Fatalf("recipient direct message delete events = %+v, want send then delete", events)
	}
	deleteEvent := requireNativeDecisionPayload[proto.DirectMessageDeletedPayload](t, events[1], "direct message delete payload")
	if deleteEvent.MessageID != dmEvent.ID || deleteEvent.UserID != alice.ID || deleteEvent.SenderDeleted ||
		!deleteEvent.RecipientDeleted || deleteEvent.TS != 2234 {
		t.Fatalf("recipient direct message delete event = %+v, want recipient-only delete", deleteEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-direct-message-delete-test", senderEventPartition, 10, "materialize recipient direct message delete event")
	aliceMessages, err := c.ListDirectMessages(alice.ID, bob.ID, 10, 0)
	if err != nil {
		t.Fatalf("list alice direct messages after delete: %v", err)
	}
	if len(aliceMessages) != 0 {
		t.Fatalf("alice direct messages after delete = %+v, want hidden", aliceMessages)
	}
	bobMessages, err := c.ListDirectMessages(bob.ID, alice.ID, 10, 0)
	if err != nil {
		t.Fatalf("list bob direct messages after recipient delete: %v", err)
	}
	if len(bobMessages) != 1 || !bobMessages[0].Mine {
		t.Fatalf("bob direct messages after recipient delete = %+v, want sender copy", bobMessages)
	}

	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "no-op recipient direct message delete", senderEventPartition, 0, 10, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-delete-recipient-noop",
		Command:    proto.CmdDeleteDirectMessage,
		Payload:    deletePayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 {
		t.Fatalf("events after no-op recipient direct message delete = %+v, want no additional event", events)
	}

	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "sender direct message delete", senderEventPartition, 0, 10, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-delete-sender",
		Command:    proto.CmdDeleteDirectMessage,
		Payload:    deletePayload,
		EnqueuedAt: 4234,
	})
	if len(events) != 3 || events[2].Kind != proto.EvtDirectMessageDeleted {
		t.Fatalf("sender direct message delete events = %+v, want second delete", events)
	}
	senderDeleteEvent := requireNativeDecisionPayload[proto.DirectMessageDeletedPayload](t, events[2], "sender direct message delete payload")
	if senderDeleteEvent.UserID != bob.ID || !senderDeleteEvent.SenderDeleted ||
		senderDeleteEvent.RecipientDeleted || senderDeleteEvent.TS != 4234 {
		t.Fatalf("sender direct message delete event = %+v, want sender-only delete", senderDeleteEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-direct-message-delete-test", senderEventPartition, 10, "materialize sender direct message delete event")
	bobMessages, err = c.ListDirectMessages(bob.ID, alice.ID, 10, 0)
	if err != nil {
		t.Fatalf("list bob direct messages after sender delete: %v", err)
	}
	if len(bobMessages) != 0 {
		t.Fatalf("bob direct messages after sender delete = %+v, want hidden", bobMessages)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDirectMessageSettings(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	commandPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	invalidPayload := marshalCoreTestJSON(t, "marshal invalid direct-message settings payload", proto.SetDirectMessageSettingsPayload{Policy: "strangers"})
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-settings-invalid",
		Command:    proto.CmdSetDirectMessageSettings,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, invalid, proto.ErrValidationFailed, "invalid direct-message settings decision", validationMessage("strangers", proto.NormalizeDirectMessagePolicy))

	payload := marshalCoreTestJSON(t, "marshal direct-message settings payload", proto.SetDirectMessageSettingsPayload{Policy: "friends-only"})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "direct-message settings", 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-settings",
		Command:    proto.CmdSetDirectMessageSettings,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "direct-message settings events", proto.EvtDirectMessageSettingsSet)
	settingsEvent := requireNativeDecisionPayload[proto.DirectMessageSettingsSetPayload](t, events[0], "direct-message settings payload")
	if settingsEvent.UserID != alice.ID || settingsEvent.Policy != "friends" || settingsEvent.TS != 2234 {
		t.Fatalf("direct-message settings event = %+v, want normalized friends policy", settingsEvent)
	}

	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-direct-message-settings-test", commandPartition, 10, "materialize direct-message settings event")
	settings, err := c.GetDirectMessageSettings(alice.ID)
	if err != nil {
		t.Fatalf("get direct-message settings: %v", err)
	}
	if settings.Policy != "friends" || settings.UpdatedAt != 2234 {
		t.Fatalf("direct-message settings = %+v, want friends at event timestamp", settings)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsUserRelationships(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	commandPartition := LogPartition{Kind: partitionUser, Key: "bob"}
	invalidPayload := marshalCoreTestJSON(t, "marshal invalid relationship payload", proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "enemy",
		Active: true,
	})
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-relationship-invalid",
		Command:    proto.CmdSetUserRelationship,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, invalid, proto.ErrValidationFailed, "invalid relationship decision")
	longRelationshipNote := strings.Repeat("x", proto.MaxUserRelationshipNoteLength+1)
	longNotePayload := marshalCoreTestJSON(t, "marshal long relationship note payload", proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "friends",
		Active: true,
		Note:   longRelationshipNote,
	})
	longNote := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-relationship-long-note",
		Command:    proto.CmdSetUserRelationship,
		Payload:    longNotePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, longNote, proto.ErrValidationFailed, "long relationship note decision", validationMessage(longRelationshipNote, proto.NormalizeUserRelationshipNote))

	addPayload := marshalCoreTestJSON(t, "marshal relationship add payload", proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "friends",
		Active: true,
		Note:   " lab partner ",
	})
	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "relationship add", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-relationship-add",
		Command:    proto.CmdSetUserRelationship,
		Payload:    addPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "relationship events", proto.EvtUserRelationshipSet)
	addEvent := requireNativeDecisionPayload[proto.UserRelationshipSetPayload](t, events[0], "relationship payload")
	if addEvent.UserID != alice.ID || addEvent.TargetUserID != bob.ID || addEvent.Kind != "friend" ||
		!addEvent.Active || addEvent.Note != "lab partner" || addEvent.TS != 2234 {
		t.Fatalf("relationship add event = %+v, want normalized friend add", addEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-relationship-test", eventPartition, 10, "materialize relationship add event")
	friends, err := c.ListSocialUsers(alice.ID, "friends", false)
	if err != nil {
		t.Fatalf("list friends after relationship add: %v", err)
	}
	if len(friends) != 1 || friends[0].Name != "bob" || friends[0].Note != "lab partner" {
		t.Fatalf("friends after relationship add = %+v, want bob with note", friends)
	}

	removePayload := marshalCoreTestJSON(t, "marshal relationship remove payload", proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "follow",
		Active: false,
	})
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "relationship remove", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-relationship-remove",
		Command:    proto.CmdSetUserRelationship,
		Payload:    removePayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtUserRelationshipSet {
		t.Fatalf("relationship remove events = %+v, want add then remove", events)
	}
	removeEvent := requireNativeDecisionPayload[proto.UserRelationshipSetPayload](t, events[1], "relationship remove payload")
	if removeEvent.UserID != alice.ID || removeEvent.TargetUserID != bob.ID || removeEvent.Kind != "friend" ||
		removeEvent.Active || removeEvent.TS != 3234 {
		t.Fatalf("relationship remove event = %+v, want normalized friend remove", removeEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-relationship-test", eventPartition, 10, "materialize relationship remove event")
	friends, err = c.ListSocialUsers(alice.ID, "friends", false)
	if err != nil {
		t.Fatalf("list friends after relationship remove: %v", err)
	}
	if len(friends) != 0 {
		t.Fatalf("friends after relationship remove = %+v, want none", friends)
	}

	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "no-op relationship remove", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-relationship-remove-noop",
		Command:    proto.CmdSetUserRelationship,
		Payload:    removePayload,
		EnqueuedAt: 4234,
	})
	if len(events) != 2 {
		t.Fatalf("events after no-op relationship remove = %+v, want no additional event", events)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsLoginWatch(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")
	if err := projections.SetUserRelationship(c.DB, alice.ID, bob.ID, "friend", "", true); err != nil {
		t.Fatalf("seed alice friend bob: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	commandPartition := LogPartition{Kind: partitionUser, Key: "bob"}
	forbiddenPayload := marshalCoreTestJSON(t, "marshal forbidden login watch payload", proto.SetLoginWatchPayload{User: "bob", Active: true})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-login-watch-forbidden",
		Command:    proto.CmdSetLoginWatch,
		Payload:    forbiddenPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "forbidden login watch decision")

	watchPayload := marshalCoreTestJSON(t, "marshal login watch payload", proto.SetLoginWatchPayload{User: "bob", Active: true})
	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "login watch", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-login-watch",
		Command:    proto.CmdSetLoginWatch,
		Payload:    watchPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "login watch events", proto.EvtUserRelationshipSet)
	watchEvent := requireNativeDecisionPayload[proto.UserRelationshipSetPayload](t, events[0], "login watch relationship payload")
	if watchEvent.UserID != alice.ID || watchEvent.TargetUserID != bob.ID || watchEvent.Kind != "login_watch" ||
		!watchEvent.Active || watchEvent.TS != 2234 {
		t.Fatalf("login watch relationship event = %+v, want active login watch", watchEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-login-watch-test", eventPartition, 10, "materialize login watch event")
	if exists, err := projections.UserRelationshipExists(c.DB, alice.ID, bob.ID, "login_watch"); err != nil || !exists {
		t.Fatalf("login watch relationship exists = %v, %v; want true, nil", exists, err)
	}

	cancelPayload := marshalCoreTestJSON(t, "marshal login watch cancel payload", proto.SetLoginWatchPayload{User: "bob", Active: false})
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "login watch cancel", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-login-watch-cancel",
		Command:    proto.CmdSetLoginWatch,
		Payload:    cancelPayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtUserRelationshipSet {
		t.Fatalf("login watch cancel events = %+v, want second relationship event", events)
	}
	cancelEvent := requireNativeDecisionPayload[proto.UserRelationshipSetPayload](t, events[1], "login watch cancel payload")
	if cancelEvent.UserID != alice.ID || cancelEvent.TargetUserID != bob.ID || cancelEvent.Kind != "login_watch" ||
		cancelEvent.Active || cancelEvent.TS != 3234 {
		t.Fatalf("login watch cancel event = %+v, want inactive login watch", cancelEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-login-watch-test", eventPartition, 10, "materialize login watch cancel event")
	if exists, err := projections.UserRelationshipExists(c.DB, alice.ID, bob.ID, "login_watch"); err != nil || exists {
		t.Fatalf("login watch relationship after cancel = %v, %v; want false, nil", exists, err)
	}

	if err := setUserPresence(c.DB, bob.ID, "web", "active", "reading", "general", "", "General", "127.0.0.1", nowMS()); err != nil {
		t.Fatalf("seed bob presence: %v", err)
	}
	onlineRecord, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "online login watch", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-login-watch-online",
		Command:    proto.CmdSetLoginWatch,
		Payload:    watchPayload,
		EnqueuedAt: 4234,
	})
	if len(events) != 4 || events[2].Kind != proto.EvtNotificationCreated || events[3].Kind != proto.EvtUserRelationshipSet {
		t.Fatalf("online login watch events = %+v, want notification then relationship clear", events)
	}
	notificationEvent := requireNativeDecisionPayload[proto.NotificationCreatedPayload](t, events[2], "online login watch notification payload")
	if notificationEvent.ID != stableCommandLogDecisionID("notif_", onlineRecord, 0) ||
		notificationEvent.UserID != alice.ID || notificationEvent.Kind != "login" ||
		notificationEvent.Actor != "bob" || notificationEvent.ThreadID != "" ||
		notificationEvent.PostID != "" || notificationEvent.TS != 4234 {
		t.Fatalf("online login watch notification = %+v, want login notification for alice", notificationEvent)
	}
	clearEvent := requireNativeDecisionPayload[proto.UserRelationshipSetPayload](t, events[3], "online login watch clear payload")
	if clearEvent.UserID != alice.ID || clearEvent.TargetUserID != bob.ID || clearEvent.Kind != "login_watch" ||
		clearEvent.Active || clearEvent.TS != 4234 {
		t.Fatalf("online login watch clear event = %+v, want inactive login watch", clearEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-login-watch-test", eventPartition, 10, "materialize online login watch events")
	notifications, err := c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("list login watch notifications: %v", err)
	}
	if len(notifications) != 1 || notifications[0].Kind != "login" || notifications[0].Actor != "bob" ||
		notifications[0].ThreadID != "" || notifications[0].PostID != "" {
		t.Fatalf("login watch notifications = %+v, want one login notification from bob", notifications)
	}
	if exists, err := projections.UserRelationshipExists(c.DB, alice.ID, bob.ID, "login_watch"); err != nil || exists {
		t.Fatalf("login watch relationship after online notification = %v, %v; want false, nil", exists, err)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardZap(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	missingPayload := marshalCoreTestJSON(t, "marshal missing board zap payload", proto.SetBoardZapPayload{Board: "missing", Zapped: true})
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-zap-missing",
		Command:    proto.CmdSetBoardZap,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missing, proto.ErrNotFound, "missing board zap decision")

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	zapPayload := marshalCoreTestJSON(t, "marshal board zap payload", proto.SetBoardZapPayload{Board: "general", Zapped: true})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board zap", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-zap",
		Command:    proto.CmdSetBoardZap,
		Payload:    zapPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "board zap events", proto.EvtBoardZapSet)
	zapEvent := requireNativeDecisionPayload[proto.BoardZapSetPayload](t, events[0], "board zap payload")
	if zapEvent.UserID != alice.ID || zapEvent.Board != "general" || !zapEvent.Zapped || zapEvent.TS != 2234 {
		t.Fatalf("board zap event = %+v, want alice zapping general", zapEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-zap-test", partition, 10, "materialize board zap event")
	summaries, err := c.ListBoardSummaries(alice.ID, false)
	if err != nil {
		t.Fatalf("list board summaries after zap: %v", err)
	}
	general := findBoardSummaryForTest(summaries, "general")
	if general == nil || !general.Zapped {
		t.Fatalf("general summary after zap = %+v, want zapped", general)
	}

	clearPayload := marshalCoreTestJSON(t, "marshal board zap clear payload", proto.SetBoardZapPayload{Board: "general", Zapped: false})
	_, events = produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board zap clear", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-zap-clear",
		Command:    proto.CmdSetBoardZap,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtBoardZapSet {
		t.Fatalf("board zap clear events = %+v, want second board.zap_set", events)
	}
	clearEvent := requireNativeDecisionPayload[proto.BoardZapSetPayload](t, events[1], "board zap clear payload")
	if clearEvent.UserID != alice.ID || clearEvent.Board != "general" || clearEvent.Zapped || clearEvent.TS != 3234 {
		t.Fatalf("board zap clear event = %+v, want alice clearing general zap", clearEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-zap-test", partition, 10, "materialize board zap clear event")
	summaries, err = c.ListBoardSummaries(alice.ID, false)
	if err != nil {
		t.Fatalf("list board summaries after clear: %v", err)
	}
	general = findBoardSummaryForTest(summaries, "general")
	if general == nil || general.Zapped {
		t.Fatalf("general summary after clear = %+v, want not zapped", general)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardFavorite(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	missingBoardPayload := marshalCoreTestJSON(t, "marshal missing board favorite payload", proto.SetBoardFavoritePayload{Board: "missing", Favorite: true})
	missingBoard := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-favorite-missing-board",
		Command:    proto.CmdSetBoardFavorite,
		Payload:    missingBoardPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missingBoard, proto.ErrNotFound, "missing board favorite decision")

	techTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin tech board setup: %v", err)
	}
	if err := projections.InsertBoard(techTx, "tech", "Tech", "Technology discussion", "", 1); err != nil {
		techTx.Rollback() //nolint:errcheck
		t.Fatalf("insert tech board: %v", err)
	}
	if err := techTx.Commit(); err != nil {
		t.Fatalf("commit tech board setup: %v", err)
	}

	missingFolderPayload := marshalCoreTestJSON(t, "marshal missing folder favorite payload", proto.SetBoardFavoritePayload{Board: "tech", Favorite: true, FolderID: "missing"})
	missingFolder := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "tech"},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-board-favorite-missing-folder",
		Command:    proto.CmdSetBoardFavorite,
		Payload:    missingFolderPayload,
		EnqueuedAt: 1334,
	})
	requireNativeDecisionTerminalError(t, missingFolder, proto.ErrNotFound, "missing folder favorite decision")

	partition := LogPartition{Kind: partitionBoard, Key: "tech"}
	position := 0
	favoritePayload := marshalCoreTestJSON(t, "marshal board favorite payload", proto.SetBoardFavoritePayload{Board: "tech", Favorite: true, Position: &position})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board favorite", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-favorite",
		Command:    proto.CmdSetBoardFavorite,
		Payload:    favoritePayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "board favorite events", proto.EvtBoardFavoriteSet)
	favoriteEvent := requireNativeDecisionPayload[proto.BoardFavoriteSetPayload](t, events[0], "board favorite payload")
	if favoriteEvent.UserID != alice.ID || favoriteEvent.Board != "tech" || !favoriteEvent.Favorite ||
		favoriteEvent.Position == nil || *favoriteEvent.Position != 0 || favoriteEvent.TS != 2234 {
		t.Fatalf("board favorite event = %+v, want alice favoriting tech at position 0", favoriteEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-favorite-test", partition, 10, "materialize board favorite event")
	favorites, err := c.ListFavoriteBoards(alice.ID)
	if err != nil {
		t.Fatalf("list favorite boards after favorite: %v", err)
	}
	if !hasBoardForTest(favorites, "tech") {
		t.Fatalf("favorite boards after favorite = %+v, want tech", favorites)
	}

	clearPayload := marshalCoreTestJSON(t, "marshal board favorite clear payload", proto.SetBoardFavoritePayload{Board: "tech", Favorite: false})
	_, events = produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "board favorite clear", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-favorite-clear",
		Command:    proto.CmdSetBoardFavorite,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtBoardFavoriteSet {
		t.Fatalf("board favorite clear events = %+v, want second board.favorite_set", events)
	}
	clearEvent := requireNativeDecisionPayload[proto.BoardFavoriteSetPayload](t, events[1], "board favorite clear payload")
	if clearEvent.UserID != alice.ID || clearEvent.Board != "tech" || clearEvent.Favorite || clearEvent.TS != 3234 {
		t.Fatalf("board favorite clear event = %+v, want alice clearing tech favorite", clearEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-board-favorite-test", partition, 10, "materialize board favorite clear event")
	favorites, err = c.ListFavoriteBoards(alice.ID)
	if err != nil {
		t.Fatalf("list favorite boards after clear: %v", err)
	}
	if hasBoardForTest(favorites, "tech") {
		t.Fatalf("favorite boards after clear = %+v, want tech removed", favorites)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsCreateFavoriteFolder(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	emptyNamePayload := marshalCoreTestJSON(t, "marshal empty favorite folder payload", proto.CreateFavoriteFolderPayload{Name: "   "})
	emptyName := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-favorite-folder-empty-name",
		Command:    proto.CmdCreateFavoriteFolder,
		Payload:    emptyNamePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, emptyName, proto.ErrValidationFailed, "empty favorite folder decision")
	longFolderName := strings.Repeat("x", proto.MaxFavoriteFolderNameLength+1)
	longNamePayload := marshalCoreTestJSON(t, "marshal long favorite folder name payload", proto.CreateFavoriteFolderPayload{Name: longFolderName})
	longName := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-favorite-folder-long-name",
		Command:    proto.CmdCreateFavoriteFolder,
		Payload:    longNamePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, longName, proto.ErrValidationFailed, "long favorite folder decision", favoriteFolderNameValidationMessage(longFolderName, true))

	missingParentPayload := marshalCoreTestJSON(t, "marshal missing parent favorite folder payload", proto.CreateFavoriteFolderPayload{Name: "Work", ParentID: "missing"})
	missingParent := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-favorite-folder-missing-parent",
		Command:    proto.CmdCreateFavoriteFolder,
		Payload:    missingParentPayload,
		EnqueuedAt: 1334,
	})
	requireNativeDecisionTerminalError(t, missingParent, proto.ErrNotFound, "missing parent favorite folder decision")

	partition := LogPartition{Kind: partitionUser, Key: alice.ID}
	rootPayload := marshalCoreTestJSON(t, "marshal root favorite folder payload", proto.CreateFavoriteFolderPayload{Name: " Work "})
	rootRecord, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "root favorite folder", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-favorite-folder-root",
		Command:    proto.CmdCreateFavoriteFolder,
		Payload:    rootPayload,
		EnqueuedAt: 2234,
	})
	rootID := stableCommandLogDecisionID("favfld_", rootRecord, 0)
	requireNativeDecisionEventKinds(t, events, "root favorite folder events", proto.EvtFavoriteFolderCreated)
	rootEvent := requireNativeDecisionPayload[proto.FavoriteFolderCreatedPayload](t, events[0], "root favorite folder payload")
	if rootEvent.ID != rootID || rootEvent.UserID != alice.ID || rootEvent.ParentID != "" ||
		rootEvent.Name != "Work" || rootEvent.Position != 0 || rootEvent.TS != 2234 {
		t.Fatalf("root favorite folder event = %+v, want normalized Work root folder", rootEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-favorite-folder-test", partition, 10, "materialize root favorite folder event")
	tree, err := c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatalf("list favorite tree after root folder: %v", err)
	}
	rootFolder := findFavoriteFolderForTest(tree, rootID)
	if rootFolder == nil || rootFolder.Name != "Work" || rootFolder.ParentID != "" || rootFolder.Position != 0 {
		t.Fatalf("root favorite folder after materialize = %+v in tree %+v", rootFolder, tree)
	}

	childPosition := 0
	childPayload := marshalCoreTestJSON(t, "marshal child favorite folder payload", proto.CreateFavoriteFolderPayload{Name: "Child", ParentID: rootID, Position: &childPosition})
	childRecord, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "child favorite folder", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-favorite-folder-child",
		Command:    proto.CmdCreateFavoriteFolder,
		Payload:    childPayload,
		EnqueuedAt: 3234,
	})
	childID := stableCommandLogDecisionID("favfld_", childRecord, 0)
	if len(events) != 2 || events[1].Kind != proto.EvtFavoriteFolderCreated {
		t.Fatalf("child favorite folder events = %+v, want second favorite_folder.created", events)
	}
	childEvent := requireNativeDecisionPayload[proto.FavoriteFolderCreatedPayload](t, events[1], "child favorite folder payload")
	if childEvent.ID != childID || childEvent.UserID != alice.ID || childEvent.ParentID != rootID ||
		childEvent.Name != "Child" || childEvent.Position != 0 || childEvent.TS != 3234 {
		t.Fatalf("child favorite folder event = %+v, want child under root folder", childEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-favorite-folder-test", partition, 10, "materialize child favorite folder event")
	tree, err = c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatalf("list favorite tree after child folder: %v", err)
	}
	childFolder := findFavoriteFolderForTest(tree, childID)
	if childFolder == nil || childFolder.Name != "Child" || childFolder.ParentID != rootID || childFolder.Position != 0 {
		t.Fatalf("child favorite folder after materialize = %+v in tree %+v", childFolder, tree)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsUpdateFavoriteFolder(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")

	const workID = "favfld_work"
	const projectID = "favfld_project"
	if err := projections.CreateFavoriteFolder(c.DB, alice.ID, workID, "", "Work", nil); err != nil {
		t.Fatalf("create work folder setup: %v", err)
	}
	if err := projections.CreateFavoriteFolder(c.DB, alice.ID, projectID, workID, "Projects", nil); err != nil {
		t.Fatalf("create project folder setup: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	missingPayload := marshalCoreTestJSON(t, "marshal missing update favorite folder payload", proto.UpdateFavoriteFolderPayload{Folder: "missing", Name: "Nope"})
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-update-favorite-folder-missing",
		Command:    proto.CmdUpdateFavoriteFolder,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missing, proto.ErrNotFound, "missing favorite folder update decision")

	selfParent := workID
	selfPayload := marshalCoreTestJSON(t, "marshal self-parent update favorite folder payload", proto.UpdateFavoriteFolderPayload{Folder: workID, ParentID: &selfParent})
	self := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: workID},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-update-favorite-folder-self",
		Command:    proto.CmdUpdateFavoriteFolder,
		Payload:    selfPayload,
		EnqueuedAt: 1334,
	})
	requireNativeDecisionTerminalError(t, self, proto.ErrValidationFailed, "self-parent favorite folder update decision")

	descendantParent := projectID
	descendantPayload := marshalCoreTestJSON(t, "marshal descendant update favorite folder payload", proto.UpdateFavoriteFolderPayload{Folder: workID, ParentID: &descendantParent})
	descendant := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: workID},
		Offset:     3,
		ActorID:    alice.ID,
		CID:        "cid-native-update-favorite-folder-descendant",
		Command:    proto.CmdUpdateFavoriteFolder,
		Payload:    descendantPayload,
		EnqueuedAt: 1434,
	})
	requireNativeDecisionTerminalError(t, descendant, proto.ErrValidationFailed, "descendant favorite folder update decision")

	rootParent := ""
	zero := 0
	updatePayload := marshalCoreTestJSON(t, "marshal update favorite folder payload", proto.UpdateFavoriteFolderPayload{
		Folder:   projectID,
		Name:     " Projects Renamed ",
		ParentID: &rootParent,
		Position: &zero,
	})
	commandPartition := LogPartition{Kind: partitionUser, Key: projectID}
	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "update favorite folder", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-update-favorite-folder",
		Command:    proto.CmdUpdateFavoriteFolder,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "update favorite folder events", proto.EvtFavoriteFolderUpdated)
	updateEvent := requireNativeDecisionPayload[proto.FavoriteFolderUpdatedPayload](t, events[0], "update favorite folder payload")
	if updateEvent.ID != projectID || updateEvent.UserID != alice.ID || updateEvent.ParentID != "" ||
		updateEvent.Name != "Projects Renamed" || updateEvent.Position != 0 || updateEvent.TS != 2234 {
		t.Fatalf("update favorite folder event = %+v, want renamed root project folder", updateEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-update-favorite-folder-test", eventPartition, 10, "materialize update favorite folder event")
	tree, err := c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatalf("list favorite tree after update: %v", err)
	}
	project := findFavoriteFolderForTest(tree, projectID)
	if project == nil || project.Name != "Projects Renamed" || project.ParentID != "" || project.Position != 0 {
		t.Fatalf("project folder after update = %+v in tree %+v", project, tree)
	}
	work := findFavoriteFolderForTest(tree, workID)
	if work == nil || work.ParentID != "" || work.Position != 1 {
		t.Fatalf("work folder after project move = %+v in tree %+v", work, tree)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDeleteFavoriteFolder(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")

	techTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin tech board setup: %v", err)
	}
	if err := projections.InsertBoard(techTx, "tech", "Tech", "Technology discussion", "", 1); err != nil {
		techTx.Rollback() //nolint:errcheck
		t.Fatalf("insert tech board: %v", err)
	}
	if err := techTx.Commit(); err != nil {
		t.Fatalf("commit tech board setup: %v", err)
	}
	const workID = "favfld_work"
	const childID = "favfld_child"
	if err := projections.CreateFavoriteFolder(c.DB, alice.ID, workID, "", "Work", nil); err != nil {
		t.Fatalf("create work folder setup: %v", err)
	}
	if err := projections.CreateFavoriteFolder(c.DB, alice.ID, childID, workID, "Child", nil); err != nil {
		t.Fatalf("create child folder setup: %v", err)
	}
	if err := projections.SetBoardFavorite(c.DB, alice.ID, "tech", workID, nil, true); err != nil {
		t.Fatalf("favorite tech in work setup: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	missingPayload := marshalCoreTestJSON(t, "marshal missing delete favorite folder payload", proto.DeleteFavoriteFolderPayload{Folder: "missing"})
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-favorite-folder-missing",
		Command:    proto.CmdDeleteFavoriteFolder,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missing, proto.ErrNotFound, "missing favorite folder delete decision")

	commandPartition := LogPartition{Kind: partitionUser, Key: workID}
	deletePayload := marshalCoreTestJSON(t, "marshal delete favorite folder payload", proto.DeleteFavoriteFolderPayload{Folder: workID})
	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "delete favorite folder", eventPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-favorite-folder",
		Command:    proto.CmdDeleteFavoriteFolder,
		Payload:    deletePayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "delete favorite folder events", proto.EvtFavoriteFolderDeleted)
	deleteEvent := requireNativeDecisionPayload[proto.FavoriteFolderDeletedPayload](t, events[0], "delete favorite folder payload")
	if deleteEvent.ID != workID || deleteEvent.UserID != alice.ID || deleteEvent.ParentID != "" || deleteEvent.TS != 2234 {
		t.Fatalf("delete favorite folder event = %+v, want work folder deleted to root", deleteEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-delete-favorite-folder-test", eventPartition, 10, "materialize delete favorite folder event")
	tree, err := c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatalf("list favorite tree after delete: %v", err)
	}
	if folder := findFavoriteFolderForTest(tree, workID); folder != nil {
		t.Fatalf("work folder after delete = %+v in tree %+v, want removed", folder, tree)
	}
	child := findFavoriteFolderForTest(tree, childID)
	if child == nil || child.ParentID != "" {
		t.Fatalf("child folder after deleting work = %+v in tree %+v, want moved to root", child, tree)
	}
	techFavorite := findFavoriteBoardEntryForTest(tree, "tech")
	if techFavorite == nil || techFavorite.FolderID != "" {
		t.Fatalf("tech favorite after deleting work = %+v in tree %+v, want moved to root", techFavorite, tree)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsMoveBoardFavorite(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")

	techTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin tech board setup: %v", err)
	}
	if err := projections.InsertBoard(techTx, "tech", "Tech", "Technology discussion", "", 1); err != nil {
		techTx.Rollback() //nolint:errcheck
		t.Fatalf("insert tech board: %v", err)
	}
	if err := techTx.Commit(); err != nil {
		t.Fatalf("commit tech board setup: %v", err)
	}
	const workFolderID = "favfld_work"
	if err := projections.CreateFavoriteFolder(c.DB, alice.ID, workFolderID, "", "Work", nil); err != nil {
		t.Fatalf("create work folder setup: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	missingBoardPayload := marshalCoreTestJSON(t, "marshal missing board move favorite payload", proto.MoveBoardFavoritePayload{Board: "missing", FolderID: workFolderID})
	missingBoard := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-move-favorite-missing-board",
		Command:    proto.CmdMoveBoardFavorite,
		Payload:    missingBoardPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missingBoard, proto.ErrNotFound, "missing board move favorite decision")

	missingFolderPayload := marshalCoreTestJSON(t, "marshal missing folder move favorite payload", proto.MoveBoardFavoritePayload{Board: "tech", FolderID: "missing"})
	missingFolder := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "tech"},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-move-favorite-missing-folder",
		Command:    proto.CmdMoveBoardFavorite,
		Payload:    missingFolderPayload,
		EnqueuedAt: 1334,
	})
	requireNativeDecisionTerminalError(t, missingFolder, proto.ErrNotFound, "missing folder move favorite decision")

	partition := LogPartition{Kind: partitionBoard, Key: "tech"}
	position := 0
	movePayload := marshalCoreTestJSON(t, "marshal move favorite payload", proto.MoveBoardFavoritePayload{Board: "tech", FolderID: workFolderID, Position: &position})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "move favorite", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-move-favorite",
		Command:    proto.CmdMoveBoardFavorite,
		Payload:    movePayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "move favorite events", proto.EvtBoardFavoriteSet)
	moveEvent := requireNativeDecisionPayload[proto.BoardFavoriteSetPayload](t, events[0], "move favorite payload")
	if moveEvent.UserID != alice.ID || moveEvent.Board != "tech" || !moveEvent.Favorite ||
		moveEvent.FolderID != workFolderID || moveEvent.Position == nil || *moveEvent.Position != 0 || moveEvent.TS != 2234 {
		t.Fatalf("move favorite event = %+v, want tech moved into work at position 0", moveEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-move-favorite-test", partition, 10, "materialize move favorite event")
	tree, err := c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatalf("list favorite tree after move: %v", err)
	}
	techFavorite := findFavoriteBoardEntryForTest(tree, "tech")
	if techFavorite == nil || techFavorite.FolderID != workFolderID || techFavorite.Position != 0 {
		t.Fatalf("tech favorite after move = %+v in tree %+v", techFavorite, tree)
	}

	rootPayload := marshalCoreTestJSON(t, "marshal move favorite root payload", proto.MoveBoardFavoritePayload{Board: "tech"})
	_, events = produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "move favorite root", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-move-favorite-root",
		Command:    proto.CmdMoveBoardFavorite,
		Payload:    rootPayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtBoardFavoriteSet {
		t.Fatalf("move favorite root events = %+v, want second board.favorite_set", events)
	}
	rootEvent := requireNativeDecisionPayload[proto.BoardFavoriteSetPayload](t, events[1], "move favorite root payload")
	if rootEvent.UserID != alice.ID || rootEvent.Board != "tech" || !rootEvent.Favorite ||
		rootEvent.FolderID != "" || rootEvent.Position != nil || rootEvent.TS != 3234 {
		t.Fatalf("move favorite root event = %+v, want tech moved to root", rootEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-move-favorite-test", partition, 10, "materialize move favorite root event")
	tree, err = c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatalf("list favorite tree after root move: %v", err)
	}
	techFavorite = findFavoriteBoardEntryForTest(tree, "tech")
	if techFavorite == nil || techFavorite.FolderID != "" {
		t.Fatalf("tech favorite after root move = %+v in tree %+v", techFavorite, tree)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsImportFavoriteTree(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")

	boardTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin board setup: %v", err)
	}
	for i, board := range []struct {
		id          string
		name        string
		description string
	}{
		{id: "tech", name: "Tech", description: "Technology discussion"},
		{id: "life", name: "Life", description: "Life discussion"},
		{id: "old", name: "Old", description: "Old favorite"},
	} {
		if err := projections.InsertBoard(boardTx, board.id, board.name, board.description, "", i+1); err != nil {
			boardTx.Rollback() //nolint:errcheck
			t.Fatalf("insert board %s: %v", board.id, err)
		}
	}
	if err := boardTx.Commit(); err != nil {
		t.Fatalf("commit board setup: %v", err)
	}
	const oldFolderID = "favfld_old"
	if err := projections.CreateFavoriteFolder(c.DB, alice.ID, oldFolderID, "", "Old", nil); err != nil {
		t.Fatalf("create old folder setup: %v", err)
	}
	if err := projections.SetBoardFavorite(c.DB, alice.ID, "old", oldFolderID, nil, true); err != nil {
		t.Fatalf("favorite old board setup: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	duplicatePayload := marshalCoreTestJSON(t, "marshal duplicate import payload", proto.ImportFavoriteTreePayload{
		Folders: []proto.ImportFavoriteFolderPayload{
			{ID: "src", Name: "One"},
			{ID: "src", Name: "Two"},
		},
	})
	duplicate := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-import-favorite-duplicate",
		Command:    proto.CmdImportFavoriteTree,
		Payload:    duplicatePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, duplicate, proto.ErrValidationFailed, "duplicate favorite import decision")

	cyclePayload := marshalCoreTestJSON(t, "marshal cycle import payload", proto.ImportFavoriteTreePayload{
		Folders: []proto.ImportFavoriteFolderPayload{
			{ID: "a", ParentID: "b", Name: "A"},
			{ID: "b", ParentID: "a", Name: "B"},
		},
	})
	cycle := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-import-favorite-cycle",
		Command:    proto.CmdImportFavoriteTree,
		Payload:    cyclePayload,
		EnqueuedAt: 1334,
	})
	requireNativeDecisionTerminalError(t, cycle, proto.ErrValidationFailed, "cycle favorite import decision")

	missingBoardPayload := marshalCoreTestJSON(t, "marshal missing board import payload", proto.ImportFavoriteTreePayload{
		Boards: []proto.ImportFavoriteBoardPayload{{ID: "missing"}},
	})
	missingBoard := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     3,
		ActorID:    alice.ID,
		CID:        "cid-native-import-favorite-missing-board",
		Command:    proto.CmdImportFavoriteTree,
		Payload:    missingBoardPayload,
		EnqueuedAt: 1434,
	})
	requireNativeDecisionTerminalError(t, missingBoard, proto.ErrValidationFailed, "missing board favorite import decision")

	replace := true
	importPayload := marshalCoreTestJSON(t, "marshal import favorite tree payload", proto.ImportFavoriteTreePayload{
		Replace: &replace,
		Folders: []proto.ImportFavoriteFolderPayload{
			{ID: "src_root", Name: " Work ", Position: 2},
			{ID: "src_child", ParentID: "src_root", Name: "Projects", Position: 0},
		},
		Boards: []proto.ImportFavoriteBoardPayload{
			{ID: "tech", FolderID: "src_child", Position: 3},
			{ID: "life", Position: 1},
			{ID: "tech", Position: 9},
		},
	})
	partition := LogPartition{Kind: partitionUser, Key: alice.ID}
	importRecord, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "import favorite tree", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-import-favorite-tree",
		Command:    proto.CmdImportFavoriteTree,
		Payload:    importPayload,
		EnqueuedAt: 2234,
	})
	rootID := stableCommandLogDecisionID("favfld_", importRecord, 0)
	childID := stableCommandLogDecisionID("favfld_", importRecord, 1)
	requireNativeDecisionEventKinds(t, events, "import favorite tree events", proto.EvtFavoriteTreeImported)
	importEvent := requireNativeDecisionPayload[proto.FavoriteTreeImportedPayload](t, events[0], "import favorite tree payload")
	if importEvent.UserID != alice.ID || !importEvent.Replace || importEvent.TS != 2234 {
		t.Fatalf("import favorite tree event = %+v, want alice replace import at ts 2234", importEvent)
	}
	if len(importEvent.Folders) != 2 ||
		importEvent.Folders[0].ID != rootID || importEvent.Folders[0].ParentID != "" ||
		importEvent.Folders[0].Name != "Work" || importEvent.Folders[0].Position != 2 ||
		importEvent.Folders[1].ID != childID || importEvent.Folders[1].ParentID != rootID ||
		importEvent.Folders[1].Name != "Projects" || importEvent.Folders[1].Position != 0 {
		t.Fatalf("import favorite tree folders = %+v, want deterministic mapped root/child", importEvent.Folders)
	}
	if len(importEvent.Boards) != 2 ||
		importEvent.Boards[0].ID != "tech" || importEvent.Boards[0].FolderID != childID || importEvent.Boards[0].Position != 3 ||
		importEvent.Boards[1].ID != "life" || importEvent.Boards[1].FolderID != "" || importEvent.Boards[1].Position != 1 {
		t.Fatalf("import favorite tree boards = %+v, want duplicate tech skipped and folders mapped", importEvent.Boards)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-import-favorite-tree-test", partition, 10, "materialize import favorite tree event")
	tree, err := c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatalf("list favorite tree after import: %v", err)
	}
	if folder := findFavoriteFolderForTest(tree, oldFolderID); folder != nil {
		t.Fatalf("old folder after replace import = %+v in tree %+v, want removed", folder, tree)
	}
	if board := findFavoriteBoardEntryForTest(tree, "old"); board != nil {
		t.Fatalf("old favorite after replace import = %+v in tree %+v, want removed", board, tree)
	}
	rootFolder := findFavoriteFolderForTest(tree, rootID)
	if rootFolder == nil || rootFolder.Name != "Work" || rootFolder.ParentID != "" || rootFolder.Position != 2 {
		t.Fatalf("root folder after import = %+v in tree %+v", rootFolder, tree)
	}
	childFolder := findFavoriteFolderForTest(tree, childID)
	if childFolder == nil || childFolder.Name != "Projects" || childFolder.ParentID != rootID || childFolder.Position != 0 {
		t.Fatalf("child folder after import = %+v in tree %+v", childFolder, tree)
	}
	techFavorite := findFavoriteBoardEntryForTest(tree, "tech")
	if techFavorite == nil || techFavorite.FolderID != childID || techFavorite.Position != 3 {
		t.Fatalf("tech favorite after import = %+v in tree %+v, want child folder position 3", techFavorite, tree)
	}
	lifeFavorite := findFavoriteBoardEntryForTest(tree, "life")
	if lifeFavorite == nil || lifeFavorite.FolderID != "" || lifeFavorite.Position != 1 {
		t.Fatalf("life favorite after import = %+v in tree %+v, want root position 1", lifeFavorite, tree)
	}

	retryReply := executor.ExecuteCommandLogRecord(ctx, importRecord)
	retryEvents, err := executor.DecideCommandLogEvents(ctx, importRecord, retryReply)
	if err != nil {
		t.Fatalf("retry import favorite tree events: %v", err)
	}
	if len(retryEvents) != 1 || retryEvents[0].ID != events[0].ID || retryEvents[0].Kind != proto.EvtFavoriteTreeImported {
		t.Fatalf("retry import favorite tree events = %+v, want same deterministic event", retryEvents)
	}
	retryEvent := requireNativeDecisionPayload[proto.FavoriteTreeImportedPayload](t, retryEvents[0], "retry import favorite tree payload")
	if len(retryEvent.Folders) != 2 || retryEvent.Folders[0].ID != rootID || retryEvent.Folders[1].ID != childID ||
		len(retryEvent.Boards) != 2 || retryEvent.Boards[0].FolderID != childID {
		t.Fatalf("retry import favorite tree payload = %+v, want same mapped folder ids", retryEvent)
	}
}

func findBoardSummaryForTest(summaries []projections.BoardSummary, boardID string) *projections.BoardSummary {
	for i := range summaries {
		if summaries[i].ID == boardID {
			return &summaries[i]
		}
	}
	return nil
}

func hasBoardForTest(boards []Board, boardID string) bool {
	for _, board := range boards {
		if board.ID == boardID {
			return true
		}
	}
	return false
}

func findFavoriteFolderForTest(tree *projections.FavoriteTree, folderID string) *projections.FavoriteFolder {
	if tree == nil {
		return nil
	}
	for i := range tree.Folders {
		if tree.Folders[i].ID == folderID {
			return &tree.Folders[i]
		}
	}
	return nil
}

func findFavoriteBoardEntryForTest(tree *projections.FavoriteTree, boardID string) *projections.FavoriteBoardEntry {
	if tree == nil {
		return nil
	}
	for i := range tree.Boards {
		if tree.Boards[i].ID == boardID {
			return &tree.Boards[i]
		}
	}
	return nil
}

func TestNativeCommandLogDecisionExecutorProjectsRepostPost(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	campusTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin campus board setup: %v", err)
	}
	if err := projections.InsertBoard(campusTx, "campus", "Campus", "Shared campus notes", "", 1); err != nil {
		campusTx.Rollback() //nolint:errcheck
		t.Fatalf("insert campus board: %v", err)
	}
	if err := campusTx.Commit(); err != nil {
		t.Fatalf("commit campus board setup: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload := marshalCoreTestJSON(t, "marshal source create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Original article",
		Body:  "source body",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "source create", CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-repost-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	})
	sourceEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, sourcePartition, 0, 10, "replay source events")
	if len(sourceEvents) != 2 {
		t.Fatalf("source events = %+v, want thread and post", sourceEvents)
	}
	sourceThreadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, sourceEvents[0], "source thread payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-repost-test", sourcePartition, 10, "materialize source events")
	sourcePosts, err := c.ListPosts(sourceThreadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}

	repostPartition := LogPartition{Kind: partitionBoard, Key: "campus"}
	repostPayload := marshalCoreTestJSON(t, "marshal repost payload", proto.RepostPostPayload{
		Post:  sourcePosts[0].ID,
		Board: "campus",
		Title: "Shared original article",
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "repost", 0, 10, CommandLogRecord{
		Partition:  repostPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-repost-post",
		Command:    proto.CmdRepostPost,
		Payload:    repostPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, events, "repost events", proto.EvtThreadNew, proto.EvtPostAppended)
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "repost thread payload")
	postPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "repost post payload")
	if threadPayload.Board != "campus" || threadPayload.AuthorID != bob.ID || threadPayload.Title != "Shared original article" {
		t.Fatalf("repost thread payload = %+v, want bob-authored campus thread", threadPayload)
	}
	if postPayload.SourcePost != sourcePosts[0].ID || postPayload.SourceThread != sourceThreadPayload.ID ||
		postPayload.SourceBoard != "general" || postPayload.SourceAuthor != "alice" || postPayload.SourceTitle != "Original article" {
		t.Fatalf("repost post payload lineage = %+v, want source lineage", postPayload)
	}
	if !strings.Contains(postPayload.Body, "Original post: "+sourcePosts[0].ID) || !strings.Contains(postPayload.Body, "source body") {
		t.Fatalf("repost post body = %q, want source body and original post id", postPayload.Body)
	}

	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-repost-test", repostPartition, 10, "materialize repost events")
	repostedPosts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list reposted posts: %v", err)
	}
	if len(repostedPosts) != 1 {
		t.Fatalf("reposted posts = %+v, want one", repostedPosts)
	}
	got := repostedPosts[0]
	if got.Author != "bob" || got.SourcePost != sourcePosts[0].ID || got.SourceThread != sourceThreadPayload.ID ||
		got.SourceBoard != "general" || got.SourceAuthor != "alice" || got.SourceTitle != "Original article" {
		t.Fatalf("materialized repost lineage = %+v, want source lineage", got)
	}

	secretTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin secret board setup: %v", err)
	}
	if err := projections.InsertBoard(secretTx, "secret", "Secret", "Members only", "", 2); err != nil {
		secretTx.Rollback() //nolint:errcheck
		t.Fatalf("insert secret board: %v", err)
	}
	if err := secretTx.Commit(); err != nil {
		t.Fatalf("commit secret board setup: %v", err)
	}
	memberRead := true
	if err := projections.SetBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read mode: %v", err)
	}
	secretPayload := marshalCoreTestJSON(t, "marshal private source create payload", proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private article",
		Body:  "hidden source",
	})
	secretPartition := LogPartition{Kind: partitionBoard, Key: "secret"}
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "private source create", CommandLogRecord{
		Partition:  secretPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-repost-private-source-create",
		Command:    proto.CmdCreateThread,
		Payload:    secretPayload,
		EnqueuedAt: 3234,
	})
	secretEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, secretPartition, 0, 10, "replay private source events")
	if len(secretEvents) != 2 {
		t.Fatalf("private source events = %+v, want thread and post", secretEvents)
	}
	secretThreadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, secretEvents[0], "private source thread payload")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-repost-test", secretPartition, 10, "materialize private source events")
	secretPosts, err := c.ListPosts(secretThreadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list private source posts: %v", err)
	}
	if len(secretPosts) != 1 {
		t.Fatalf("private source posts = %+v, want one", secretPosts)
	}
	privateRepostPayload := marshalCoreTestJSON(t, "marshal private repost payload", proto.RepostPostPayload{
		Post:  secretPosts[0].ID,
		Board: "campus",
	})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  repostPartition,
		Offset:     2,
		ActorID:    bob.ID,
		CID:        "cid-native-repost-private-source",
		Command:    proto.CmdRepostPost,
		Payload:    privateRepostPayload,
		EnqueuedAt: 3334,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "private source repost denied")
}

func TestNativeCommandLogDecisionExecutorProjectsRoleChanges(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	userPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	grantPayload := marshalCoreTestJSON(t, "marshal grant payload", proto.GrantRolePayload{User: alice.ID, Role: "moderator"})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  userPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-grant-role-denied",
		Command:    proto.CmdGrantRole,
		Payload:    grantPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "grant role as non-admin")
	missingPayload := marshalCoreTestJSON(t, "marshal missing grant payload", proto.GrantRolePayload{User: "usr_missing", Role: "moderator"})
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "usr_missing"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-grant-role-missing",
		Command:    proto.CmdGrantRole,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, missing, proto.ErrNotFound, "grant missing user")

	_, accountEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "grant role", 0, 10, CommandLogRecord{
		Partition:  userPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-grant-role",
		Command:    proto.CmdGrantRole,
		Payload:    grantPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionEventKinds(t, accountEvents, "account role events", proto.EvtRoleGranted)
	grantEvent := requireNativeDecisionPayload[proto.RoleGrantedPayload](t, accountEvents[0], "grant payload")
	if grantEvent.User != alice.ID || grantEvent.Role != "moderator" || grantEvent.By != admin.ID || grantEvent.TS != 1234 {
		t.Fatalf("grant event = %+v, want deterministic role grant", grantEvent)
	}
	syssecurityPartition := LogPartition{Kind: partitionBoard, Key: proto.SyssecuritySystemBoardID}
	syssecurityEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, syssecurityPartition, 0, 10, "replay syssecurity grant events")
	if len(syssecurityEvents) != 3 ||
		syssecurityEvents[0].Kind != proto.EvtBoardCreated ||
		syssecurityEvents[1].Kind != proto.EvtThreadNew ||
		syssecurityEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("syssecurity grant events = %+v, want board/thread/post audit events", syssecurityEvents)
	}
	grantAuditPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, syssecurityEvents[2], "grant audit payload")
	for _, want := range []string{"Action: role granted", "User: alice", "Role: moderator", "Actor: admin"} {
		if !strings.Contains(grantAuditPost.Body, want) {
			t.Fatalf("grant audit body missing %q:\n%s", want, grantAuditPost.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-role-change-test", userPartition, 10, "materialize grant account event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-role-change-test", syssecurityPartition, 10, "materialize grant syssecurity events")
	aliceUser, err := projections.GetUserByID(c.DB, alice.ID)
	if err != nil {
		t.Fatalf("get alice after grant: %v", err)
	}
	if aliceUser == nil || aliceUser.Role != "moderator" {
		t.Fatalf("alice after grant = %+v, want moderator role", aliceUser)
	}
	syssecurityThreads, err := c.ListThreads(proto.SyssecuritySystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list syssecurity after grant: %v", err)
	}
	if len(syssecurityThreads) != 1 {
		t.Fatalf("syssecurity threads after grant = %+v, want one audit thread", syssecurityThreads)
	}

	revokePayload := marshalCoreTestJSON(t, "marshal revoke payload", proto.RevokeRolePayload{User: alice.ID, Role: "moderator"})
	_, accountEvents = produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "revoke role", 0, 10, CommandLogRecord{
		Partition:  userPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-revoke-role",
		Command:    proto.CmdRevokeRole,
		Payload:    revokePayload,
		EnqueuedAt: 2234,
	})
	if len(accountEvents) != 2 || accountEvents[1].Kind != proto.EvtRoleRevoked {
		t.Fatalf("account role events after revoke = %+v, want role.granted then role.revoked", accountEvents)
	}
	revokeEvent := requireNativeDecisionPayload[proto.RoleRevokedPayload](t, accountEvents[1], "revoke payload")
	if revokeEvent.User != alice.ID || revokeEvent.Role != "moderator" || revokeEvent.By != admin.ID || revokeEvent.TS != 2234 {
		t.Fatalf("revoke event = %+v, want deterministic role revoke", revokeEvent)
	}
	syssecurityEvents = replayCommandLogWorkerPartition(t, ctx, eventStore, syssecurityPartition, 0, 10, "replay syssecurity events after revoke")
	if len(syssecurityEvents) != 5 ||
		syssecurityEvents[3].Kind != proto.EvtThreadNew ||
		syssecurityEvents[4].Kind != proto.EvtPostAppended {
		t.Fatalf("syssecurity events after revoke = %+v, want grant board/thread/post then revoke thread/post", syssecurityEvents)
	}
	revokeAuditPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, syssecurityEvents[4], "revoke audit payload")
	for _, want := range []string{"Action: role revoked", "User: alice", "Role: moderator", "Actor: admin"} {
		if !strings.Contains(revokeAuditPost.Body, want) {
			t.Fatalf("revoke audit body missing %q:\n%s", want, revokeAuditPost.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-role-change-test", userPartition, 10, "materialize revoke account event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-role-change-test", syssecurityPartition, 10, "materialize revoke syssecurity events")
	aliceUser, err = projections.GetUserByID(c.DB, alice.ID)
	if err != nil {
		t.Fatalf("get alice after revoke: %v", err)
	}
	if aliceUser == nil || aliceUser.Role != "user" {
		t.Fatalf("alice after revoke = %+v, want user role", aliceUser)
	}
	syssecurityThreads, err = c.ListThreads(proto.SyssecuritySystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list syssecurity after revoke: %v", err)
	}
	if len(syssecurityThreads) != 2 {
		t.Fatalf("syssecurity threads after revoke = %+v, want two audit threads", syssecurityThreads)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsPublishStatsSnapshot(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	commandPartition := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}
	statsPayload := marshalCoreTestJSON(t, "marshal stats snapshot payload", proto.PublishStatsSnapshotPayload{Date: "2026-06-04"})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-stats-denied",
		Command:    proto.CmdPublishStatsSnapshot,
		Payload:    statsPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "publish stats as non-admin")
	invalidPayload := marshalCoreTestJSON(t, "marshal invalid stats snapshot payload", proto.PublishStatsSnapshotPayload{Date: "06/04/2026"})
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     2,
		ActorID:    admin.ID,
		CID:        "cid-native-stats-invalid",
		Command:    proto.CmdPublishStatsSnapshot,
		Payload:    invalidPayload,
		EnqueuedAt: 1334,
	})
	requireNativeDecisionTerminalError(t, invalid, proto.ErrValidationFailed, "publish stats invalid date", statsSnapshotDateValidationMessage("06/04/2026", 1334))

	globalPartition := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}
	statsRecord, globalEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "stats snapshot", globalPartition, 0, 10, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-stats-snapshot",
		Command:    proto.CmdPublishStatsSnapshot,
		Payload:    statsPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionEventKinds(t, globalEvents, "stats global events", proto.EvtCommunityStatsSnapshotRecorded)
	snapshotEvent := requireNativeDecisionPayload[proto.CommunityStatsSnapshotRecordedPayload](t, globalEvents[0], "stats global payload")
	if snapshotEvent.Day != "2026-06-04" || snapshotEvent.SnapshotAt != 2234 || snapshotEvent.TotalUsers != 2 {
		t.Fatalf("stats snapshot event = %+v, want 2026-06-04 current stats", snapshotEvent)
	}

	boardEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, LogPartition{Kind: partitionBoard, Key: "BBSLists"}, 0, 100, "replay BBSLists events")
	if len(boardEvents) != 29 {
		t.Fatalf("BBSLists event count = %d, want board create plus 14 thread/post pairs: %+v", len(boardEvents), boardEvents)
	}
	if boardEvents[0].Kind != proto.EvtBoardCreated {
		t.Fatalf("first BBSLists event = %s, want board.created", boardEvents[0].Kind)
	}
	threadEvents := 0
	postEvents := 0
	for _, event := range boardEvents {
		switch event.Kind {
		case proto.EvtThreadNew:
			threadEvents++
		case proto.EvtPostAppended:
			postEvents++
		}
	}
	if threadEvents != 14 || postEvents != 14 {
		t.Fatalf("BBSLists thread/post events = %d/%d, want 14/14", threadEvents, postEvents)
	}

	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-stats-snapshot-test", commandPartition, 10, "materialize stats global event")
	statsBoardPartition := LogPartition{Kind: partitionBoard, Key: "BBSLists"}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-stats-snapshot-test", statsBoardPartition, 100, "materialize BBSLists events")
	history, err := c.ListCommunityStatHistory(5, 0)
	if err != nil {
		t.Fatalf("list stat history after native snapshot: %v", err)
	}
	if len(history) == 0 || history[0].Day != "2026-06-04" || history[0].TotalUsers != 2 {
		t.Fatalf("stat history after native snapshot = %+v, want 2026-06-04 row", history)
	}
	board, err := c.GetBoard("BBSLists")
	if err != nil {
		t.Fatalf("get BBSLists board: %v", err)
	}
	if board == nil || board.Name != "BBSLists" {
		t.Fatalf("BBSLists board = %+v, want generated board", board)
	}
	threads, err := c.ListThreads("BBSLists", 20, 0)
	if err != nil {
		t.Fatalf("list BBSLists threads: %v", err)
	}
	if len(threads) != 14 {
		t.Fatalf("BBSLists threads = %+v, want 14 daily stats threads", threads)
	}
	wantThreads := map[string]string{
		"bbslists_stats_20260604":       "Community stats 2026-06-04",
		"bbslists_countlogins_20260604": "Login count history 2026-06-04",
		"bbslists_statguy_20260604":     "User activity rankings 2026-06-04",
		"bbslists_bonline_20260604":     "Board online occupancy 2026-06-04",
		"bbslists_uonline_20260604":     "Online user roster 2026-06-04",
		"bbslists_statbm_20260604":      "Board moderator activity 2026-06-04",
		"bbslists_bms_20260604":         "Board moderator tenure history 2026-06-04",
		"bbslists_boardlog_20260604":    "Board activity history 2026-06-04",
		"bbslists_boardrank_20260604":   "Board popularity list 2026-06-04",
		"bbslists_newboards_20260604":   "New board list 2026-06-04",
		"bbslists_rcmdbrd_20260604":     "Recommended board list 2026-06-04",
		"bbslists_commend_20260604":     "Recommended article list 2026-06-04",
		"bbslists_toplog_20260604":      "Hot topic history 2026-06-04",
		"bbslists_bless_20260604":       "Daily blessing list 2026-06-04",
	}
	for _, thread := range threads {
		delete(wantThreads, thread.ID)
	}
	if len(wantThreads) != 0 {
		t.Fatalf("missing BBSLists threads after native snapshot: %+v", wantThreads)
	}
	posts, err := c.ListPosts("bbslists_stats_20260604", 10, 0)
	if err != nil {
		t.Fatalf("list native stats snapshot post: %v", err)
	}
	if len(posts) != 1 || !strings.Contains(posts[0].Body, "Community stats 2026-06-04") ||
		!strings.Contains(posts[0].Body, "Total users: 2") {
		t.Fatalf("native stats snapshot post = %+v, want community stats body", posts)
	}

	retryReply := executor.ExecuteCommandLogRecord(ctx, statsRecord)
	if retryReply.Err != nil || retryReply.Result == nil || retryReply.Result.ID != "bbslists_stats_20260604" || retryReply.Result.Seq <= 0 {
		t.Fatalf("retry stats snapshot reply = %+v, want existing stats thread ack", retryReply)
	}
	retryEvents, err := executor.DecideCommandLogEvents(ctx, statsRecord, retryReply)
	if err != nil {
		t.Fatalf("retry stats snapshot events: %v", err)
	}
	if len(retryEvents) != 1 || retryEvents[0].ID != globalEvents[0].ID || retryEvents[0].Kind != proto.EvtCommunityStatsSnapshotRecorded {
		t.Fatalf("retry stats snapshot events = %+v, want same snapshot-recorded event only", retryEvents)
	}

	weeklyPayload := marshalCoreTestJSON(t, "marshal weekly stats snapshot payload", proto.PublishStatsSnapshotPayload{Date: "2026-06-07"})
	_, weeklyBoardEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "weekly stats snapshot", statsBoardPartition, 29, 100, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-stats-snapshot-weekly",
		Command:    proto.CmdPublishStatsSnapshot,
		Payload:    weeklyPayload,
		EnqueuedAt: 3234,
	})
	if len(weeklyBoardEvents) != 32 {
		t.Fatalf("weekly BBSLists event count = %d, want 16 thread/post pairs: %+v", len(weeklyBoardEvents), weeklyBoardEvents)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-stats-snapshot-test", commandPartition, 10, "materialize weekly stats global event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-stats-snapshot-test", statsBoardPartition, 100, "materialize weekly BBSLists events")
	threads, err = c.ListThreads("BBSLists", 40, 0)
	if err != nil {
		t.Fatalf("list BBSLists threads after weekly snapshot: %v", err)
	}
	weeklyThreads := map[string]bool{
		"bbslists_week_2026w23":        false,
		"bbslists_toplog_week_2026w23": false,
	}
	for _, thread := range threads {
		if _, ok := weeklyThreads[thread.ID]; ok {
			weeklyThreads[thread.ID] = true
		}
	}
	for id, found := range weeklyThreads {
		if !found {
			t.Fatalf("missing weekly BBSLists thread %s after native Sunday snapshot; threads=%+v", id, threads)
		}
	}
}

func TestNativeCommandLogDecisionExecutorProjectsSystemNotices(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	alice := registerNativeDecisionTestUser(t, c, "alice")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	defaultCommandPartition := LogPartition{Kind: partitionBoard, Key: partitionGlobal}
	noticePayload := marshalCoreTestJSON(t, "marshal notice payload", proto.PublishSystemNoticePayload{
		Title:  "Campus notice",
		Body:   "Maintenance tonight at 23:00.",
		Source: "operator broadcast",
	})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  defaultCommandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-system-notice-denied",
		Command:    proto.CmdPublishSystemNotice,
		Payload:    noticePayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "publish system notice as non-admin")
	invalidPayload := marshalCoreTestJSON(t, "marshal invalid notice payload", proto.PublishSystemNoticePayload{
		Board: "Filter",
		Title: "Filtered",
		Body:  "not a public notice board",
	})
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "Filter"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-system-notice-invalid",
		Command:    proto.CmdPublishSystemNotice,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, invalid, proto.ErrValidationFailed, "publish system notice to invalid board")

	notepadPartition := LogPartition{Kind: partitionBoard, Key: "notepad"}
	_, notepadEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "system notice", notepadPartition, 0, 10, CommandLogRecord{
		Partition:  defaultCommandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-system-notice",
		Command:    proto.CmdPublishSystemNotice,
		Payload:    noticePayload,
		EnqueuedAt: 1234,
	})
	if len(notepadEvents) != 3 ||
		notepadEvents[0].Kind != proto.EvtBoardCreated ||
		notepadEvents[1].Kind != proto.EvtThreadNew ||
		notepadEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("notepad notice events = %+v, want board/thread/post", notepadEvents)
	}
	threadEvent := requireNativeDecisionPayload[proto.ThreadNewPayload](t, notepadEvents[1], "notice thread payload")
	postEvent := requireNativeDecisionPayload[proto.PostAppendedPayload](t, notepadEvents[2], "notice post payload")
	if threadEvent.Board != "notepad" || threadEvent.Title != "Campus notice" ||
		postEvent.Thread != threadEvent.ID || postEvent.AuthorID != admin.ID {
		t.Fatalf("notice thread/post payloads = %+v / %+v, want deterministic notepad notice", threadEvent, postEvent)
	}
	for _, want := range []string{"# Campus notice", "Notice board: notepad", "Actor: admin", "Source: operator broadcast", "Maintenance tonight at 23:00.", "Generated public system notice"} {
		if !strings.Contains(postEvent.Body, want) {
			t.Fatalf("notice body missing %q:\n%s", want, postEvent.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-system-notice-test", notepadPartition, 10, "materialize notepad notice events")
	notepad, err := c.GetBoard("notepad")
	if err != nil {
		t.Fatalf("get notepad board: %v", err)
	}
	if notepad == nil || notepad.Name != "notepad" {
		t.Fatalf("notepad board = %+v, want generated notepad board", notepad)
	}
	posts, err := c.ListPosts(threadEvent.ID, 10, 0)
	if err != nil {
		t.Fatalf("list notice posts: %v", err)
	}
	if len(posts) != 1 || posts[0].Body != postEvent.Body {
		t.Fatalf("notice posts = %+v, want materialized notice post", posts)
	}

	secondPayload := marshalCoreTestJSON(t, "marshal second notice payload", proto.PublishSystemNoticePayload{
		Board: "notepad",
		Title: "Second notice",
		Body:  "Another maintenance window.",
	})
	secondCommandPartition := LogPartition{Kind: partitionBoard, Key: "notepad"}
	_, notepadEvents = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "second system notice", notepadPartition, 0, 10, CommandLogRecord{
		Partition:  secondCommandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-system-notice-second",
		Command:    proto.CmdPublishSystemNotice,
		Payload:    secondPayload,
		EnqueuedAt: 2234,
	})
	if len(notepadEvents) != 5 ||
		notepadEvents[3].Kind != proto.EvtThreadNew ||
		notepadEvents[4].Kind != proto.EvtPostAppended {
		t.Fatalf("notepad events after second notice = %+v, want original board/thread/post plus second thread/post", notepadEvents)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-system-notice-test", notepadPartition, 10, "materialize second notepad notice events")
	notepadThreads, err := c.ListThreads("notepad", 10, 0)
	if err != nil {
		t.Fatalf("list notepad threads after second notice: %v", err)
	}
	if len(notepadThreads) != 2 {
		t.Fatalf("notepad threads after second notice = %+v, want two notices", notepadThreads)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsModerationReviews(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	moderationPartition := LogPartition{Kind: partitionBoard, Key: proto.ModerationSystemBoardID}
	globalPartition := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Native moderation review",
		Body:  "public body should stay out of logs",
	})
	_, boardEvents := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-moderation-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-moderation-review-test", boardPartition, 10, "materialize create events")
	if len(boardEvents) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", boardEvents)
	}
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, boardEvents[1], "root post payload")

	flagPayload := marshalCoreTestJSON(t, "marshal flag payload", proto.FlagPostPayload{
		Post:   rootPostPayload.ID,
		Reason: "sensitive report reason",
	})
	postPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "flag", CommandLogRecord{
		Partition:  postPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-flag-post",
		Command:    proto.CmdFlagPost,
		Payload:    flagPayload,
		EnqueuedAt: 2234,
	})
	// The flag event carries the reporter + reason, so it must NOT land on the
	// board partition (delivering/replaying it to board subscribers would leak
	// the reporter's identity — M8). The board partition keeps only the public
	// thread/post events; the flag event lands on the moderation/global partition.
	boardEvents = replayCommandLogWorkerPartition(t, ctx, eventStore, boardPartition, 0, 10, "replay board events")
	for _, e := range boardEvents {
		if e.Kind == proto.EvtPostFlagged {
			t.Fatalf("post.flagged must not appear on the board partition (reporter leak): %+v", boardEvents)
		}
	}
	globalEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, globalPartition, 0, 10, "replay global events")
	var flagEvent *proto.PostFlaggedPayload
	for _, e := range globalEvents {
		if e.Kind != proto.EvtPostFlagged {
			continue
		}
		p := requireNativeDecisionPayload[proto.PostFlaggedPayload](t, e, "flag payload")
		flagEvent = p
	}
	if flagEvent == nil {
		t.Fatalf("post.flagged not found on the moderation/global partition; global=%+v", globalEvents)
	}
	if flagEvent.Kind != "post_flag" || flagEvent.PostID != rootPostPayload.ID || flagEvent.Thread != rootPostPayload.Thread ||
		flagEvent.Reporter != bob.ID || flagEvent.Reason != "sensitive report reason" || flagEvent.TS != 2234 {
		t.Fatalf("flag event = %+v, want deterministic post flag review", flagEvent)
	}
	moderationEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, moderationPartition, 0, 10, "replay generated moderation events")
	if len(moderationEvents) != 3 ||
		moderationEvents[0].Kind != proto.EvtBoardCreated ||
		moderationEvents[1].Kind != proto.EvtThreadNew ||
		moderationEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("generated moderation events = %+v, want board/thread/post log events", moderationEvents)
	}
	flagLogPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, moderationEvents[2], "flag log payload")
	for _, want := range []string{"Status: opened", "Board: general", "Thread: " + rootPostPayload.Thread, "Post: " + rootPostPayload.ID, "Actor: bob"} {
		if !strings.Contains(flagLogPost.Body, want) {
			t.Fatalf("generated flag log missing %q:\n%s", want, flagLogPost.Body)
		}
	}
	for _, secret := range []string{"sensitive report reason", "public body should stay out of logs"} {
		if strings.Contains(flagLogPost.Body, secret) {
			t.Fatalf("generated flag log leaked %q:\n%s", secret, flagLogPost.Body)
		}
	}
	// post.flagged is scoped moderation-only, so it lands on the global
	// partition (M8) — materialize it there to build the review.
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-moderation-review-test", globalPartition, 10, "materialize flag event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-moderation-review-test", moderationPartition, 10, "materialize generated flag log")
	reviews, err := c.ListModerationReviews("open", 10, 0)
	if err != nil {
		t.Fatalf("list open reviews: %v", err)
	}
	if len(reviews) != 1 || reviews[0].ID != flagEvent.ReviewID || reviews[0].Kind != "post_flag" ||
		reviews[0].TargetID != rootPostPayload.ID || reviews[0].Reporter != bob.ID ||
		reviews[0].Reason != "sensitive report reason" {
		t.Fatalf("open reviews after native flag = %+v, want projected post flag review", reviews)
	}
	moderationThreads, err := c.ListThreads(proto.ModerationSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list moderation threads after flag: %v", err)
	}
	if len(moderationThreads) != 1 || moderationThreads[0].ID != "mod_flag_thr_"+flagEvent.ReviewID {
		t.Fatalf("moderation threads after flag = %+v, want generated flag thread", moderationThreads)
	}

	resolvePayload := marshalCoreTestJSON(t, "marshal resolve payload", proto.ResolveReviewPayload{
		Review:     flagEvent.ReviewID,
		Resolution: "private moderator note",
	})
	resolveDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionReview, Key: flagEvent.ReviewID},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-resolve-review-denied",
		Command:    proto.CmdResolveReview,
		Payload:    resolvePayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, resolveDenied, proto.ErrForbidden, "resolve reply without permission")
	reviewPartition := LogPartition{Kind: partitionReview, Key: flagEvent.ReviewID}
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "resolve", CommandLogRecord{
		Partition:  reviewPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-resolve-review",
		Command:    proto.CmdResolveReview,
		Payload:    resolvePayload,
		EnqueuedAt: 3234,
	})
	globalEvents = replayCommandLogWorkerPartition(t, ctx, eventStore, globalPartition, 0, 10, "replay resolve events")
	// The moderation/global partition now holds both the earlier post.flagged
	// and this review.resolved (flag events are scoped moderation-only — M8).
	var resolveEvent *proto.ReviewResolvedPayload
	for _, e := range globalEvents {
		if e.Kind != proto.EvtReviewResolved {
			continue
		}
		p := requireNativeDecisionPayload[proto.ReviewResolvedPayload](t, e, "resolve payload")
		resolveEvent = p
	}
	if resolveEvent == nil {
		t.Fatalf("review.resolved not found on the moderation/global partition; global=%+v", globalEvents)
	}
	if resolveEvent.ReviewID != flagEvent.ReviewID || resolveEvent.Resolution != "private moderator note" || resolveEvent.By != admin.ID || resolveEvent.TS != 3234 {
		t.Fatalf("resolve event = %+v, want deterministic review resolution", resolveEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-moderation-review-test", globalPartition, 10, "materialize resolve event")
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-moderation-review-test", moderationPartition, 10, "materialize generated resolve log")
	resolvedReviews, err := c.ListModerationReviews("resolved", 10, 0)
	if err != nil {
		t.Fatalf("list resolved reviews: %v", err)
	}
	if len(resolvedReviews) != 1 || resolvedReviews[0].ID != flagEvent.ReviewID ||
		resolvedReviews[0].Actor != admin.ID || resolvedReviews[0].Resolution != "private moderator note" {
		t.Fatalf("resolved reviews = %+v, want projected native resolution", resolvedReviews)
	}
	moderationThreads, err = c.ListThreads(proto.ModerationSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list moderation threads after resolve: %v", err)
	}
	if len(moderationThreads) != 2 {
		t.Fatalf("moderation threads after resolve = %+v, want flag and resolution log threads", moderationThreads)
	}
	resolvePosts, err := c.ListPosts("mod_resolve_thr_"+flagEvent.ReviewID, 10, 0)
	if err != nil {
		t.Fatalf("list generated resolve posts: %v", err)
	}
	if len(resolvePosts) != 1 || !strings.Contains(resolvePosts[0].Body, "Status: resolved") {
		t.Fatalf("generated resolve posts = %+v, want sanitized resolution log", resolvePosts)
	}
	if strings.Contains(resolvePosts[0].Body, "private moderator note") {
		t.Fatalf("generated resolve log leaked moderator note:\n%s", resolvePosts[0].Body)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsPollResultRecords(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")
	if err := setNativeDecisionTestTrustLevel(c, alice.ID, 2); err != nil {
		t.Fatalf("set alice trust level: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Native vote result poll",
		Body:  "[poll]\nBest option?\nOption A\nOption B\n[/poll]",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "poll create", CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-poll-result-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-poll-result-test", boardPartition, 10, "materialize poll create events")
	boardEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, boardPartition, 0, 10, "replay poll create events")
	if len(boardEvents) != 2 || boardEvents[1].Kind != proto.EvtPostAppended {
		t.Fatalf("poll create events = %+v, want thread.new and post.appended", boardEvents)
	}
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, boardEvents[1], "root poll post payload")
	rootPoll, err := c.GetPollByPostID(rootPostPayload.ID)
	if err != nil {
		t.Fatalf("get root poll: %v", err)
	}
	if rootPoll == nil {
		t.Fatal("expected native-created poll to materialize")
	}
	fullPoll, err := c.GetPoll(rootPoll.ID, bob.ID)
	if err != nil {
		t.Fatalf("get full poll: %v", err)
	}
	if len(fullPoll.Options) != 2 {
		t.Fatalf("poll options = %+v, want two options", fullPoll.Options)
	}
	voteMutation, err := c.counterStore.BeginMutation()
	if err != nil {
		t.Fatalf("begin vote mutation: %v", err)
	}
	defer voteMutation.Rollback() //nolint:errcheck
	if err := voteMutation.CastVote(rootPoll.ID, fullPoll.Options[0].ID, bob.ID, 1800); err != nil {
		t.Fatalf("cast vote: %v", err)
	}
	if err := voteMutation.Commit(); err != nil {
		t.Fatalf("commit vote mutation: %v", err)
	}

	publishPayload := marshalCoreTestJSON(t, "marshal publish payload", proto.PublishPollResultPayload{Poll: rootPoll.ID})
	pollPartition := LogPartition{Kind: partitionPoll, Key: rootPoll.ID}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  pollPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-poll-result-denied",
		Command:    proto.CmdPublishPollResult,
		Payload:    publishPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "publish reply without permission")
	canManagePolls := true
	if err := projections.SetBoardMember(c.DB, "general", carol.ID, true, BoardMemberPatch{CanManagePolls: &canManagePolls}); err != nil {
		t.Fatalf("set carol poll manager: %v", err)
	}
	votePartition := LogPartition{Kind: partitionBoard, Key: proto.VoteSystemBoardID}
	_, voteEvents := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "publish poll result", votePartition, 0, 10, CommandLogRecord{
		Partition:  pollPartition,
		ActorID:    carol.ID,
		CID:        "cid-native-poll-result-publish",
		Command:    proto.CmdPublishPollResult,
		Payload:    publishPayload,
		EnqueuedAt: 2234,
	})
	if len(voteEvents) != 3 ||
		voteEvents[0].Kind != proto.EvtBoardCreated ||
		voteEvents[1].Kind != proto.EvtThreadNew ||
		voteEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("generated vote events = %+v, want board/thread/post result events", voteEvents)
	}
	resultThread := requireNativeDecisionPayload[proto.ThreadNewPayload](t, voteEvents[1], "vote thread payload")
	resultPost := requireNativeDecisionPayload[proto.PostAppendedPayload](t, voteEvents[2], "vote post payload")
	expectedThreadID, expectedPostID := proto.PollResultPostIDs(rootPoll.ID)
	if resultThread.ID != expectedThreadID || resultThread.Board != proto.VoteSystemBoardID ||
		!strings.Contains(resultThread.Title, "Best option?") || resultPost.Thread != resultThread.ID ||
		resultPost.ID != expectedPostID {
		t.Fatalf("result thread/post payloads = %+v / %+v, want deterministic vote result records", resultThread, resultPost)
	}
	for _, want := range []string{"# Poll result: Best option?", "Source thread: Native vote result poll", "Source board: general", "Total votes: 1", "Option A: 1 vote", "Option B: 0 vote", "Generated public poll result"} {
		if !strings.Contains(resultPost.Body, want) {
			t.Fatalf("generated vote result body missing %q:\n%s", want, resultPost.Body)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-poll-result-test", votePartition, 10, "materialize generated vote events")
	voteBoard, err := c.GetBoard(proto.VoteSystemBoardID)
	if err != nil {
		t.Fatalf("get vote board: %v", err)
	}
	if voteBoard == nil || voteBoard.Name != proto.VoteSystemBoardName {
		t.Fatalf("vote board = %+v, want generated vote board", voteBoard)
	}
	resultPosts, err := c.ListPosts(resultThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list vote result posts: %v", err)
	}
	if len(resultPosts) != 1 || resultPosts[0].Body != resultPost.Body {
		t.Fatalf("result posts = %+v, want materialized generated result post", resultPosts)
	}

	_, voteEvents = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "duplicate publish poll result", votePartition, 0, 10, CommandLogRecord{
		Partition:  pollPartition,
		ActorID:    carol.ID,
		CID:        "cid-native-poll-result-duplicate",
		Command:    proto.CmdPublishPollResult,
		Payload:    publishPayload,
		EnqueuedAt: 3234,
	})
	if len(voteEvents) != 3 {
		t.Fatalf("generated vote events after duplicate = %+v, want no duplicate events", voteEvents)
	}
	voteThreads, err := c.ListThreads(proto.VoteSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list vote threads after duplicate: %v", err)
	}
	if len(voteThreads) != 1 || voteThreads[0].ID != resultThread.ID {
		t.Fatalf("vote threads after duplicate = %+v, want single result thread", voteThreads)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsRedactAndRestorePost(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native redaction",
		Body:  "redactsearchtoken visible body",
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-redact-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-redact-restore-test", partition, 10, "materialize create events")
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root post payload")
	postPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}
	if search, err := c.SearchReadablePosts(alice, "redactsearchtoken", "", 10); err != nil || len(search) != 1 {
		t.Fatalf("search before redact = %+v, %v; want visible post", search, err)
	}

	redactPayload := marshalCoreTestJSON(t, "marshal redact payload", proto.RedactPostPayload{
		Post:   rootPostPayload.ID,
		Reason: "author cleanup",
	})
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-redact-forbidden",
		Command:    proto.CmdRedactPost,
		Payload:    redactPayload,
		EnqueuedAt: 2234,
	})
	requireNativeDecisionTerminalError(t, forbidden, proto.ErrForbidden, "forbidden redact reply")
	redactRecord, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "redact", partition, 0, 10, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-redact-author",
		Command:    proto.CmdRedactPost,
		Payload:    redactPayload,
		EnqueuedAt: 2234,
	})
	if len(events) != 3 {
		t.Fatalf("events after redact = %+v, want post.redacted", events)
	}
	redactEvent := requireNativeDecisionPayload[proto.PostRedactedPayload](t, events[2], "redact event payload")
	if redactEvent.ID != rootPostPayload.ID || redactEvent.Thread != rootPostPayload.Thread ||
		redactEvent.By != alice.ID || redactEvent.Reason != "author cleanup" || redactEvent.TS != 2234 {
		t.Fatalf("redact event = %+v, want deterministic author redaction", redactEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-redact-restore-test", partition, 10, "materialize redact event")
	post, err := c.GetPost(rootPostPayload.ID)
	if err != nil {
		t.Fatalf("get post after redact: %v", err)
	}
	if post == nil || !post.Redacted || post.UpdatedAt != 2234 {
		t.Fatalf("post after redact = %+v, want redacted post", post)
	}
	if search, err := c.SearchReadablePosts(alice, "redactsearchtoken", "", 10); err != nil || len(search) != 0 {
		t.Fatalf("search after redact = %+v, %v; want no visible post", search, err)
	}
	deleted, err := c.ListBoardDeletedPosts("general", "junk", 10, 0)
	if err != nil {
		t.Fatalf("list junk deleted posts: %v", err)
	}
	if len(deleted) != 1 || deleted[0].PostID != rootPostPayload.ID || deleted[0].DeletedByID != alice.ID ||
		deleted[0].DeletedByName != alice.Name || deleted[0].Reason != "author cleanup" || deleted[0].Kind != "junk" {
		t.Fatalf("junk deleted posts = %+v, want author deletion row", deleted)
	}

	restorePayload := marshalCoreTestJSON(t, "marshal restore payload", proto.RestorePostPayload{Post: rootPostPayload.ID})
	restoreDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     redactRecord.Offset + 1,
		ActorID:    bob.ID,
		CID:        "cid-native-restore-forbidden",
		Command:    proto.CmdRestorePost,
		Payload:    restorePayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, restoreDenied, proto.ErrForbidden, "restore denied reply")
	canModeratePosts := true
	if err := projections.SetBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{CanModeratePosts: &canModeratePosts}); err != nil {
		t.Fatalf("grant bob post moderation: %v", err)
	}
	_, events = produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "restore", partition, 0, 10, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-restore",
		Command:    proto.CmdRestorePost,
		Payload:    restorePayload,
		EnqueuedAt: 3234,
	})
	if len(events) != 4 {
		t.Fatalf("events after restore = %+v, want post.restored", events)
	}
	restoreEvent := requireNativeDecisionPayload[proto.PostRestoredPayload](t, events[3], "restore event payload")
	if restoreEvent.ID != rootPostPayload.ID || restoreEvent.Thread != rootPostPayload.Thread || restoreEvent.By != bob.ID || restoreEvent.TS != 3234 {
		t.Fatalf("restore event = %+v, want deterministic restore", restoreEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-redact-restore-test", partition, 10, "materialize restore event")
	post, err = c.GetPost(rootPostPayload.ID)
	if err != nil {
		t.Fatalf("get post after restore: %v", err)
	}
	if post == nil || post.Redacted || post.UpdatedAt != 3234 {
		t.Fatalf("post after restore = %+v, want restored post", post)
	}
	deleted, err = c.ListBoardDeletedPosts("general", "junk", 10, 0)
	if err != nil {
		t.Fatalf("list junk deleted posts after restore: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("junk deleted posts after restore = %+v, want cleared deletion row", deleted)
	}
	if search, err := c.SearchReadablePosts(alice, "redactsearchtoken", "", 10); err != nil || len(search) != 1 {
		t.Fatalf("search after restore = %+v, %v; want visible post", search, err)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsPostRangeRedactAndRestore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCoreTestCore(t)
	go c.Run(ctx)
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Range moderation",
		Body:  "first range post",
	})
	createReply := c.ExecCmd(ctx, bob, proto.CmdCreateThread, createPayload, "cid-sql-range-create")
	if createReply.Err != nil || createReply.Result == nil {
		t.Fatalf("create range thread reply = %+v", createReply)
	}
	appendPayload := marshalCoreTestJSON(t, "marshal append payload", proto.AppendPostPayload{
		Thread: createReply.Result.ID,
		Body:   "second range post",
	})
	appendReply := c.ExecCmd(ctx, bob, proto.CmdAppendPost, appendPayload, "cid-sql-range-append")
	if appendReply.Err != nil || appendReply.Result == nil {
		t.Fatalf("append range post reply = %+v", appendReply)
	}
	posts, err := c.ListPosts(createReply.Result.ID, 10, 0)
	if err != nil {
		t.Fatalf("list range posts: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("range posts = %+v, want two posts", posts)
	}
	firstPostID := posts[0].ID
	secondPostID := posts[1].ID
	canModeratePosts := true
	if err := projections.SetBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{CanModeratePosts: &canModeratePosts}); err != nil {
		t.Fatalf("grant bob post moderation: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)
	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	rangePayload := marshalCoreTestJSON(t, "marshal redact range payload", proto.RedactPostRangePayload{
		Board:  "general",
		Posts:  []string{firstPostID, secondPostID, firstPostID},
		Reason: "range cleanup",
	})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-range-redact-denied",
		Command:    proto.CmdRedactPostRange,
		Payload:    rangePayload,
		EnqueuedAt: 3333,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "range redact denied reply")
	emptyPostRangePayload := marshalCoreTestJSON(t, "marshal empty post range payload", proto.RedactPostRangePayload{
		Board:  "general",
		Reason: "range cleanup",
	})
	emptyPostRange := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-range-redact-empty",
		Command:    proto.CmdRedactPostRange,
		Payload:    emptyPostRangePayload,
		EnqueuedAt: 3333,
	})
	requireNativeDecisionTerminalError(t, emptyPostRange, proto.ErrValidationFailed, "empty range redact decision", validationMessage(nil, proto.NormalizePostRangeIDs))
	dryRun := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-range-redact",
		Command:    proto.CmdRedactPostRange,
		Payload:    rangePayload,
		EnqueuedAt: 3333,
	})
	if dryRun.Err != nil || dryRun.Result == nil || dryRun.Result.ID != "2" {
		t.Fatalf("range redact dry run = %+v, want ack count 2", dryRun)
	}
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "range redact", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-range-redact",
		Command:    proto.CmdRedactPostRange,
		Payload:    rangePayload,
		EnqueuedAt: 3333,
	})
	requireNativeDecisionEventKinds(t, events, "range redact events", proto.EvtPostRedacted, proto.EvtPostRedacted)
	for i, event := range events {
		payload := requireNativeDecisionPayload[proto.PostRedactedPayload](t, event, fmt.Sprintf("range redact event %d payload", i))
		if payload.By != bob.ID || payload.Reason != "range cleanup" || payload.DeletionKind != "recycle" || payload.TS != 3333 {
			t.Fatalf("range redact payload %d = %+v, want moderator recycle redaction", i, payload)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-range-moderation-test", boardPartition, 10, "materialize range redact events")
	for _, postID := range []string{firstPostID, secondPostID} {
		post, err := c.GetPost(postID)
		if err != nil {
			t.Fatalf("get redacted range post %s: %v", postID, err)
		}
		if post == nil || !post.Redacted || post.UpdatedAt != 3333 {
			t.Fatalf("range post %s after redact = %+v, want redacted", postID, post)
		}
	}
	junk, err := c.ListBoardDeletedPosts("general", "junk", 10, 0)
	if err != nil {
		t.Fatalf("list junk after range redact: %v", err)
	}
	if len(junk) != 0 {
		t.Fatalf("junk after range redact = %+v, want empty even though moderator authored posts", junk)
	}
	recycle, err := c.ListBoardDeletedPosts("general", "recycle", 10, 0)
	if err != nil {
		t.Fatalf("list recycle after range redact: %v", err)
	}
	if len(recycle) != 2 {
		t.Fatalf("recycle after range redact = %+v, want two posts", recycle)
	}

	restorePayload := marshalCoreTestJSON(t, "marshal restore range payload", proto.RestorePostRangePayload{
		Board: "general",
		Posts: []string{firstPostID},
	})
	_, events = produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "range restore", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-range-restore",
		Command:    proto.CmdRestorePostRange,
		Payload:    restorePayload,
		EnqueuedAt: 4444,
	})
	if len(events) != 3 || events[2].Kind != proto.EvtPostRestored {
		t.Fatalf("range events after restore = %+v, want post.restored", events)
	}
	restoreEvent := requireNativeDecisionPayload[proto.PostRestoredPayload](t, events[2], "range restore payload")
	if restoreEvent.ID != firstPostID || restoreEvent.By != bob.ID || restoreEvent.TS != 4444 {
		t.Fatalf("range restore event = %+v, want first post restored by bob", restoreEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-range-moderation-test", boardPartition, 10, "materialize range restore event")
	firstPost, err := c.GetPost(firstPostID)
	if err != nil {
		t.Fatalf("get first post after range restore: %v", err)
	}
	secondPost, err := c.GetPost(secondPostID)
	if err != nil {
		t.Fatalf("get second post after range restore: %v", err)
	}
	if firstPost == nil || firstPost.Redacted || firstPost.UpdatedAt != 4444 {
		t.Fatalf("first post after range restore = %+v, want restored", firstPost)
	}
	if secondPost == nil || !secondPost.Redacted {
		t.Fatalf("second post after range restore = %+v, want still redacted", secondPost)
	}
	recycle, err = c.ListBoardDeletedPosts("general", "recycle", 10, 0)
	if err != nil {
		t.Fatalf("list recycle after range restore: %v", err)
	}
	if len(recycle) != 1 || recycle[0].PostID != secondPostID {
		t.Fatalf("recycle after range restore = %+v, want only second post", recycle)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsClearBoardJunk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCoreTestCore(t)
	go c.Run(ctx)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")
	canModeratePosts := true
	if err := projections.SetBoardMember(c.DB, "general", alice.ID, true, BoardMemberPatch{CanModeratePosts: &canModeratePosts}); err != nil {
		t.Fatalf("grant alice post moderation: %v", err)
	}

	createJunkPost := func(cid, title string) string {
		t.Helper()
		createPayload := marshalCoreTestJSON(t, "marshal create junk post payload", proto.CreateThreadPayload{
			Board: "general",
			Title: title,
			Body:  title + " body",
		})
		reply := c.ExecCmd(ctx, bob, proto.CmdCreateThread, createPayload, cid+"-create")
		if reply.Err != nil || reply.Result == nil {
			t.Fatalf("create junk post reply = %+v", reply)
		}
		posts, err := c.ListPosts(reply.Result.ID, 10, 0)
		if err != nil {
			t.Fatalf("list junk target posts: %v", err)
		}
		if len(posts) != 1 {
			t.Fatalf("junk target posts = %+v, want one post", posts)
		}
		redactPayload := marshalCoreTestJSON(t, "marshal redact junk post payload", proto.RedactPostPayload{
			Post:   posts[0].ID,
			Reason: "author cleanup",
		})
		redactReply := c.ExecCmd(ctx, bob, proto.CmdRedactPost, redactPayload, cid+"-redact")
		if redactReply.Err != nil {
			t.Fatalf("redact junk post reply = %+v", redactReply)
		}
		return posts[0].ID
	}
	firstPostID := createJunkPost("cid-sql-junk-one", "Junk one")
	secondPostID := createJunkPost("cid-sql-junk-two", "Junk two")

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)
	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	clearPayload := marshalCoreTestJSON(t, "marshal selected junk clear payload", proto.ClearBoardJunkPayload{
		Board: "general",
		Posts: []string{firstPostID, firstPostID},
	})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-clear-junk-denied",
		Command:    proto.CmdClearBoardJunk,
		Payload:    clearPayload,
		EnqueuedAt: 5555,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "clear junk denied reply")
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "selected junk clear", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-clear-junk-one",
		Command:    proto.CmdClearBoardJunk,
		Payload:    clearPayload,
		EnqueuedAt: 5555,
	})
	requireNativeDecisionEventKinds(t, events, "selected junk clear events", proto.EvtPostDeletionCleared)
	clearEvent := requireNativeDecisionPayload[proto.PostDeletionClearedPayload](t, events[0], "selected junk clear payload")
	if clearEvent.ID != firstPostID || clearEvent.Board != "general" || clearEvent.Kind != "junk" || clearEvent.By != alice.ID || clearEvent.TS != 5555 {
		t.Fatalf("selected junk clear event = %+v, want first junk post cleared by alice", clearEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-clear-junk-test", boardPartition, 10, "materialize selected junk clear event")
	junk, err := c.ListBoardDeletedPosts("general", "junk", 10, 0)
	if err != nil {
		t.Fatalf("list junk after selected clear: %v", err)
	}
	if len(junk) != 1 || junk[0].PostID != secondPostID {
		t.Fatalf("junk after selected clear = %+v, want only second post", junk)
	}

	clearAllPayload := marshalCoreTestJSON(t, "marshal all junk clear payload", proto.ClearBoardJunkPayload{Board: "general"})
	_, events = produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "all junk clear", 0, 10, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-clear-junk-all",
		Command:    proto.CmdClearBoardJunk,
		Payload:    clearAllPayload,
		EnqueuedAt: 6666,
	})
	if len(events) != 2 || events[1].Kind != proto.EvtPostDeletionCleared {
		t.Fatalf("events after all junk clear = %+v, want second post.deletion_cleared", events)
	}
	clearAllEvent := requireNativeDecisionPayload[proto.PostDeletionClearedPayload](t, events[1], "all junk clear payload")
	if clearAllEvent.ID != secondPostID || clearAllEvent.By != alice.ID || clearAllEvent.TS != 6666 {
		t.Fatalf("all junk clear event = %+v, want second junk post cleared by alice", clearAllEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-clear-junk-test", boardPartition, 10, "materialize all junk clear event")
	junk, err = c.ListBoardDeletedPosts("general", "junk", 10, 0)
	if err != nil {
		t.Fatalf("list junk after all clear: %v", err)
	}
	if len(junk) != 0 {
		t.Fatalf("junk after all clear = %+v, want empty", junk)
	}
	for _, postID := range []string{firstPostID, secondPostID} {
		post, err := c.GetPost(postID)
		if err != nil {
			t.Fatalf("get post %s after junk clear: %v", postID, err)
		}
		if post == nil || !post.Redacted {
			t.Fatalf("post %s after junk clear = %+v, want still redacted", postID, post)
		}
	}
}

func TestNativeCommandLogDecisionExecutorProjectsPurgePost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCoreTestCore(t)
	go c.Run(ctx)
	admin := registerNativeDecisionTestUser(t, c, "admin")
	bob := registerNativeDecisionTestUser(t, c, "bob")

	createPayload := marshalCoreTestJSON(t, "marshal purge target payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Native purge target",
		Body:  "purgeprivacytoken body that must leave local fts",
	})
	createReply := c.ExecCmd(ctx, bob, proto.CmdCreateThread, createPayload, "cid-sql-purge-target")
	if createReply.Err != nil || createReply.Result == nil {
		t.Fatalf("create purge target reply = %+v", createReply)
	}
	posts, err := c.ListPosts(createReply.Result.ID, 10, 0)
	if err != nil {
		t.Fatalf("list purge target posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("purge target posts = %+v, want one post", posts)
	}
	postID := posts[0].ID
	if search, err := c.SearchReadablePosts(admin, "purgeprivacytoken", "", 10); err != nil || len(search) != 1 {
		t.Fatalf("search before purge = %+v, %v; want visible target", search, err)
	}
	var ftsRows int
	if err := qQueryRow(c.DB, `SELECT COUNT(*) FROM posts_fts WHERE post_id=?`, postID).Scan(&ftsRows); err != nil || ftsRows != 1 {
		t.Fatalf("fts rows before purge = %d, %v; want 1, nil", ftsRows, err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)
	postPartition := LogPartition{Kind: partitionPost, Key: postID}
	purgePayload := marshalCoreTestJSON(t, "marshal purge payload", proto.PurgePostPayload{
		Post:   postID,
		Reason: "privacy request",
	})
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-purge-denied",
		Command:    proto.CmdPurgePost,
		Payload:    purgePayload,
		EnqueuedAt: 7777,
	})
	requireNativeDecisionTerminalError(t, denied, proto.ErrForbidden, "purge denied reply")
	purgePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	_, events := produceDrainReplayNativeDecisionEvents(t, ctx, commandLog, eventStore, worker, "purge", purgePartition, 0, 10, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-purge-post",
		Command:    proto.CmdPurgePost,
		Payload:    purgePayload,
		EnqueuedAt: 7777,
	})
	requireNativeDecisionEventKinds(t, events, "purge events", proto.EvtPostPurged)
	purgeEvent := requireNativeDecisionPayload[proto.PostPurgedPayload](t, events[0], "purge event payload")
	if purgeEvent.ID != postID || purgeEvent.Thread != createReply.Result.ID || purgeEvent.By != admin.ID || purgeEvent.Reason != "privacy request" || purgeEvent.TS != 7777 {
		t.Fatalf("purge event = %+v, want deterministic admin purge", purgeEvent)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-purge-test", purgePartition, 10, "materialize purge event")
	post, err := c.GetPost(postID)
	if err != nil {
		t.Fatalf("get post after purge: %v", err)
	}
	if post == nil || post.Body != "" || !post.Redacted || post.UpdatedAt != 7777 {
		t.Fatalf("post after purge = %+v, want empty redacted body", post)
	}
	if search, err := c.SearchReadablePosts(admin, "purgeprivacytoken", "", 10); err != nil || len(search) != 0 {
		t.Fatalf("search after purge = %+v, %v; want no target", search, err)
	}
	if err := qQueryRow(c.DB, `SELECT COUNT(*) FROM posts_fts WHERE post_id=?`, postID).Scan(&ftsRows); err != nil || ftsRows != 0 {
		t.Fatalf("fts rows after purge = %d, %v; want 0, nil", ftsRows, err)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsPollPosts(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	if err := setNativeDecisionTestTrustLevel(c, alice.ID, 2); err != nil {
		t.Fatalf("seed alice trust level: %v", err)
	}

	commandLog, eventStore, executor, worker := newNativeDecisionTestHarness(c)

	blockedPollPayload := marshalCoreTestJSON(t, "marshal blocked poll payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Blocked native poll",
		Body:  "[poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	})
	blocked := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-poll-low-trust",
		Command:    proto.CmdCreateThread,
		Payload:    blockedPollPayload,
		EnqueuedAt: 1111,
	})
	requireNativeDecisionTerminalError(t, blocked, proto.ErrForbidden, "blocked low-trust poll reply")

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createBody := "intro\n[poll]\nQuestion?\nOption A\nOption B\n[/poll]\noutro"
	createPayload := marshalCoreTestJSON(t, "marshal create poll payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native poll",
		Body:  createBody,
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "create poll", CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-poll",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-poll-test", partition, 10, "materialize create poll events")
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 0, 10, "replay create poll events")
	if len(events) != 2 {
		t.Fatalf("create poll events = %+v, want thread.new and post.appended", events)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root post payload")
	if rootPostPayload.Body != "intro\noutro" || rootPostPayload.RawBody != createBody {
		t.Fatalf("root poll event body/rawBody = %q/%q, want stripped body and raw poll body", rootPostPayload.Body, rootPostPayload.RawBody)
	}
	posts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after create poll: %v", err)
	}
	if len(posts) != 1 || posts[0].Body != "intro\noutro" {
		t.Fatalf("posts after create poll = %+v, want stripped root post body", posts)
	}
	rootPoll, err := c.GetPollByPostID(posts[0].ID)
	if err != nil {
		t.Fatalf("get root poll: %v", err)
	}
	if rootPoll == nil || rootPoll.Question != "Question?" {
		t.Fatalf("root poll = %+v, want projected poll question", rootPoll)
	}
	fullRootPoll, err := c.GetPoll(rootPoll.ID, alice.ID)
	if err != nil {
		t.Fatalf("get full root poll: %v", err)
	}
	if fullRootPoll == nil || len(fullRootPoll.Options) != 2 || fullRootPoll.Options[0].Text != "Option A" || fullRootPoll.Options[1].Text != "Option B" {
		t.Fatalf("full root poll = %+v, want two projected options", fullRootPoll)
	}

	editExistingPollPayload := marshalCoreTestJSON(t, "marshal edit existing poll payload", proto.EditPostPayload{
		Post: rootPostPayload.ID,
		Body: "edited poll body",
	})
	editExistingPollReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionPost, Key: rootPostPayload.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-edit-existing-poll-post",
		Command:    proto.CmdEditPost,
		Payload:    editExistingPollPayload,
		EnqueuedAt: 1734,
	})
	requireNativeDecisionTerminalError(t, editExistingPollReply, proto.ErrValidationFailed, "edit existing poll reply")
	if !strings.Contains(editExistingPollReply.Err.Message, "contain a poll") {
		t.Fatalf("edit existing poll error = %q, want poll-bearing post validation", editExistingPollReply.Err.Message)
	}

	appendPayload := marshalCoreTestJSON(t, "marshal append poll payload", proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "[poll]\nReply question?\nYes\nNo\n[/poll]",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "append poll", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-poll",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-poll-test", partition, 10, "materialize append poll event")
	posts, err = c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after append poll: %v", err)
	}
	if len(posts) != 2 || posts[1].Body != "" {
		t.Fatalf("posts after append poll = %+v, want empty stripped reply body", posts)
	}
	replyPoll, err := c.GetPollByPostID(posts[1].ID)
	if err != nil {
		t.Fatalf("get reply poll: %v", err)
	}
	if replyPoll == nil || replyPoll.Question != "Reply question?" {
		t.Fatalf("reply poll = %+v, want projected reply poll", replyPoll)
	}
	fullReplyPoll, err := c.GetPoll(replyPoll.ID, alice.ID)
	if err != nil {
		t.Fatalf("get full reply poll: %v", err)
	}
	if fullReplyPoll == nil || len(fullReplyPoll.Options) != 2 || fullReplyPoll.Options[0].Text != "Yes" || fullReplyPoll.Options[1].Text != "No" {
		t.Fatalf("full reply poll = %+v, want two projected reply options", fullReplyPoll)
	}
}

func setNativeDecisionTestTrustLevel(c *Core, userID string, trustLevel int) error {
	_, err := c.DB.Exec(
		`INSERT INTO user_activity (user_id, posts_created, days_visited, last_visit_day, reactions_recv, trust_level)
		 VALUES (?, 0, 0, '1970-01-01', 0, ?)
		 ON CONFLICT(user_id) DO UPDATE SET trust_level=excluded.trust_level`,
		userID, trustLevel,
	)
	return err
}

func TestNativeCommandLogDecisionExecutorProjectsQuotedReplies(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	carol := registerNativeDecisionTestUser(t, c, "carol")

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native quoted reply",
		Body:  "root mentions @bob\nsecond line",
	})
	_, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-quote",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-quote-test", partition, 10, "materialize create events")
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed outbox jobs after create = %d, want root post job", processed)
	}
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root post payload")
	bobNotifications, err := c.ListNotifications(bob.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("ListNotifications(bob) after create: %v", err)
	}
	if len(bobNotifications) != 1 || bobNotifications[0].Kind != "mention" || bobNotifications[0].PostID != rootPostPayload.ID {
		t.Fatalf("bob notifications after create = %+v, want one root mention", bobNotifications)
	}

	replyBody := "quoted reply without a mention"
	appendPayload := marshalCoreTestJSON(t, "marshal quoted reply payload", proto.AppendPostPayload{
		Thread:    threadPayload.ID,
		ReplyTo:   rootPostPayload.ID,
		QuotePost: true,
		Body:      replyBody,
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "quoted reply", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    carol.ID,
		CID:        "cid-native-append-post-quote",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	quoteEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 2, 10, "replay quoted reply event")
	requireNativeDecisionEventKinds(t, quoteEvents, "quote events", proto.EvtPostAppended)
	quotePostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, quoteEvents[0], "quote post payload")
	expectedPrefix := "> alice wrote:\n> root mentions @bob\n> second line\n\n"
	if quotePostPayload.Body != expectedPrefix+replyBody || quotePostPayload.RawBody != expectedPrefix+replyBody || quotePostPayload.PostCommitBody == nil || *quotePostPayload.PostCommitBody != replyBody {
		postCommitBody := "<nil>"
		if quotePostPayload.PostCommitBody != nil {
			postCommitBody = *quotePostPayload.PostCommitBody
		}
		t.Fatalf("quote payload body/raw/postCommit = %q/%q/%q, want display quote with unquoted post-commit body", quotePostPayload.Body, quotePostPayload.RawBody, postCommitBody)
	}
	if quotePostPayload.ReplyTo != rootPostPayload.ID {
		t.Fatalf("quote payload ReplyTo = %q, want root %q", quotePostPayload.ReplyTo, rootPostPayload.ID)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-quote-test", partition, 10, "materialize quoted reply event")
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed outbox jobs after quoted reply = %d, want quoted reply post job", processed)
	}
	posts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after quoted reply: %v", err)
	}
	if len(posts) != 2 || posts[1].ID != quotePostPayload.ID || posts[1].Body != expectedPrefix+replyBody || posts[1].ReplyTo != rootPostPayload.ID {
		t.Fatalf("posts after quoted reply = %+v, want materialized quoted reply", posts)
	}
	aliceNotifications, err := c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("ListNotifications(alice): %v", err)
	}
	if len(aliceNotifications) != 1 || aliceNotifications[0].Kind != "reply" || aliceNotifications[0].PostID != quotePostPayload.ID || aliceNotifications[0].Actor != carol.Name {
		t.Fatalf("alice notifications = %+v, want reply notification from carol", aliceNotifications)
	}
	bobNotifications, err = c.ListNotifications(bob.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("ListNotifications(bob) after quoted reply: %v", err)
	}
	if len(bobNotifications) != 1 || bobNotifications[0].PostID != rootPostPayload.ID {
		t.Fatalf("bob notifications after quoted reply = %+v, want no new mention from quoted source text", bobNotifications)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsRandomSignatures(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	first, err := c.SaveUserSignature(alice.ID, "", "First", "signature one", -1, true)
	if err != nil {
		t.Fatalf("save first signature: %v", err)
	}
	if _, err := c.SaveUserSignature(alice.ID, "", "Second", "signature two", -1, true); err != nil {
		t.Fatalf("save second signature: %v", err)
	}
	if err := c.SetUserSignatureSettings(alice.ID, first.ID, true); err != nil {
		t.Fatalf("enable random signatures: %v", err)
	}

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal create payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native random signature",
		Body:  "root post",
	})
	createRecord, events := produceDrainReplayNativeDecisionCommand(t, ctx, commandLog, eventStore, worker, "create", 0, 10, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-random-signature",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread event payload")
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root post payload")
	expectedRootSignature, err := nativePostSignature(c.DB, alice.ID, createRecord)
	if err != nil {
		t.Fatalf("expected root native signature: %v", err)
	}
	if rootPostPayload.Signature != expectedRootSignature || !nativeSignatureIn(rootPostPayload.Signature, "signature one", "signature two") {
		t.Fatalf("root signature = %q, want deterministic active random signature %q", rootPostPayload.Signature, expectedRootSignature)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-random-signature-test", partition, 10, "materialize create events")
	posts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after create: %v", err)
	}
	if len(posts) != 1 || posts[0].Signature != rootPostPayload.Signature {
		t.Fatalf("posts after create = %+v, want broker-projected random signature %q", posts, rootPostPayload.Signature)
	}

	appendPayload := marshalCoreTestJSON(t, "marshal append payload", proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "reply post",
	})
	appendRecord := produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "append", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-random-signature",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	appendEvents := replayCommandLogWorkerPartition(t, ctx, eventStore, partition, 2, 10, "replay append event")
	if len(appendEvents) != 1 {
		t.Fatalf("append events = %+v, want one post.appended", appendEvents)
	}
	replyPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, appendEvents[0], "reply post payload")
	expectedReplySignature, err := nativePostSignature(c.DB, alice.ID, appendRecord)
	if err != nil {
		t.Fatalf("expected reply native signature: %v", err)
	}
	if replyPostPayload.Signature != expectedReplySignature || !nativeSignatureIn(replyPostPayload.Signature, "signature one", "signature two") {
		t.Fatalf("reply signature = %q, want deterministic active random signature %q", replyPostPayload.Signature, expectedReplySignature)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-random-signature-test", partition, 10, "materialize append event")
	posts, err = c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after append: %v", err)
	}
	if len(posts) != 2 || posts[1].Signature != replyPostPayload.Signature {
		t.Fatalf("posts after append = %+v, want reply random signature %q", posts, replyPostPayload.Signature)
	}
}

func nativeSignatureIn(got string, options ...string) bool {
	for _, option := range options {
		if got == option {
			return true
		}
	}
	return false
}

func TestNativeCommandLogDecisionExecutorProjectsAnonymousPosts(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	alice := registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	if err := c.UpdateUserProfile(alice.ID, "Alice", "", "bio", "", "private-ish signature", "", ""); err != nil {
		t.Fatalf("update alice profile signature: %v", err)
	}
	anonymousAllowed := true
	if err := projections.SetBoardSettings(c.DB, "general", BoardSettingsPatch{AnonymousAllowed: &anonymousAllowed}); err != nil {
		t.Fatalf("enable anonymous posting: %v", err)
	}

	commandLog, eventStore, _, worker := newNativeDecisionTestHarness(c)

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload := marshalCoreTestJSON(t, "marshal anonymous create payload", proto.CreateThreadPayload{
		Board:     "general",
		Title:     "Broker-native anonymous post",
		Body:      "anonymous root post",
		Anonymous: true,
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "anonymous create", CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-anonymous-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	events := replayCommandLogWorkerPartition(t, ctx, eventStore, boardPartition, 0, 10, "replay anonymous create events")
	if len(events) != 2 {
		t.Fatalf("anonymous create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, events[0], "thread payload")
	rootPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[1], "root post payload")
	if threadPayload.Author != "Anonymous" || threadPayload.AuthorID != "" ||
		rootPostPayload.Author != "Anonymous" || rootPostPayload.AuthorID != "" ||
		rootPostPayload.Signature != "" || rootPostPayload.PostCommitActorID != alice.ID ||
		rootPostPayload.PostCommitActorName != "Anonymous" {
		t.Fatalf("anonymous create payloads = thread %+v post %+v, want public anonymous identity with hidden commit actor", threadPayload, rootPostPayload)
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-anonymous-test", boardPartition, 10, "materialize anonymous create events")
	thread, err := c.GetThread(threadPayload.ID)
	if err != nil {
		t.Fatalf("get anonymous thread: %v", err)
	}
	if thread == nil || thread.Author != "Anonymous" || thread.AuthorID != "" {
		t.Fatalf("thread after anonymous projection = %+v, want anonymous public identity", thread)
	}
	posts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list anonymous posts: %v", err)
	}
	if len(posts) != 1 || posts[0].Author != "Anonymous" || posts[0].AuthorID != "" ||
		posts[0].Signature != "" || posts[0].Body != "anonymous root post" {
		t.Fatalf("posts after anonymous create = %+v, want anonymous root without signature", posts)
	}
	var alicePostsCreated int
	if err := qQueryRow(c.DB, `SELECT posts_created FROM user_activity WHERE user_id=?`, alice.ID).Scan(&alicePostsCreated); err != nil {
		t.Fatalf("query alice post activity: %v", err)
	}
	if alicePostsCreated != 1 {
		t.Fatalf("alice posts_created after anonymous create = %d, want hidden actor activity", alicePostsCreated)
	}
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed root outbox jobs = %d, want one anonymous root post.committed job", processed)
	}
	if err := qQueryRow(c.DB, `SELECT posts_created FROM user_activity WHERE user_id=?`, alice.ID).Scan(&alicePostsCreated); err != nil {
		t.Fatalf("query alice post activity after root outbox: %v", err)
	}
	if alicePostsCreated != 2 {
		t.Fatalf("alice posts_created after root outbox = %d, want hidden actor post.committed activity", alicePostsCreated)
	}

	watchLevel := "watch"
	if err := projections.SetThreadPref(c.DB, bob.ID, threadPayload.ID, watchLevel); err != nil {
		t.Fatalf("watch anonymous thread: %v", err)
	}
	appendPayload := marshalCoreTestJSON(t, "marshal anonymous reply payload", proto.AppendPostPayload{
		Thread:    threadPayload.ID,
		Anonymous: true,
		Body:      "anonymous reply body",
	})
	produceDrainNativeDecisionCommand(t, ctx, commandLog, worker, "anonymous reply", CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-anonymous-reply",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-anonymous-test", boardPartition, 10, "materialize anonymous reply event")
	events = replayCommandLogWorkerPartition(t, ctx, eventStore, boardPartition, 0, 10, "replay anonymous reply events")
	if len(events) != 3 {
		t.Fatalf("anonymous events after reply = %+v, want three board events", events)
	}
	replyPostPayload := requireNativeDecisionPayload[proto.PostAppendedPayload](t, events[2], "reply post payload")
	if replyPostPayload.Author != "Anonymous" || replyPostPayload.AuthorID != "" ||
		replyPostPayload.Signature != "" || replyPostPayload.PostCommitActorID != alice.ID ||
		replyPostPayload.PostCommitActorName != "Anonymous" {
		t.Fatalf("anonymous reply payload = %+v, want public anonymous identity with hidden commit actor", replyPostPayload)
	}
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed reply outbox jobs = %d, want one anonymous reply post.committed job", processed)
	}
	notifications, err := c.ListNotifications(bob.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("list bob notifications: %v", err)
	}
	if len(notifications) != 1 || notifications[0].Kind != "watched" ||
		notifications[0].PostID != replyPostPayload.ID ||
		notifications[0].Actor != "Anonymous" {
		t.Fatalf("bob notifications = %+v, want watched notification from anonymous actor label", notifications)
	}
	if err := qQueryRow(c.DB, `SELECT posts_created FROM user_activity WHERE user_id=?`, alice.ID).Scan(&alicePostsCreated); err != nil {
		t.Fatalf("query alice post activity after reply: %v", err)
	}
	if alicePostsCreated != 4 {
		t.Fatalf("alice posts_created after projection and outbox = %d, want hidden actor activity for both anonymous posts", alicePostsCreated)
	}
}

func TestNativeCommandLogDecisionExecutorRejectsAnonymousWhenDisabled(t *testing.T) {
	ctx := context.Background()
	c := newCoreTestCore(t)
	registerNativeDecisionTestUser(t, c, "alice")
	bob := registerNativeDecisionTestUser(t, c, "bob")
	executor := NewCommandLogNativeDecisionExecutor(c)
	createPayload := marshalCoreTestJSON(t, "marshal anonymous create payload", proto.CreateThreadPayload{
		Board:     "general",
		Title:     "Anonymous disabled",
		Body:      "anonymous should need board policy",
		Anonymous: true,
	})
	reply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-anonymous-disabled-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	requireNativeDecisionTerminalError(t, reply, proto.ErrForbidden, "anonymous create reply")

	basePayload := marshalCoreTestJSON(t, "marshal base payload", proto.CreateThreadPayload{
		Board: "general",
		Title: "Native anonymous disabled base",
		Body:  "root post",
	})
	baseRecord := CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:     2,
		ActorID:    bob.ID,
		CID:        "cid-native-anonymous-disabled-base",
		Command:    proto.CmdCreateThread,
		Payload:    basePayload,
		EnqueuedAt: 2234,
	}
	baseReply := executor.ExecuteCommandLogRecord(ctx, baseRecord)
	baseEvents, err := executor.DecideCommandLogEvents(ctx, baseRecord, baseReply)
	if err != nil {
		t.Fatalf("decide base native events: %v", err)
	}
	if baseReply.Err != nil || len(baseEvents) != 2 {
		t.Fatalf("base native create reply = %+v, want decided thread events", baseReply)
	}
	threadPayload := requireNativeDecisionPayload[proto.ThreadNewPayload](t, baseEvents[0], "base thread payload")
	eventStore := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	for _, event := range baseEvents {
		if _, err := eventStore.Append(ctx, event); err != nil {
			t.Fatalf("append base native event %s: %v", event.ID, err)
		}
	}
	materializeNativeDecisionPartition(t, ctx, c, eventStore, "native-anonymous-disabled-test", LogPartition{Kind: partitionBoard, Key: "general"}, 10, "materialize base native events")
	appendPayload := marshalCoreTestJSON(t, "marshal anonymous reply payload", proto.AppendPostPayload{
		Thread:    threadPayload.ID,
		Anonymous: true,
		Body:      "anonymous reply should need board policy",
	})
	reply = executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-anonymous-disabled-reply",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 3234,
	})
	requireNativeDecisionTerminalError(t, reply, proto.ErrForbidden, "anonymous reply")
}
