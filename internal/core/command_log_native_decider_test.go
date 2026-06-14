package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

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
		if commandBypassesCommandLog(name) {
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
		if commandBypassesCommandLog(name) {
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

func TestNativeCommandLogDecisionExecutorProjectsCreateBoard(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "clubs"}
	payload, err := json.Marshal(proto.CreateBoardPayload{
		ID:          "clubs",
		Name:        " Clubs ",
		Description: "Campus clubs",
		ParentID:    " general ",
	})
	if err != nil {
		t.Fatalf("marshal create board payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-create-board-forbidden",
		Command:    proto.CmdCreateBoard,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Retryable {
		t.Fatalf("create board as non-admin = %+v, want terminal forbidden", forbidden)
	}
	missingParentPayload, err := json.Marshal(proto.CreateBoardPayload{
		ID:       "orphan",
		Name:     "Orphan",
		ParentID: "missing",
	})
	if err != nil {
		t.Fatalf("marshal missing parent payload: %v", err)
	}
	missingParent := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "orphan"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-create-board-missing-parent",
		Command:    proto.CmdCreateBoard,
		Payload:    missingParentPayload,
		EnqueuedAt: 1234,
	})
	if missingParent.Err == nil || missingParent.Err.Code != proto.ErrNotFound || missingParent.Err.Retryable {
		t.Fatalf("create board missing parent = %+v, want terminal not_found", missingParent)
	}

	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-create-board",
		Command:    proto.CmdCreateBoard,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce create board command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create board once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, boardPartition); err != nil || got != record.Offset {
		t.Fatalf("create board committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create board events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtBoardCreated {
		t.Fatalf("create board events = %+v, want one board.created", events)
	}
	boardEvent, ok := events[0].Payload.(*proto.BoardCreatedPayload)
	if !ok {
		t.Fatalf("create board payload = %T, want BoardCreatedPayload", events[0].Payload)
	}
	if boardEvent.ID != "clubs" || boardEvent.Name != "Clubs" || boardEvent.Description != "Campus clubs" ||
		boardEvent.ParentID != "general" || boardEvent.Position != 0 || boardEvent.By != admin.ID || boardEvent.TS != 2234 {
		t.Fatalf("create board event = %+v, want deterministic child board", boardEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-create-board-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create board event: %v", err)
	}
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
	retryBoard, ok := retryEvents[0].Payload.(*proto.BoardCreatedPayload)
	if !ok {
		t.Fatalf("create board retry payload = %T, want BoardCreatedPayload", retryEvents[0].Payload)
	}
	if retryBoard.ID != boardEvent.ID || retryBoard.Name != boardEvent.Name || retryBoard.ParentID != boardEvent.ParentID ||
		retryBoard.Position != boardEvent.Position || retryBoard.Description != boardEvent.Description {
		t.Fatalf("create board retry payload = %+v, want stable payload %+v", retryBoard, boardEvent)
	}

	conflictPayload, err := json.Marshal(proto.CreateBoardPayload{
		ID:          "clubs",
		Name:        "Other Clubs",
		Description: "Changed",
		ParentID:    "general",
	})
	if err != nil {
		t.Fatalf("marshal conflicting create board payload: %v", err)
	}
	conflict := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     record.Offset + 1,
		ActorID:    admin.ID,
		CID:        "cid-native-create-board-conflict",
		Command:    proto.CmdCreateBoard,
		Payload:    conflictPayload,
		EnqueuedAt: 3234,
	})
	if conflict.Err == nil || conflict.Err.Code != proto.ErrConflict || conflict.Err.Retryable {
		t.Fatalf("conflicting create board = %+v, want terminal conflict", conflict)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsRecommendedBoard(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin board setup: %v", err)
	}
	if err := insertBoard(tx, "tech", "Tech", "Technology discussion", "", 1); err != nil {
		tx.Rollback()
		t.Fatalf("insert tech board: %v", err)
	}
	if err := insertBoard(tx, "secret", "Secret", "Members only", "", 2); err != nil {
		tx.Rollback()
		t.Fatalf("insert secret board: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit board setup: %v", err)
	}
	memberReadMode := true
	if err := setBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberReadMode}); err != nil {
		t.Fatalf("set secret board settings: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "tech"}
	payload, err := json.Marshal(proto.SetRecommendedBoardPayload{
		Board:       " tech ",
		Recommended: true,
		Note:        " Start here. ",
	})
	if err != nil {
		t.Fatalf("marshal recommended board payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-recommended-board-forbidden",
		Command:    proto.CmdSetRecommendedBoard,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "admin role required" || forbidden.Err.Retryable {
		t.Fatalf("set recommended board as non-admin = %+v, want terminal admin error", forbidden)
	}
	secretPayload, err := json.Marshal(proto.SetRecommendedBoardPayload{
		Board:       "secret",
		Recommended: true,
	})
	if err != nil {
		t.Fatalf("marshal secret recommended board payload: %v", err)
	}
	secret := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "secret"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-recommended-board-secret",
		Command:    proto.CmdSetRecommendedBoard,
		Payload:    secretPayload,
		EnqueuedAt: 1234,
	})
	if secret.Err == nil || secret.Err.Code != proto.ErrValidationFailed || secret.Err.Message != "member-read boards cannot be publicly recommended" || secret.Err.Retryable {
		t.Fatalf("set recommended member-read board = %+v, want terminal visibility validation", secret)
	}

	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-recommended-board",
		Command:    proto.CmdSetRecommendedBoard,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce recommended board command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain recommended board once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
		t.Fatalf("recommended board committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay recommended board events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtBoardRecommendedSet {
		t.Fatalf("recommended board events = %+v, want one board.recommended_set", events)
	}
	recommendedEvent, ok := events[0].Payload.(*proto.BoardRecommendedSetPayload)
	if !ok {
		t.Fatalf("recommended board payload = %T, want BoardRecommendedSetPayload", events[0].Payload)
	}
	if recommendedEvent.Board != "tech" || !recommendedEvent.Recommended || recommendedEvent.Note != "Start here." ||
		recommendedEvent.Position != 0 || recommendedEvent.CuratedBy != admin.ID || recommendedEvent.TS != 2234 {
		t.Fatalf("recommended board event = %+v, want deterministic recommendation", recommendedEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-recommended-board-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize recommended board event: %v", err)
	}
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
	retryRecommended, ok := retryEvents[0].Payload.(*proto.BoardRecommendedSetPayload)
	if !ok {
		t.Fatalf("recommended board retry payload = %T, want BoardRecommendedSetPayload", retryEvents[0].Payload)
	}
	if retryRecommended.Board != recommendedEvent.Board || retryRecommended.Position != recommendedEvent.Position ||
		retryRecommended.Note != recommendedEvent.Note || retryRecommended.CuratedBy != recommendedEvent.CuratedBy {
		t.Fatalf("recommended board retry payload = %+v, want stable payload %+v", retryRecommended, recommendedEvent)
	}

	clearPayload, err := json.Marshal(proto.SetRecommendedBoardPayload{
		Board:       "tech",
		Recommended: false,
	})
	if err != nil {
		t.Fatalf("marshal clear recommended board payload: %v", err)
	}
	clearRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-recommended-board-clear",
		Command:    proto.CmdSetRecommendedBoard,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce clear recommended board command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain clear recommended board once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-recommended-board-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize clear recommended board event: %v", err)
	}
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
	if len(clearRetryEvents) != 1 || clearRetryEvents[0].Kind != proto.EvtBoardRecommendedSet {
		t.Fatalf("clear recommended board retry events = %+v, want stable recommendation clear event", clearRetryEvents)
	}
	clearEvent, ok := clearRetryEvents[0].Payload.(*proto.BoardRecommendedSetPayload)
	if !ok {
		t.Fatalf("clear recommended board retry payload = %T, want BoardRecommendedSetPayload", clearRetryEvents[0].Payload)
	}
	if clearEvent.Board != "tech" || clearEvent.Recommended || clearEvent.Position != 0 ||
		clearEvent.CuratedBy != admin.ID || clearEvent.TS != 3234 {
		t.Fatalf("clear recommended board retry payload = %+v, want deterministic clear event", clearEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardSettings(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	if _, err := c.RegisterUser("admin", "pw"); err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	readOnly := true
	mailInAllowed := true
	statsExcluded := true
	zapAllowed := false
	payload, err := json.Marshal(proto.SetBoardSettingsPayload{
		Board:         " general ",
		ReadOnly:      &readOnly,
		MailInAllowed: &mailInAllowed,
		StatsExcluded: &statsExcluded,
		ZapAllowed:    &zapAllowed,
	})
	if err != nil {
		t.Fatalf("marshal board settings payload: %v", err)
	}
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
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "board settings permission required" || forbidden.Err.Retryable {
		t.Fatalf("set board settings without permission = %+v, want terminal board-settings forbidden", forbidden)
	}
	if _, err := qExec(c.DB,
		`INSERT INTO board_members (board_id, user_id, position, can_set_board_settings, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		"general", alice.ID, 10, int64(1234), int64(1234),
	); err != nil {
		t.Fatalf("grant delegated board-settings permission: %v", err)
	}

	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-settings",
		Command:    proto.CmdSetBoardSettings,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce board settings command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board settings once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
		t.Fatalf("board settings committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	boardEvents, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board settings events: %v", err)
	}
	if len(boardEvents) != 1 || boardEvents[0].Kind != proto.EvtBoardSettingsSet {
		t.Fatalf("board settings events = %+v, want one board.settings_set", boardEvents)
	}
	settingsEvent, ok := boardEvents[0].Payload.(*proto.BoardSettingsSetPayload)
	if !ok {
		t.Fatalf("board settings payload = %T, want BoardSettingsSetPayload", boardEvents[0].Payload)
	}
	if settingsEvent.Board != "general" || !settingsEvent.ReadOnly || !settingsEvent.MailInAllowed ||
		!settingsEvent.StatsExcluded || settingsEvent.ZapAllowed || settingsEvent.By != alice.ID || settingsEvent.TS != 2234 {
		t.Fatalf("board settings event = %+v, want deterministic final settings", settingsEvent)
	}
	syssecurityPartition := LogPartition{Kind: partitionBoard, Key: nativeSyssecuritySystemBoardID}
	syssecurityEvents, err := eventStore.ReplayPartition(ctx, syssecurityPartition.Kind, syssecurityPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board settings syssecurity events: %v", err)
	}
	if len(syssecurityEvents) != 3 ||
		syssecurityEvents[0].Kind != proto.EvtBoardCreated ||
		syssecurityEvents[1].Kind != proto.EvtThreadNew ||
		syssecurityEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("board settings syssecurity events = %+v, want board/thread/post audit events", syssecurityEvents)
	}
	auditPost, ok := syssecurityEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("board settings audit payload = %T, want PostAppendedPayload", syssecurityEvents[2].Payload)
	}
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

	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-settings-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board settings event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-settings-test",
		Partition: syssecurityPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board settings syssecurity events: %v", err)
	}
	settings, err := getBoardSettings(c.DB, "general")
	if err != nil {
		t.Fatalf("get projected board settings: %v", err)
	}
	if settings == nil || !settings.ReadOnly || !settings.MailInAllowed ||
		!settings.StatsExcluded || settings.ZapAllowed || settings.UpdatedAt != 2234 {
		t.Fatalf("projected board settings = %+v, want event final settings", settings)
	}
	syssecurityThreads, err := c.ListThreads(nativeSyssecuritySystemBoardID, 10, 0)
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
	retrySettings, ok := retryEvents[0].Payload.(*proto.BoardSettingsSetPayload)
	if !ok {
		t.Fatalf("board settings retry payload = %T, want BoardSettingsSetPayload", retryEvents[0].Payload)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	if _, err := c.RegisterUser("admin", "pw"); err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	minPostCount := 4
	minBoardMarkCount := 2
	if err := setBoardMemberRequirements(c.DB, "general", BoardMemberRequirementsPatch{
		MinPostCount:      &minPostCount,
		MinBoardMarkCount: &minBoardMarkCount,
	}); err != nil {
		t.Fatalf("seed board member requirements: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	minLoginCount := 3
	minTrustLevel := 2
	minScore := 7
	minBoardDigestCount := 5
	maxMembers := 42
	approvalMode := " automatic "
	payload, err := json.Marshal(proto.SetBoardMemberRequirementsPayload{
		Board:               " general ",
		MinLoginCount:       &minLoginCount,
		MinTrustLevel:       &minTrustLevel,
		MinScore:            &minScore,
		MinBoardDigestCount: &minBoardDigestCount,
		MaxMembers:          &maxMembers,
		ApprovalMode:        &approvalMode,
	})
	if err != nil {
		t.Fatalf("marshal board member requirements payload: %v", err)
	}
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
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "board settings permission required" || forbidden.Err.Retryable {
		t.Fatalf("set board member requirements without permission = %+v, want terminal board-settings forbidden", forbidden)
	}
	if _, err := qExec(c.DB,
		`INSERT INTO board_members (board_id, user_id, position, can_set_board_settings, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		"general", alice.ID, 10, int64(1234), int64(1234),
	); err != nil {
		t.Fatalf("grant delegated board-settings permission: %v", err)
	}
	negativeMinScore := -1
	negativePayload, err := json.Marshal(proto.SetBoardMemberRequirementsPayload{
		Board:    "general",
		MinScore: &negativeMinScore,
	})
	if err != nil {
		t.Fatalf("marshal negative requirements payload: %v", err)
	}
	negative := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-requirements-negative",
		Command:    proto.CmdSetBoardMemberRequirements,
		Payload:    negativePayload,
		EnqueuedAt: 1234,
	})
	if negative.Err == nil || negative.Err.Code != proto.ErrValidationFailed || negative.Err.Message != "minScore must be non-negative" || negative.Err.Retryable {
		t.Fatalf("set negative board member requirement = %+v, want terminal validation", negative)
	}
	badApproval := "open"
	badApprovalPayload, err := json.Marshal(proto.SetBoardMemberRequirementsPayload{
		Board:        "general",
		ApprovalMode: &badApproval,
	})
	if err != nil {
		t.Fatalf("marshal bad approval mode payload: %v", err)
	}
	badMode := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-requirements-bad-mode",
		Command:    proto.CmdSetBoardMemberRequirements,
		Payload:    badApprovalPayload,
		EnqueuedAt: 1234,
	})
	if badMode.Err == nil || badMode.Err.Code != proto.ErrValidationFailed || badMode.Err.Message != `approvalMode must be "manual" or "auto"` || badMode.Err.Retryable {
		t.Fatalf("set invalid board member approval mode = %+v, want terminal validation", badMode)
	}

	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-requirements",
		Command:    proto.CmdSetBoardMemberRequirements,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce board member requirements command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board member requirements once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
		t.Fatalf("board member requirements committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board member requirements events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtBoardMemberRequirementsSet {
		t.Fatalf("board member requirements events = %+v, want one board.member_requirements_set", events)
	}
	requirementsEvent, ok := events[0].Payload.(*proto.BoardMemberRequirementsSetPayload)
	if !ok {
		t.Fatalf("board member requirements payload = %T, want BoardMemberRequirementsSetPayload", events[0].Payload)
	}
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
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-member-requirements-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board member requirements event: %v", err)
	}
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
	retryRequirements, ok := retryEvents[0].Payload.(*proto.BoardMemberRequirementsSetPayload)
	if !ok {
		t.Fatalf("board member requirements retry payload = %T, want BoardMemberRequirementsSetPayload", retryEvents[0].Payload)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin secret board setup: %v", err)
	}
	if err := insertBoard(tx, "secret", "Secret", "Private board", "", 10); err != nil {
		t.Fatalf("insert secret board: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit secret board: %v", err)
	}
	memberRead := true
	if err := setBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	position := 7
	appointPayload, err := json.Marshal(proto.SetBoardModeratorPayload{
		Board:     " general ",
		User:      alice.Name,
		Moderator: true,
		Position:  &position,
	})
	if err != nil {
		t.Fatalf("marshal board moderator appoint payload: %v", err)
	}
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
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "admin role required" || forbidden.Err.Retryable {
		t.Fatalf("set board moderator as non-admin = %+v, want terminal admin forbidden", forbidden)
	}
	missingPayload, err := json.Marshal(proto.SetBoardModeratorPayload{
		Board:     "general",
		User:      "missing",
		Moderator: true,
	})
	if err != nil {
		t.Fatalf("marshal missing moderator user payload: %v", err)
	}
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-board-moderator-missing",
		Command:    proto.CmdSetBoardModerator,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	if missing.Err == nil || missing.Err.Code != proto.ErrNotFound || missing.Err.Message != "user not found" || missing.Err.Retryable {
		t.Fatalf("set board moderator missing user = %+v, want terminal not found", missing)
	}

	appointRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-moderator-appoint",
		Command:    proto.CmdSetBoardModerator,
		Payload:    appointPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce board moderator appoint command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board moderator appoint once: %v", err)
	}
	boardEvents, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board moderator appoint events: %v", err)
	}
	if len(boardEvents) != 1 || boardEvents[0].Kind != proto.EvtBoardModeratorSet {
		t.Fatalf("board moderator appoint events = %+v, want one board.moderator_set", boardEvents)
	}
	appointEvent, ok := boardEvents[0].Payload.(*proto.BoardModeratorSetPayload)
	if !ok {
		t.Fatalf("board moderator appoint payload = %T, want BoardModeratorSetPayload", boardEvents[0].Payload)
	}
	if appointEvent.Board != "general" || appointEvent.User != alice.ID || !appointEvent.Moderator ||
		appointEvent.Position != 7 || appointEvent.By != admin.ID || appointEvent.TS != 2234 {
		t.Fatalf("board moderator appoint event = %+v, want deterministic final moderator assignment", appointEvent)
	}
	syssecurityPartition := LogPartition{Kind: partitionBoard, Key: nativeSyssecuritySystemBoardID}
	syssecurityEvents, err := eventStore.ReplayPartition(ctx, syssecurityPartition.Kind, syssecurityPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board moderator syssecurity events: %v", err)
	}
	if len(syssecurityEvents) != 3 ||
		syssecurityEvents[0].Kind != proto.EvtBoardCreated ||
		syssecurityEvents[1].Kind != proto.EvtThreadNew ||
		syssecurityEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("board moderator syssecurity events = %+v, want board/thread/post audit events", syssecurityEvents)
	}
	appointAudit, ok := syssecurityEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("board moderator appoint audit payload = %T, want PostAppendedPayload", syssecurityEvents[2].Payload)
	}
	for _, want := range []string{"Action: board moderator appointed", "Board: general", "User: alice", "Actor: admin"} {
		if !strings.Contains(appointAudit.Body, want) {
			t.Fatalf("board moderator appoint audit body missing %q:\n%s", want, appointAudit.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-moderator-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board moderator appoint event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-moderator-test",
		Partition: syssecurityPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board moderator appoint syssecurity events: %v", err)
	}
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
	retryAppoint, ok := retryEvents[0].Payload.(*proto.BoardModeratorSetPayload)
	if !ok {
		t.Fatalf("board moderator appoint retry payload = %T, want BoardModeratorSetPayload", retryEvents[0].Payload)
	}
	if retryAppoint.Board != appointEvent.Board || retryAppoint.User != appointEvent.User ||
		retryAppoint.Moderator != appointEvent.Moderator || retryAppoint.Position != appointEvent.Position ||
		retryAppoint.By != appointEvent.By || retryAppoint.TS != appointEvent.TS {
		t.Fatalf("board moderator appoint retry payload = %+v, want stable payload %+v", retryAppoint, appointEvent)
	}

	removePayload, err := json.Marshal(proto.SetBoardModeratorPayload{
		Board:     "general",
		User:      alice.ID,
		Moderator: false,
	})
	if err != nil {
		t.Fatalf("marshal board moderator remove payload: %v", err)
	}
	removeRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-moderator-remove",
		Command:    proto.CmdSetBoardModerator,
		Payload:    removePayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce board moderator remove command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board moderator remove once: %v", err)
	}
	boardEvents, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board moderator events after remove: %v", err)
	}
	if len(boardEvents) != 2 || boardEvents[1].Kind != proto.EvtBoardModeratorSet {
		t.Fatalf("board moderator events after remove = %+v, want appointment then removal", boardEvents)
	}
	removeEvent, ok := boardEvents[1].Payload.(*proto.BoardModeratorSetPayload)
	if !ok {
		t.Fatalf("board moderator remove payload = %T, want BoardModeratorSetPayload", boardEvents[1].Payload)
	}
	if removeEvent.Board != "general" || removeEvent.User != alice.ID || removeEvent.Moderator ||
		removeEvent.Position != 7 || removeEvent.By != admin.ID || removeEvent.TS != 3234 {
		t.Fatalf("board moderator remove event = %+v, want deterministic removal with prior position", removeEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-moderator-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board moderator remove event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-moderator-test",
		Partition: syssecurityPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board moderator remove syssecurity events: %v", err)
	}
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
	removeRetry, ok := removeRetryEvents[0].Payload.(*proto.BoardModeratorSetPayload)
	if !ok {
		t.Fatalf("board moderator remove retry payload = %T, want BoardModeratorSetPayload", removeRetryEvents[0].Payload)
	}
	if removeRetry.Position != removeEvent.Position || removeRetry.Moderator != removeEvent.Moderator ||
		removeRetry.User != removeEvent.User || removeRetry.TS != removeEvent.TS {
		t.Fatalf("board moderator remove retry payload = %+v, want stable removal %+v", removeRetry, removeEvent)
	}

	secretPayload, err := json.Marshal(proto.SetBoardModeratorPayload{
		Board:     "secret",
		User:      bob.Name,
		Moderator: true,
	})
	if err != nil {
		t.Fatalf("marshal secret board moderator payload: %v", err)
	}
	secretPartition := LogPartition{Kind: partitionBoard, Key: "secret"}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  secretPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-moderator-secret",
		Command:    proto.CmdSetBoardModerator,
		Payload:    secretPayload,
		EnqueuedAt: 4234,
	}); err != nil {
		t.Fatalf("produce secret board moderator command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain secret board moderator once: %v", err)
	}
	secretEvents, err := eventStore.ReplayPartition(ctx, secretPartition.Kind, secretPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay secret board moderator events: %v", err)
	}
	if len(secretEvents) != 1 || secretEvents[0].Kind != proto.EvtBoardModeratorSet {
		t.Fatalf("secret board moderator events = %+v, want one private board moderator event", secretEvents)
	}
	syssecurityEvents, err = eventStore.ReplayPartition(ctx, syssecurityPartition.Kind, syssecurityPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay syssecurity after secret board moderator: %v", err)
	}
	if len(syssecurityEvents) != 5 {
		t.Fatalf("syssecurity events after private board moderator = %+v, want no new private-board audit", syssecurityEvents)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardMember(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	manager := true
	if err := setBoardMember(c.DB, "general", alice.ID, true, BoardMemberPatch{CanManageMembers: &manager}); err != nil {
		t.Fatalf("seed delegated board member manager: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	position := 3
	canCurate := true
	canSetSettings := true
	memberPayload, err := json.Marshal(proto.SetBoardMemberPayload{
		Board:               " general ",
		User:                bob.Name,
		Member:              true,
		Title:               " Curator ",
		Position:            &position,
		CanCurate:           &canCurate,
		CanSetBoardSettings: &canSetSettings,
	})
	if err != nil {
		t.Fatalf("marshal board member payload: %v", err)
	}
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
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "board member manager permission required" || forbidden.Err.Retryable {
		t.Fatalf("set board member without manager permission = %+v, want terminal manager forbidden", forbidden)
	}
	managerDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-manager-denied-permissions",
		Command:    proto.CmdSetBoardMember,
		Payload:    memberPayload,
		EnqueuedAt: 1234,
	})
	if managerDenied.Err == nil || managerDenied.Err.Code != proto.ErrForbidden ||
		managerDenied.Err.Message != "board moderator role required to change member permissions" || managerDenied.Err.Retryable {
		t.Fatalf("delegated manager changing permissions = %+v, want terminal moderator forbidden", managerDenied)
	}
	longTitle := strings.Repeat("x", 81)
	longTitlePayload, err := json.Marshal(proto.SetBoardMemberPayload{
		Board:  "general",
		User:   bob.Name,
		Member: true,
		Title:  longTitle,
	})
	if err != nil {
		t.Fatalf("marshal long board member title payload: %v", err)
	}
	longTitleReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-board-member-long-title",
		Command:    proto.CmdSetBoardMember,
		Payload:    longTitlePayload,
		EnqueuedAt: 1234,
	})
	if longTitleReply.Err == nil || longTitleReply.Err.Code != proto.ErrValidationFailed ||
		longTitleReply.Err.Message != "member title must be 80 characters or less" || longTitleReply.Err.Retryable {
		t.Fatalf("long board member title = %+v, want terminal title validation", longTitleReply)
	}

	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-member-add",
		Command:    proto.CmdSetBoardMember,
		Payload:    memberPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce board member add command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board member add once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
		t.Fatalf("board member add committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board member events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtBoardMemberSet {
		t.Fatalf("board member events = %+v, want one board.member_set", events)
	}
	memberEvent, ok := events[0].Payload.(*proto.BoardMemberSetPayload)
	if !ok {
		t.Fatalf("board member payload = %T, want BoardMemberSetPayload", events[0].Payload)
	}
	if memberEvent.Board != "general" || memberEvent.User != bob.ID || !memberEvent.Member ||
		memberEvent.Title != "Curator" || memberEvent.Position != 3 ||
		!memberEvent.CanCurate || !memberEvent.CanSetBoardSettings ||
		memberEvent.CanManageMembers || memberEvent.CanModeratePosts || memberEvent.CanModerateThreads ||
		memberEvent.CanAnnounce || memberEvent.CanManagePolls ||
		memberEvent.By != admin.ID || memberEvent.TS != 2234 {
		t.Fatalf("board member event = %+v, want deterministic final membership", memberEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-member-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board member add event: %v", err)
	}
	info, err := c.GetBoardInfo("general")
	if err != nil {
		t.Fatalf("get board info after member add: %v", err)
	}
	var projectedBob *BoardMember
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
	retryMember, ok := retryEvents[0].Payload.(*proto.BoardMemberSetPayload)
	if !ok {
		t.Fatalf("board member add retry payload = %T, want BoardMemberSetPayload", retryEvents[0].Payload)
	}
	if retryMember.Board != memberEvent.Board || retryMember.User != memberEvent.User ||
		retryMember.Member != memberEvent.Member || retryMember.Title != memberEvent.Title ||
		retryMember.Position != memberEvent.Position ||
		retryMember.CanCurate != memberEvent.CanCurate ||
		retryMember.CanSetBoardSettings != memberEvent.CanSetBoardSettings ||
		retryMember.By != memberEvent.By || retryMember.TS != memberEvent.TS {
		t.Fatalf("board member add retry payload = %+v, want stable payload %+v", retryMember, memberEvent)
	}

	managerDeniedPrivilegedRemovePayload, err := json.Marshal(proto.SetBoardMemberPayload{
		Board:  "general",
		User:   bob.Name,
		Member: false,
	})
	if err != nil {
		t.Fatalf("marshal delegated remove payload: %v", err)
	}
	managerDeniedPrivileged := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-member-manager-denied-privileged",
		Command:    proto.CmdSetBoardMember,
		Payload:    managerDeniedPrivilegedRemovePayload,
		EnqueuedAt: 1234,
	})
	if managerDeniedPrivileged.Err == nil || managerDeniedPrivileged.Err.Code != proto.ErrForbidden ||
		managerDeniedPrivileged.Err.Message != "board moderator role required to manage delegated board members" || managerDeniedPrivileged.Err.Retryable {
		t.Fatalf("delegated manager removing privileged member = %+v, want terminal moderator forbidden", managerDeniedPrivileged)
	}

	removePayload, err := json.Marshal(proto.SetBoardMemberPayload{
		Board:  "general",
		User:   bob.ID,
		Member: false,
	})
	if err != nil {
		t.Fatalf("marshal board member remove payload: %v", err)
	}
	removeRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-board-member-remove",
		Command:    proto.CmdSetBoardMember,
		Payload:    removePayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce board member remove command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board member remove once: %v", err)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board member events after remove: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtBoardMemberSet {
		t.Fatalf("board member events after remove = %+v, want add then remove", events)
	}
	removeEvent, ok := events[1].Payload.(*proto.BoardMemberSetPayload)
	if !ok {
		t.Fatalf("board member remove payload = %T, want BoardMemberSetPayload", events[1].Payload)
	}
	if removeEvent.Board != "general" || removeEvent.User != bob.ID || removeEvent.Member ||
		removeEvent.Title != "" || removeEvent.Position != 0 ||
		removeEvent.CanCurate || removeEvent.CanSetBoardSettings ||
		removeEvent.By != admin.ID || removeEvent.TS != 3234 {
		t.Fatalf("board member remove event = %+v, want deterministic final removal", removeEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-member-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board member remove event: %v", err)
	}
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
	if len(removeRetryEvents) != 1 || removeRetryEvents[0].Kind != proto.EvtBoardMemberSet {
		t.Fatalf("board member remove retry events = %+v, want stable removal event", removeRetryEvents)
	}
	removeRetry, ok := removeRetryEvents[0].Payload.(*proto.BoardMemberSetPayload)
	if !ok {
		t.Fatalf("board member remove retry payload = %T, want BoardMemberSetPayload", removeRetryEvents[0].Payload)
	}
	if removeRetry.User != removeEvent.User || removeRetry.Member != removeEvent.Member ||
		removeRetry.Title != removeEvent.Title || removeRetry.Position != removeEvent.Position ||
		removeRetry.TS != removeEvent.TS {
		t.Fatalf("board member remove retry payload = %+v, want stable removal %+v", removeRetry, removeEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardMembershipLeave(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	if _, err := c.RegisterUser("admin", "pw"); err != nil {
		t.Fatalf("register admin: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	if err := setBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{Title: "Resident"}); err != nil {
		t.Fatalf("seed bob board membership: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	emptyPayload, err := json.Marshal(proto.LeaveBoardMembershipPayload{Board: " "})
	if err != nil {
		t.Fatalf("marshal empty leave membership payload: %v", err)
	}
	emptyReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: partitionGlobal},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-leave-membership-empty",
		Command:    proto.CmdLeaveBoardMembership,
		Payload:    emptyPayload,
		EnqueuedAt: 1234,
	})
	if emptyReply.Err == nil || emptyReply.Err.Code != proto.ErrValidationFailed ||
		emptyReply.Err.Message != "board is required" || emptyReply.Err.Retryable {
		t.Fatalf("leave membership empty board = %+v, want terminal board validation", emptyReply)
	}

	missingPayload, err := json.Marshal(proto.LeaveBoardMembershipPayload{Board: "missing"})
	if err != nil {
		t.Fatalf("marshal missing board leave membership payload: %v", err)
	}
	missingReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-leave-membership-missing-board",
		Command:    proto.CmdLeaveBoardMembership,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	if missingReply.Err == nil || missingReply.Err.Code != proto.ErrNotFound ||
		missingReply.Err.Message != "board not found" || missingReply.Err.Retryable {
		t.Fatalf("leave membership missing board = %+v, want terminal not found", missingReply)
	}

	leavePayload, err := json.Marshal(proto.LeaveBoardMembershipPayload{Board: " general "})
	if err != nil {
		t.Fatalf("marshal leave membership payload: %v", err)
	}
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    bob.ID,
		CID:        "cid-native-leave-membership",
		Command:    proto.CmdLeaveBoardMembership,
		Payload:    leavePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce leave membership command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain leave membership once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
		t.Fatalf("leave membership committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay leave membership events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtBoardMemberSet {
		t.Fatalf("leave membership events = %+v, want one board.member_set", events)
	}
	leaveEvent, ok := events[0].Payload.(*proto.BoardMemberSetPayload)
	if !ok {
		t.Fatalf("leave membership payload = %T, want BoardMemberSetPayload", events[0].Payload)
	}
	if leaveEvent.Board != "general" || leaveEvent.User != bob.ID || leaveEvent.Member ||
		leaveEvent.Title != "" || leaveEvent.Position != 0 ||
		leaveEvent.CanManageMembers || leaveEvent.CanCurate || leaveEvent.CanModeratePosts ||
		leaveEvent.CanModerateThreads || leaveEvent.CanAnnounce || leaveEvent.CanManagePolls ||
		leaveEvent.CanSetBoardSettings || leaveEvent.By != bob.ID || leaveEvent.TS != 2234 {
		t.Fatalf("leave membership event = %+v, want deterministic final removal", leaveEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-membership-leave-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize leave membership event: %v", err)
	}
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
	retryLeave, ok := retryEvents[0].Payload.(*proto.BoardMemberSetPayload)
	if !ok {
		t.Fatalf("leave membership retry payload = %T, want BoardMemberSetPayload", retryEvents[0].Payload)
	}
	if retryLeave.Board != leaveEvent.Board || retryLeave.User != leaveEvent.User ||
		retryLeave.Member != leaveEvent.Member || retryLeave.By != leaveEvent.By ||
		retryLeave.TS != leaveEvent.TS {
		t.Fatalf("leave membership retry payload = %+v, want stable removal %+v", retryLeave, leaveEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardMembershipApplication(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	if _, err := c.RegisterUser("admin", "pw"); err != nil {
		t.Fatalf("register admin: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	manualPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	longNotePayload, err := json.Marshal(proto.ApplyBoardMembershipPayload{
		Board: "general",
		Note:  strings.Repeat("x", 501),
	})
	if err != nil {
		t.Fatalf("marshal long membership note payload: %v", err)
	}
	longNote := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  manualPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-apply-membership-long-note",
		Command:    proto.CmdApplyBoardMembership,
		Payload:    longNotePayload,
		EnqueuedAt: 1234,
	})
	if longNote.Err == nil || longNote.Err.Code != proto.ErrValidationFailed ||
		longNote.Err.Message != "application note must be 500 characters or less" || longNote.Err.Retryable {
		t.Fatalf("apply membership long note = %+v, want terminal note validation", longNote)
	}

	manualPayload, err := json.Marshal(proto.ApplyBoardMembershipPayload{
		Board: " general ",
		Note:  " I read this board daily. ",
	})
	if err != nil {
		t.Fatalf("marshal manual membership application payload: %v", err)
	}
	manualRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  manualPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-apply-membership-manual",
		Command:    proto.CmdApplyBoardMembership,
		Payload:    manualPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce manual membership application command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain manual membership application once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, manualPartition); err != nil || got != manualRecord.Offset {
		t.Fatalf("manual membership committed offset = %d, %v; want %d, nil", got, err, manualRecord.Offset)
	}
	manualEvents, err := eventStore.ReplayPartition(ctx, manualPartition.Kind, manualPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay manual membership events: %v", err)
	}
	if len(manualEvents) != 1 || manualEvents[0].Kind != proto.EvtBoardMemberApplicationSubmitted {
		t.Fatalf("manual membership events = %+v, want one application submitted event", manualEvents)
	}
	manualEvent, ok := manualEvents[0].Payload.(*proto.BoardMemberApplicationSubmittedPayload)
	if !ok {
		t.Fatalf("manual membership payload = %T, want BoardMemberApplicationSubmittedPayload", manualEvents[0].Payload)
	}
	manualAppID := stableCommandLogDecisionID("bmap_", manualRecord, 0)
	if manualEvent.ID != manualAppID || manualEvent.Board != "general" ||
		manualEvent.User != bob.ID || manualEvent.Note != "I read this board daily." ||
		manualEvent.TS != 2234 {
		t.Fatalf("manual membership event = %+v, want deterministic submitted application", manualEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-membership-application-test",
		Partition: manualPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize manual membership application event: %v", err)
	}
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
	if duplicateReply.Err == nil || duplicateReply.Err.Code != proto.ErrConflict ||
		duplicateReply.Err.Message != "membership application already pending" || duplicateReply.Err.Retryable {
		t.Fatalf("duplicate manual membership application = %+v, want pending conflict", duplicateReply)
	}

	if _, err := qExec(c.DB,
		`INSERT INTO boards (id, name, description) VALUES (?, ?, ?)`,
		"autoclub", "Auto Club", "Auto-approved residents",
	); err != nil {
		t.Fatalf("seed auto board: %v", err)
	}
	approvalMode := "auto"
	if err := setBoardMemberRequirements(c.DB, "autoclub", BoardMemberRequirementsPatch{
		ApprovalMode: &approvalMode,
	}); err != nil {
		t.Fatalf("seed auto approval requirements: %v", err)
	}
	autoPartition := LogPartition{Kind: partitionBoard, Key: "autoclub"}
	autoPayload, err := json.Marshal(proto.ApplyBoardMembershipPayload{
		Board: "autoclub",
		Note:  "private note",
	})
	if err != nil {
		t.Fatalf("marshal auto membership application payload: %v", err)
	}
	autoRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  autoPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-apply-membership-auto",
		Command:    proto.CmdApplyBoardMembership,
		Payload:    autoPayload,
		EnqueuedAt: 4234,
	})
	if err != nil {
		t.Fatalf("produce auto membership application command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain auto membership application once: %v", err)
	}
	autoEvents, err := eventStore.ReplayPartition(ctx, autoPartition.Kind, autoPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay auto membership events: %v", err)
	}
	if len(autoEvents) != 2 ||
		autoEvents[0].Kind != proto.EvtBoardMemberApplicationSubmitted ||
		autoEvents[1].Kind != proto.EvtBoardMemberApplicationReviewed {
		t.Fatalf("auto membership events = %+v, want submitted plus reviewed", autoEvents)
	}
	autoSubmit, ok := autoEvents[0].Payload.(*proto.BoardMemberApplicationSubmittedPayload)
	if !ok {
		t.Fatalf("auto membership submitted payload = %T, want BoardMemberApplicationSubmittedPayload", autoEvents[0].Payload)
	}
	autoReview, ok := autoEvents[1].Payload.(*proto.BoardMemberApplicationReviewedPayload)
	if !ok {
		t.Fatalf("auto membership reviewed payload = %T, want BoardMemberApplicationReviewedPayload", autoEvents[1].Payload)
	}
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
	registryPartition := LogPartition{Kind: partitionBoard, Key: nativeRegistrySystemBoardID}
	registryEvents, err := eventStore.ReplayPartition(ctx, registryPartition.Kind, registryPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay registry membership events: %v", err)
	}
	if len(registryEvents) != 3 ||
		registryEvents[0].Kind != proto.EvtBoardCreated ||
		registryEvents[1].Kind != proto.EvtThreadNew ||
		registryEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("registry membership events = %+v, want board/thread/post", registryEvents)
	}
	registryPost, ok := registryEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("registry post payload = %T, want PostAppendedPayload", registryEvents[2].Payload)
	}
	if !strings.Contains(registryPost.Body, "Status: approved") ||
		!strings.Contains(registryPost.Body, "Board: Auto Club (autoclub)") ||
		!strings.Contains(registryPost.Body, "Applicant: bob") ||
		!strings.Contains(registryPost.Body, "Reviewer: bob") ||
		strings.Contains(registryPost.Body, "private note") {
		t.Fatalf("registry post body = %q, want sanitized auto approval log", registryPost.Body)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-membership-application-test",
		Partition: autoPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize auto membership events: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-membership-application-test",
		Partition: registryPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize registry membership events: %v", err)
	}
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
	if registryThread == nil || registryThread.Board != nativeRegistrySystemBoardID {
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	manager := true
	if err := setBoardMember(c.DB, "general", alice.ID, true, BoardMemberPatch{CanManageMembers: &manager}); err != nil {
		t.Fatalf("seed delegated board member manager: %v", err)
	}
	if err := insertBoardMemberApplication(c.DB, "app_review_bob", "general", bob.ID, "private application note"); err != nil {
		t.Fatalf("insert bob membership application: %v", err)
	}
	if err := insertBoardMemberApplication(c.DB, "app_review_alice", "general", alice.ID, "self application note"); err != nil {
		t.Fatalf("insert alice membership application: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	reviewPartition := LogPartition{Kind: partitionReview, Key: "app_review_bob"}
	approvePayload, err := json.Marshal(proto.ReviewBoardMembershipPayload{
		Application: " app_review_bob ",
		Status:      "approve",
		Title:       " resident ",
		Note:        " welcome aboard ",
	})
	if err != nil {
		t.Fatalf("marshal review approval payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-review-membership-forbidden",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    approvePayload,
		EnqueuedAt: 1234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden ||
		forbidden.Err.Message != "board member manager permission required" || forbidden.Err.Retryable {
		t.Fatalf("review membership without manager permission = %+v, want terminal manager forbidden", forbidden)
	}
	selfPayload, err := json.Marshal(proto.ReviewBoardMembershipPayload{
		Application: "app_review_alice",
		Status:      "approved",
	})
	if err != nil {
		t.Fatalf("marshal self review payload: %v", err)
	}
	selfReview := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionReview, Key: "app_review_alice"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-review-membership-self",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    selfPayload,
		EnqueuedAt: 1234,
	})
	if selfReview.Err == nil || selfReview.Err.Code != proto.ErrForbidden ||
		selfReview.Err.Message != "board moderator role required to review your own application" || selfReview.Err.Retryable {
		t.Fatalf("delegated manager self review = %+v, want terminal self-review forbidden", selfReview)
	}
	blacklistPayload, err := json.Marshal(proto.ReviewBoardMembershipPayload{
		Application: "app_review_bob",
		Status:      "blacklisted",
	})
	if err != nil {
		t.Fatalf("marshal blacklist review payload: %v", err)
	}
	blacklistDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-review-membership-blacklist-denied",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    blacklistPayload,
		EnqueuedAt: 1234,
	})
	if blacklistDenied.Err == nil || blacklistDenied.Err.Code != proto.ErrForbidden ||
		blacklistDenied.Err.Message != "board moderator role required to blacklist membership applications" || blacklistDenied.Err.Retryable {
		t.Fatalf("delegated manager blacklist review = %+v, want terminal moderator forbidden", blacklistDenied)
	}
	longTitlePayload, err := json.Marshal(proto.ReviewBoardMembershipPayload{
		Application: "app_review_bob",
		Status:      "approved",
		Title:       strings.Repeat("x", 81),
	})
	if err != nil {
		t.Fatalf("marshal long title review payload: %v", err)
	}
	longTitle := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-review-membership-long-title",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    longTitlePayload,
		EnqueuedAt: 1234,
	})
	if longTitle.Err == nil || longTitle.Err.Code != proto.ErrValidationFailed ||
		longTitle.Err.Message != "member title must be 80 characters or less" || longTitle.Err.Retryable {
		t.Fatalf("long review title = %+v, want terminal title validation", longTitle)
	}
	badStatusPayload, err := json.Marshal(proto.ReviewBoardMembershipPayload{
		Application: "app_review_bob",
		Status:      "maybe",
	})
	if err != nil {
		t.Fatalf("marshal bad status review payload: %v", err)
	}
	badStatus := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-review-membership-bad-status",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    badStatusPayload,
		EnqueuedAt: 1234,
	})
	if badStatus.Err == nil || badStatus.Err.Code != proto.ErrValidationFailed ||
		badStatus.Err.Message != `status must be "approved", "rejected", or "blacklisted"` || badStatus.Err.Retryable {
		t.Fatalf("bad review status = %+v, want terminal status validation", badStatus)
	}

	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-review-membership-approve",
		Command:    proto.CmdReviewBoardMembership,
		Payload:    approvePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce review membership command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain review membership once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, reviewPartition); err != nil || got != record.Offset {
		t.Fatalf("review membership committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	boardEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay review board events: %v", err)
	}
	if len(boardEvents) != 1 || boardEvents[0].Kind != proto.EvtBoardMemberApplicationReviewed {
		t.Fatalf("review board events = %+v, want one reviewed event", boardEvents)
	}
	reviewEvent, ok := boardEvents[0].Payload.(*proto.BoardMemberApplicationReviewedPayload)
	if !ok {
		t.Fatalf("review event payload = %T, want BoardMemberApplicationReviewedPayload", boardEvents[0].Payload)
	}
	if reviewEvent.Application != "app_review_bob" || reviewEvent.Board != "general" ||
		reviewEvent.User != bob.ID || reviewEvent.Status != "approved" ||
		reviewEvent.Title != "resident" || reviewEvent.Reviewer != alice.ID ||
		reviewEvent.ReviewNote != "welcome aboard" || reviewEvent.TS != 2234 {
		t.Fatalf("review event = %+v, want deterministic approval", reviewEvent)
	}
	registryPartition := LogPartition{Kind: partitionBoard, Key: nativeRegistrySystemBoardID}
	registryEvents, err := eventStore.ReplayPartition(ctx, registryPartition.Kind, registryPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay review registry events: %v", err)
	}
	if len(registryEvents) != 3 ||
		registryEvents[0].Kind != proto.EvtBoardCreated ||
		registryEvents[1].Kind != proto.EvtThreadNew ||
		registryEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("review registry events = %+v, want board/thread/post", registryEvents)
	}
	registryPost, ok := registryEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("review registry post payload = %T, want PostAppendedPayload", registryEvents[2].Payload)
	}
	if !strings.Contains(registryPost.Body, "Status: approved") ||
		!strings.Contains(registryPost.Body, "Board: General (general)") ||
		!strings.Contains(registryPost.Body, "Applicant: bob") ||
		!strings.Contains(registryPost.Body, "Reviewer: alice") ||
		strings.Contains(registryPost.Body, "private application note") ||
		strings.Contains(registryPost.Body, "welcome aboard") ||
		strings.Contains(registryPost.Body, "resident") {
		t.Fatalf("review registry post body = %q, want sanitized approval log", registryPost.Body)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-membership-review-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize review board event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-membership-review-test",
		Partition: registryPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize review registry events: %v", err)
	}
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
	var bobMember *BoardMember
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
	retryReview, ok := retryEvents[0].Payload.(*proto.BoardMemberApplicationReviewedPayload)
	if !ok {
		t.Fatalf("review retry payload = %T, want BoardMemberApplicationReviewedPayload", retryEvents[0].Payload)
	}
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
	if duplicate.Err == nil || duplicate.Err.Code != proto.ErrConflict ||
		duplicate.Err.Message != "membership application is already reviewed" || duplicate.Err.Retryable {
		t.Fatalf("duplicate review membership command = %+v, want already-reviewed conflict", duplicate)
	}
}

func TestNativeCommandLogDecisionExecutorDrainsBasicCreateThreadThroughBrokerEvents(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	payload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native hello",
		Body:  "first post from broker decision",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread",
		Command:    proto.CmdCreateThread,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	if err != nil {
		t.Fatalf("produce command: %v", err)
	}
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

	results, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("drain once: %v", err)
	}
	if len(results) != 1 || results[0].Processed != 1 || results[0].Applied != 1 || results[0].LastOffset != record.Offset {
		t.Fatalf("results = %+v, want one native decision committed", results)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
		t.Fatalf("committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay broker events: %v", err)
	}
	if len(events) != 2 || events[0].Kind != proto.EvtThreadNew || events[1].Kind != proto.EvtPostAppended {
		t.Fatalf("events = %+v, want thread.new then post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	postPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("post event payload = %T, want PostAppendedPayload", events[1].Payload)
	}
	if threadPayload.ID == "" || postPayload.Thread != threadPayload.ID || threadPayload.Title != "Broker-native hello" {
		t.Fatalf("thread/post payloads = %+v / %+v, want linked broker-native thread", threadPayload, postPayload)
	}
	if events[0].PartitionOffset != 1 || events[1].PartitionOffset != 2 {
		t.Fatalf("event offsets = %d,%d; want 1,2", events[0].PartitionOffset, events[1].PartitionOffset)
	}
	materialized, err := getThread(c.DB, threadPayload.ID)
	if err != nil {
		t.Fatalf("get materialized thread: %v", err)
	}
	if materialized != nil {
		t.Fatalf("materialized thread = %+v, want broker decision to leave SQL projections untouched", materialized)
	}

	materializedResult, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-create-thread-test",
		Partition: partition,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("materialize broker event partition: %v", err)
	}
	if materializedResult.StartedOffset != 0 || materializedResult.LastOffset != 2 || materializedResult.Applied != 2 {
		t.Fatalf("materialization result = %+v, want offsets 0->2 with two applied events", materializedResult)
	}
	materialized, err = getThread(c.DB, threadPayload.ID)
	if err != nil {
		t.Fatalf("get materialized thread after event-store projection: %v", err)
	}
	if materialized == nil || materialized.Title != "Broker-native hello" || materialized.PostCount != 1 || materialized.LastSeq != 2 {
		t.Fatalf("materialized thread = %+v, want broker-projected thread with one post", materialized)
	}
	post, err := getPost(c.DB, postPayload.ID)
	if err != nil {
		t.Fatalf("get materialized post: %v", err)
	}
	if post == nil || post.Thread != threadPayload.ID || post.Body != "first post from broker decision" || post.AuthorID != alice.ID {
		t.Fatalf("materialized post = %+v, want broker-projected first post", post)
	}
	secondResult, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-create-thread-test",
		Partition: partition,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("second materialize broker event partition: %v", err)
	}
	if secondResult.StartedOffset != 2 || secondResult.LastOffset != 2 || secondResult.Applied != 0 {
		t.Fatalf("second materialization result = %+v, want checkpointed no-op", secondResult)
	}

	replyPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "broker-native reply",
	})
	if err != nil {
		t.Fatalf("marshal reply payload: %v", err)
	}
	replyRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post",
		Command:    proto.CmdAppendPost,
		Payload:    replyPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce reply command: %v", err)
	}
	replyResults, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("drain reply once: %v", err)
	}
	var replyProcessed, replyApplied int
	for _, result := range replyResults {
		replyProcessed += result.Processed
		replyApplied += result.Applied
	}
	if replyProcessed != 1 || replyApplied != 1 {
		t.Fatalf("reply results = %+v, want one native appendPost decision committed", replyResults)
	}
	if got, err := commandLog.CommittedOffset(ctx, LogPartition{Kind: partitionThread, Key: threadPayload.ID}); err != nil || got != replyRecord.Offset {
		t.Fatalf("reply committed offset = %d, %v; want %d, nil", got, err, replyRecord.Offset)
	}
	replyEvents, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 2, 10)
	if err != nil {
		t.Fatalf("replay broker reply event: %v", err)
	}
	if len(replyEvents) != 1 || replyEvents[0].Kind != proto.EvtPostAppended || replyEvents[0].PartitionOffset != 3 {
		t.Fatalf("reply events = %+v, want one post.appended at board offset 3", replyEvents)
	}
	replyPostPayload, ok := replyEvents[0].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("reply payload = %T, want PostAppendedPayload", replyEvents[0].Payload)
	}
	replyMaterialized, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-create-thread-test",
		Partition: partition,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("materialize broker reply event partition: %v", err)
	}
	if replyMaterialized.StartedOffset != 2 || replyMaterialized.LastOffset != 3 || replyMaterialized.Applied != 1 {
		t.Fatalf("reply materialization result = %+v, want offsets 2->3 with one applied event", replyMaterialized)
	}
	replyPost, err := getPost(c.DB, replyPostPayload.ID)
	if err != nil {
		t.Fatalf("get materialized reply: %v", err)
	}
	if replyPost == nil || replyPost.Thread != threadPayload.ID || replyPost.Body != "broker-native reply" || replyPost.AuthorID != alice.ID {
		t.Fatalf("materialized reply = %+v, want broker-projected reply", replyPost)
	}
	materialized, err = getThread(c.DB, threadPayload.ID)
	if err != nil {
		t.Fatalf("get materialized thread after reply: %v", err)
	}
	if materialized == nil || materialized.PostCount != 2 || materialized.LastSeq != 3 {
		t.Fatalf("materialized thread after reply = %+v, want two posts at last seq 3", materialized)
	}

	directedPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread:  threadPayload.ID,
		ReplyTo: postPayload.ID,
		Body:    "broker-native directed reply",
	})
	if err != nil {
		t.Fatalf("marshal directed reply payload: %v", err)
	}
	directedRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    bob.ID,
		CID:        "cid-native-directed-reply",
		Command:    proto.CmdAppendPost,
		Payload:    directedPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce directed reply command: %v", err)
	}
	directedResults, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("drain directed reply once: %v", err)
	}
	var directedProcessed, directedApplied int
	for _, result := range directedResults {
		directedProcessed += result.Processed
		directedApplied += result.Applied
	}
	if directedProcessed != 1 || directedApplied != 1 {
		t.Fatalf("directed reply results = %+v, want one native directed appendPost decision committed", directedResults)
	}
	if got, err := commandLog.CommittedOffset(ctx, LogPartition{Kind: partitionThread, Key: threadPayload.ID}); err != nil || got != directedRecord.Offset {
		t.Fatalf("directed reply committed offset = %d, %v; want %d, nil", got, err, directedRecord.Offset)
	}
	directedEvents, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 3, 10)
	if err != nil {
		t.Fatalf("replay broker directed reply event: %v", err)
	}
	if len(directedEvents) != 1 || directedEvents[0].Kind != proto.EvtPostAppended || directedEvents[0].PartitionOffset != 4 {
		t.Fatalf("directed reply events = %+v, want one post.appended at board offset 4", directedEvents)
	}
	directedPostPayload, ok := directedEvents[0].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("directed reply payload = %T, want PostAppendedPayload", directedEvents[0].Payload)
	}
	if directedPostPayload.ReplyTo != postPayload.ID {
		t.Fatalf("directed reply event ReplyTo = %q, want root post %q", directedPostPayload.ReplyTo, postPayload.ID)
	}
	directedMaterialized, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-create-thread-test",
		Partition: partition,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("materialize broker directed reply event partition: %v", err)
	}
	if directedMaterialized.StartedOffset != 3 || directedMaterialized.LastOffset != 4 || directedMaterialized.Applied != 1 {
		t.Fatalf("directed materialization result = %+v, want offsets 3->4 with one applied event", directedMaterialized)
	}
	directedPost, err := getPost(c.DB, directedPostPayload.ID)
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

	nestedPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread:  threadPayload.ID,
		ReplyTo: directedPostPayload.ID,
		Body:    "broker-native nested reply",
	})
	if err != nil {
		t.Fatalf("marshal nested directed reply payload: %v", err)
	}
	nestedRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-nested-directed-reply",
		Command:    proto.CmdAppendPost,
		Payload:    nestedPayload,
		EnqueuedAt: 4234,
	})
	if err != nil {
		t.Fatalf("produce nested directed reply command: %v", err)
	}
	nestedResults, err := worker.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("drain nested directed reply once: %v", err)
	}
	var nestedApplied int
	for _, result := range nestedResults {
		nestedApplied += result.Applied
	}
	if nestedApplied != 1 {
		t.Fatalf("nested directed reply results = %+v, want one native appendPost decision committed", nestedResults)
	}
	if got, err := commandLog.CommittedOffset(ctx, LogPartition{Kind: partitionThread, Key: threadPayload.ID}); err != nil || got != nestedRecord.Offset {
		t.Fatalf("nested directed reply committed offset = %d, %v; want %d, nil", got, err, nestedRecord.Offset)
	}
	nestedEvents, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 4, 10)
	if err != nil {
		t.Fatalf("replay broker nested directed reply event: %v", err)
	}
	if len(nestedEvents) != 1 || nestedEvents[0].Kind != proto.EvtPostAppended || nestedEvents[0].PartitionOffset != 5 {
		t.Fatalf("nested directed reply events = %+v, want one post.appended at board offset 5", nestedEvents)
	}
	nestedPostPayload, ok := nestedEvents[0].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("nested directed reply payload = %T, want PostAppendedPayload", nestedEvents[0].Payload)
	}
	if nestedPostPayload.ReplyTo != postPayload.ID {
		t.Fatalf("nested directed reply event ReplyTo = %q, want flattened root post %q", nestedPostPayload.ReplyTo, postPayload.ID)
	}
}

func TestNativeCommandLogDecisionExecutorReusesExecutedDecisionForEvents(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	if _, err := c.RegisterUser("admin", "pw"); err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	executor := NewCommandLogNativeDecisionExecutor(c)
	payload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Cached native decision",
		Body:  "finalization should not re-read mutable board policy",
	})
	if err != nil {
		t.Fatalf("marshal create thread payload: %v", err)
	}
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
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{ReadOnly: &readOnly}); err != nil {
		t.Fatalf("set board read-only: %v", err)
	}

	events, err := executor.DecideCommandLogEvents(ctx, record, reply)
	if err != nil {
		t.Fatalf("decide cached create thread events: %v", err)
	}
	if len(events) != 2 || events[0].Kind != proto.EvtThreadNew || events[1].Kind != proto.EvtPostAppended {
		t.Fatalf("cached events = %+v, want thread.new then post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("cached thread payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	if reply.Result.ID != threadPayload.ID {
		t.Fatalf("reply result id = %q, cached thread id = %q; want match", reply.Result.ID, threadPayload.ID)
	}

	_, err = executor.DecideCommandLogEvents(ctx, record, reply)
	if err == nil || !strings.Contains(err.Error(), "board is read-only") {
		t.Fatalf("second decide events error = %v, want cache consumed and read-only redecision failure", err)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsPostBoardMail(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	if _, err := c.RegisterUser("admin", "pw"); err != nil {
		t.Fatalf("register admin: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	expectMailErr := func(partition LogPartition, payload proto.PostBoardMailPayload, wantCode, wantMessage string) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal postBoardMail payload: %v", err)
		}
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
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{MailInAllowed: &mailInAllowed}); err != nil {
		t.Fatalf("enable mail-in: %v", err)
	}
	createPayload, err := json.Marshal(proto.PostBoardMailPayload{
		Board:       "general",
		Subject:     " Mail thread ",
		Body:        " posted from mail ",
		ContentType: "ansi-art",
	})
	if err != nil {
		t.Fatalf("marshal create mail payload: %v", err)
	}
	createRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-board-mail-create",
		Command:    proto.CmdPostBoardMail,
		Payload:    createPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce create mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, boardPartition); err != nil || got != createRecord.Offset {
		t.Fatalf("create mail committed offset = %d, %v; want %d, nil", got, err, createRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create mail events: %v", err)
	}
	if len(events) != 2 || events[0].Kind != proto.EvtThreadNew || events[1].Kind != proto.EvtPostAppended {
		t.Fatalf("create mail events = %+v, want thread.new and post.appended", events)
	}
	threadEvent, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("create mail thread payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootEvent, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("create mail post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
	wantThreadID := stableCommandLogDecisionID("thr_", createRecord, 0)
	wantRootID := stableCommandLogDecisionID("pst_", createRecord, 1)
	if threadEvent.ID != wantThreadID || threadEvent.Board != "general" || threadEvent.Title != "Mail thread" || threadEvent.AuthorID != bob.ID || threadEvent.TS != 2234 {
		t.Fatalf("create mail thread = %+v, want deterministic mail-in thread", threadEvent)
	}
	if rootEvent.ID != wantRootID || rootEvent.Thread != wantThreadID || rootEvent.Body != "posted from mail" || rootEvent.RawBody != "posted from mail" ||
		rootEvent.AuthorID != bob.ID || rootEvent.ContentType != "ansi-art" || rootEvent.TS != 2234 {
		t.Fatalf("create mail root post = %+v, want deterministic mail-in root post", rootEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-post-board-mail-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create mail events: %v", err)
	}
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
	replyPayload, err := json.Marshal(proto.PostBoardMailPayload{
		Thread: wantThreadID,
		Body:   " mail reply ",
	})
	if err != nil {
		t.Fatalf("marshal reply mail payload: %v", err)
	}
	replyRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  replyPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-board-mail-reply",
		Command:    proto.CmdPostBoardMail,
		Payload:    replyPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce reply mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain reply mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, replyPartition); err != nil || got != replyRecord.Offset {
		t.Fatalf("reply mail committed offset = %d, %v; want %d, nil", got, err, replyRecord.Offset)
	}
	replyEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 2, 10)
	if err != nil {
		t.Fatalf("replay reply mail events: %v", err)
	}
	if len(replyEvents) != 1 || replyEvents[0].Kind != proto.EvtPostAppended || replyEvents[0].PartitionOffset != 3 {
		t.Fatalf("reply mail events = %+v, want one board-local post.appended", replyEvents)
	}
	replyEvent, ok := replyEvents[0].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("reply mail payload = %T, want PostAppendedPayload", replyEvents[0].Payload)
	}
	wantReplyID := stableCommandLogDecisionID("pst_", replyRecord, 0)
	if replyEvent.ID != wantReplyID || replyEvent.Thread != wantThreadID || replyEvent.Body != "mail reply" || replyEvent.RawBody != "mail reply" ||
		replyEvent.AuthorID != bob.ID || replyEvent.ContentType != "markup" || replyEvent.TS != 3234 {
		t.Fatalf("reply mail post = %+v, want deterministic mail-in reply", replyEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-post-board-mail-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize reply mail event: %v", err)
	}
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
	pollPayload, err := json.Marshal(proto.PostBoardMailPayload{
		Thread: wantThreadID,
		Body:   pollBody,
	})
	if err != nil {
		t.Fatalf("marshal poll mail payload: %v", err)
	}
	lowTrustPoll := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  replyPartition,
		Offset:     901,
		ActorID:    bob.ID,
		CID:        "cid-native-post-board-mail-poll-low-trust",
		Command:    proto.CmdPostBoardMail,
		Payload:    pollPayload,
		EnqueuedAt: 4234,
	})
	if lowTrustPoll.Err == nil || lowTrustPoll.Err.Code != proto.ErrForbidden || lowTrustPoll.Err.Retryable {
		t.Fatalf("low-trust poll mail reply = %+v, want terminal forbidden", lowTrustPoll)
	}
	if err := setNativeDecisionTestTrustLevel(c, bob.ID, 2); err != nil {
		t.Fatalf("raise bob trust level: %v", err)
	}
	pollRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  replyPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-board-mail-poll",
		Command:    proto.CmdPostBoardMail,
		Payload:    pollPayload,
		EnqueuedAt: 4234,
	})
	if err != nil {
		t.Fatalf("produce poll mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain poll mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, replyPartition); err != nil || got != pollRecord.Offset {
		t.Fatalf("poll mail committed offset = %d, %v; want %d, nil", got, err, pollRecord.Offset)
	}
	pollEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 3, 10)
	if err != nil {
		t.Fatalf("replay poll mail events: %v", err)
	}
	if len(pollEvents) != 1 || pollEvents[0].Kind != proto.EvtPostAppended || pollEvents[0].PartitionOffset != 4 {
		t.Fatalf("poll mail events = %+v, want one board-local post.appended", pollEvents)
	}
	pollEvent, ok := pollEvents[0].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("poll mail payload = %T, want PostAppendedPayload", pollEvents[0].Payload)
	}
	wantPollPostID := stableCommandLogDecisionID("pst_", pollRecord, 0)
	if pollEvent.ID != wantPollPostID || pollEvent.Thread != wantThreadID || pollEvent.Body != "" || pollEvent.RawBody != pollBody ||
		pollEvent.AuthorID != bob.ID || pollEvent.TS != 4234 {
		t.Fatalf("poll mail post = %+v, want stripped body and raw poll body", pollEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-post-board-mail-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize poll mail event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	sourceMailID := "mail_native_post_to_board_source"
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin source mail seed: %v", err)
	}
	if err := insertMailMessage(tx, sourceMailID, alice.ID, "Campus plans", "Meet in the lab at six.", "", 1734, 1); err != nil {
		tx.Rollback() //nolint:errcheck
		t.Fatalf("insert source mail message: %v", err)
	}
	if err := insertMailCopy(tx, sourceMailID, alice.ID, "sender", "sent", true, false, 1734); err != nil {
		tx.Rollback() //nolint:errcheck
		t.Fatalf("insert source sender copy: %v", err)
	}
	if err := insertMailCopy(tx, sourceMailID, bob.ID, "recipient", "inbox", false, false, 1734); err != nil {
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

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	invisiblePayload, err := json.Marshal(proto.PostMailToBoardPayload{
		Mail:  sourceMail.ID,
		Board: "general",
	})
	if err != nil {
		t.Fatalf("marshal invisible post-mail payload: %v", err)
	}
	invisible := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-post-mail-to-board-invisible",
		Command:    proto.CmdPostMailToBoard,
		Payload:    invisiblePayload,
		EnqueuedAt: 1234,
	})
	if invisible.Err == nil || invisible.Err.Code != proto.ErrNotFound || invisible.Err.Message != "mail not found" || invisible.Err.Retryable {
		t.Fatalf("invisible source mail reply = %+v, want terminal not_found", invisible)
	}

	createPayload, err := json.Marshal(proto.PostMailToBoardPayload{
		Mail:    sourceMail.ID,
		Board:   "general",
		Subject: " Shared campus mail ",
		Note:    "Please discuss this one.",
	})
	if err != nil {
		t.Fatalf("marshal create post-mail payload: %v", err)
	}
	createRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-mail-to-board-create",
		Command:    proto.CmdPostMailToBoard,
		Payload:    createPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce create post-mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create post-mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, boardPartition); err != nil || got != createRecord.Offset {
		t.Fatalf("create post-mail committed offset = %d, %v; want %d, nil", got, err, createRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create post-mail events: %v", err)
	}
	if len(events) != 2 || events[0].Kind != proto.EvtThreadNew || events[1].Kind != proto.EvtPostAppended {
		t.Fatalf("create post-mail events = %+v, want thread.new and post.appended", events)
	}
	threadEvent, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("create post-mail thread payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootEvent, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("create post-mail root payload = %T, want PostAppendedPayload", events[1].Payload)
	}
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
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-post-mail-to-board-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create post-mail events: %v", err)
	}
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
	replyPayload, err := json.Marshal(proto.PostMailToBoardPayload{
		Mail:   sourceMail.ID,
		Thread: wantThreadID,
		Note:   "Follow-up from mail.",
	})
	if err != nil {
		t.Fatalf("marshal reply post-mail payload: %v", err)
	}
	replyRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  replyPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-mail-to-board-reply",
		Command:    proto.CmdPostMailToBoard,
		Payload:    replyPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce reply post-mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain reply post-mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, replyPartition); err != nil || got != replyRecord.Offset {
		t.Fatalf("reply post-mail committed offset = %d, %v; want %d, nil", got, err, replyRecord.Offset)
	}
	replyEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 2, 10)
	if err != nil {
		t.Fatalf("replay reply post-mail events: %v", err)
	}
	if len(replyEvents) != 1 || replyEvents[0].Kind != proto.EvtPostAppended || replyEvents[0].PartitionOffset != 3 {
		t.Fatalf("reply post-mail events = %+v, want one board-local post.appended", replyEvents)
	}
	replyEvent, ok := replyEvents[0].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("reply post-mail payload = %T, want PostAppendedPayload", replyEvents[0].Payload)
	}
	wantReplyID := stableCommandLogDecisionID("pst_", replyRecord, 0)
	if replyEvent.ID != wantReplyID || replyEvent.Thread != wantThreadID || replyEvent.AuthorID != bob.ID ||
		replyEvent.ContentType != "markup" || replyEvent.TS != 3234 || !strings.Contains(replyEvent.Body, "Follow-up from mail.") {
		t.Fatalf("reply post-mail post = %+v, want deterministic mail board reply", replyEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-post-mail-to-board-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize reply post-mail event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}
	if err := setBoardModerator(c.DB, "general", alice.ID, alice.ID, true, nil); err != nil {
		t.Fatalf("set alice board moderator: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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
	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	expectNativeErr := func(actor *User, partition LogPartition, command proto.CommandName, payload any, wantCode string) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s payload: %v", command, err)
		}
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
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s payload: %v", command, err)
		}
		record, err := commandLog.Produce(ctx, CommandLogRecord{
			Partition:  partition,
			ActorID:    actor.ID,
			CID:        cid,
			Command:    command,
			Payload:    raw,
			EnqueuedAt: ts,
		})
		if err != nil {
			t.Fatalf("produce %s: %v", command, err)
		}
		if _, err := worker.DrainOnce(ctx); err != nil {
			t.Fatalf("drain %s: %v", command, err)
		}
		if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
			t.Fatalf("%s committed offset = %d, %v; want %d, nil", command, got, err, record.Offset)
		}
		return record
	}
	materializeBoard := func(wantApplied int) []*proto.Event {
		t.Helper()
		result, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
			Source:    "native-board-policy-test",
			Partition: boardPartition,
			Limit:     100,
		})
		if err != nil {
			t.Fatalf("materialize board policy events: %v", err)
		}
		if result.Applied != wantApplied {
			t.Fatalf("materialization result = %+v, want %d applied events", result, wantApplied)
		}
		events, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 100)
		if err != nil {
			t.Fatalf("replay board policy events: %v", err)
		}
		return events
	}

	readOnly := true
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{ReadOnly: &readOnly}); err != nil {
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
	if len(events) != 2 || events[0].Kind != proto.EvtThreadNew || events[1].Kind != proto.EvtPostAppended {
		t.Fatalf("events after read-only create = %+v, want thread.new and post.appended", events)
	}
	threadEvent, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootEvent, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root event payload = %T, want PostAppendedPayload", events[1].Payload)
	}
	if threadEvent.AuthorID != alice.ID || rootEvent.AuthorID != alice.ID {
		t.Fatalf("read-only moderator event authors = %+v / %+v, want alice", threadEvent, rootEvent)
	}

	threadPartition := LogPartition{Kind: partitionThread, Key: threadEvent.ID}
	noReply := true
	readOnly = false
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{ReadOnly: &readOnly, NoReply: &noReply}); err != nil {
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
	noReplyBypassEvent, ok := events[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("no-reply bypass payload = %T, want PostAppendedPayload", events[2].Payload)
	}
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
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{NoReply: &noReply}); err != nil {
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
	replyForParentNoReply, ok := events[4].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root no-reply bypass payload = %T, want PostAppendedPayload", events[4].Payload)
	}
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
	articleNoReplyBypassEvent, ok := events[5].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("article no-reply bypass payload = %T, want PostAppendedPayload", events[5].Payload)
	}
	if articleNoReplyBypassEvent.ReplyTo != replyForParentNoReply.ID {
		t.Fatalf("article no-reply bypass ReplyTo = %q, want %q", articleNoReplyBypassEvent.ReplyTo, replyForParentNoReply.ID)
	}

	memberMode := true
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{MemberReadMode: &memberMode, MemberPostMode: &memberMode}); err != nil {
		t.Fatalf("set member board modes: %v", err)
	}
	memberCreatePayload := proto.CreateThreadPayload{
		Board: "general",
		Title: "Native member topic",
		Body:  "member can create through member board policy",
	}
	expectNativeErr(carol, boardPartition, proto.CmdCreateThread, memberCreatePayload, proto.ErrForbidden)
	if err := setBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{}); err != nil {
		t.Fatalf("set bob board member: %v", err)
	}
	produceAndDrain(bob, boardPartition, proto.CmdCreateThread, memberCreatePayload, "cid-native-policy-member-create", 6234)
	events = materializeBoard(2)
	if len(events) != 8 || events[6].Kind != proto.EvtThreadNew || events[7].Kind != proto.EvtPostAppended {
		t.Fatalf("events after member create = %+v, want second native thread", events)
	}
	memberThreadEvent, ok := events[6].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("member thread event payload = %T, want ThreadNewPayload", events[6].Payload)
	}
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
	memberReplyEvent, ok := events[8].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("member reply payload = %T, want PostAppendedPayload", events[8].Payload)
	}
	if memberReplyEvent.AuthorID != bob.ID || memberReplyEvent.Body != "member reply through native policy" {
		t.Fatalf("member reply event = %+v, want bob member reply", memberReplyEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsMentionNotifications(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native mentions",
		Body:  "hello @bob from broker root",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	createRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-mention",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	if err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != createRecord.Offset {
		t.Fatalf("create committed offset = %d, %v; want %d, nil", got, err, createRecord.Offset)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mention-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
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

	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay broker events: %v", err)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "reply also says @bob",
	})
	if err != nil {
		t.Fatalf("marshal append payload: %v", err)
	}
	appendRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-mention",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce append command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain append once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, LogPartition{Kind: partitionThread, Key: threadPayload.ID}); err != nil || got != appendRecord.Offset {
		t.Fatalf("append committed offset = %d, %v; want %d, nil", got, err, appendRecord.Offset)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mention-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize append event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native watched thread",
		Body:  "root post",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-watch",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-watch-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed outbox jobs after create = %d, want root post job", processed)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay broker events: %v", err)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	if err := setThreadPref(c.DB, bob.ID, threadPayload.ID, "watch"); err != nil {
		t.Fatalf("set thread watch: %v", err)
	}

	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "reply for a watcher",
	})
	if err != nil {
		t.Fatalf("marshal append payload: %v", err)
	}
	appendRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-watch",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce append command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain append once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, LogPartition{Kind: partitionThread, Key: threadPayload.ID}); err != nil || got != appendRecord.Offset {
		t.Fatalf("append committed offset = %d, %v; want %d, nil", got, err, appendRecord.Offset)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-watch-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize append event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	attachmentsAllowed := true
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{AttachmentsAllowed: &attachmentsAllowed}); err != nil {
		t.Fatalf("enable attachments: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
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
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-attachment",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-attachment-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootPostPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
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

	appendPayload, err := json.Marshal(proto.AppendPostPayload{
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
	if err != nil {
		t.Fatalf("marshal append payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-attachment",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	}); err != nil {
		t.Fatalf("produce append command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain append once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-attachment-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize append event: %v", err)
	}

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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	attachmentsAllowed := true
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{AttachmentsAllowed: &attachmentsAllowed}); err != nil {
		t.Fatalf("enable attachments: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native standalone attachment metadata",
		Body:  "root post",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-attach-post-create-thread",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-attach-post-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootPostPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", events[1].Payload)
	}

	missingStagedPayload, err := json.Marshal(proto.AttachPostPayload{
		ID:           "att_missing_staged",
		Post:         rootPostPayload.ID,
		Filename:     "missing.bin",
		ContentType:  "application/octet-stream",
		SizeBytes:    7,
		StagedBlobID: "missing-staged-blob",
	})
	if err != nil {
		t.Fatalf("marshal missing staged attach payload: %v", err)
	}
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

	attachPayload, err := json.Marshal(proto.AttachPostPayload{
		Post:        rootPostPayload.ID,
		Filename:    " proof.txt ",
		ContentType: " text/plain ",
		SizeBytes:   42,
	})
	if err != nil {
		t.Fatalf("marshal attach payload: %v", err)
	}
	attachPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}
	attachRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  attachPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-attach-post-metadata",
		Command:    proto.CmdAttachPost,
		Payload:    attachPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce attach command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain attach once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, attachPartition); err != nil || got != attachRecord.Offset {
		t.Fatalf("attach committed offset = %d, %v; want %d, nil", got, err, attachRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay attach events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v, want thread.new, post.appended, post.attachment_added", events)
	}
	attachmentPayload, ok := events[2].Payload.(*proto.PostAttachmentAddedPayload)
	if !ok {
		t.Fatalf("attachment event payload = %T, want PostAttachmentAddedPayload", events[2].Payload)
	}
	expectedAttachmentID := stableCommandLogDecisionID("att_", attachRecord, 0)
	if attachmentPayload.ID != expectedAttachmentID || attachmentPayload.Post != rootPostPayload.ID || attachmentPayload.Thread != threadPayload.ID ||
		attachmentPayload.Filename != "proof.txt" || attachmentPayload.ContentType != "text/plain" || attachmentPayload.SizeBytes != 42 ||
		attachmentPayload.AuthorID != alice.ID || attachmentPayload.TS != 2234 {
		t.Fatalf("attachment event = %+v, want normalized deterministic metadata", attachmentPayload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-attach-post-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize attach event: %v", err)
	}
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
	stagedPayload, err := json.Marshal(proto.AttachPostPayload{
		ID:           stagedAttachmentID,
		Post:         rootPostPayload.ID,
		Filename:     "blob.bin",
		ContentType:  "application/octet-stream",
		SizeBytes:    int64(len(stagedBytes)),
		StagedBlobID: stagedAttachmentID,
	})
	if err != nil {
		t.Fatalf("marshal staged attach payload: %v", err)
	}
	stagedRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  attachPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-attach-post-staged",
		Command:    proto.CmdAttachPost,
		Payload:    stagedPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce staged attach command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain staged attach once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, attachPartition); err != nil || got != stagedRecord.Offset {
		t.Fatalf("staged attach committed offset = %d, %v; want %d, nil", got, err, stagedRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay staged attach events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events after staged attach = %+v, want fourth post.attachment_added", events)
	}
	stagedEvent, ok := events[3].Payload.(*proto.PostAttachmentAddedPayload)
	if !ok {
		t.Fatalf("staged attachment event payload = %T, want PostAttachmentAddedPayload", events[3].Payload)
	}
	if stagedEvent.ID != stagedAttachmentID || stagedEvent.StagedBlobID != stagedAttachmentID ||
		stagedEvent.Filename != "blob.bin" || stagedEvent.ContentType != "application/octet-stream" ||
		stagedEvent.SizeBytes != int64(len(stagedBytes)) || stagedEvent.AuthorID != alice.ID || stagedEvent.TS != 3234 {
		t.Fatalf("staged attachment event = %+v, want staged blob metadata", stagedEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-attach-post-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize staged attach event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native edit",
		Body:  "original body",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-edit-post-create-thread",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-edit-post-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootPostPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
	posts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after create: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts after create = %+v, want root post", posts)
	}
	originalVersion := posts[0].Version

	pollEditPayload, err := json.Marshal(proto.EditPostPayload{
		Post: rootPostPayload.ID,
		Body: "edited with poll [poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	})
	if err != nil {
		t.Fatalf("marshal poll edit payload: %v", err)
	}
	pollEditReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionPost, Key: rootPostPayload.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-edit-post-poll-markup",
		Command:    proto.CmdEditPost,
		Payload:    pollEditPayload,
		EnqueuedAt: 2234,
	})
	if pollEditReply.Err == nil || pollEditReply.Err.Retryable || pollEditReply.Err.Code != proto.ErrValidationFailed {
		t.Fatalf("poll edit reply = %+v, want terminal validation failure", pollEditReply)
	}

	editPayload, err := json.Marshal(proto.EditPostPayload{
		Post: rootPostPayload.ID,
		Body: "edited native editposttoken",
	})
	if err != nil {
		t.Fatalf("marshal edit payload: %v", err)
	}
	editPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}
	editRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  editPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-edit-post",
		Command:    proto.CmdEditPost,
		Payload:    editPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce edit command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain edit once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, editPartition); err != nil || got != editRecord.Offset {
		t.Fatalf("edit committed offset = %d, %v; want %d, nil", got, err, editRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay edit events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v, want thread.new, post.appended, post.edited", events)
	}
	editEvent, ok := events[2].Payload.(*proto.PostEditedPayload)
	if !ok {
		t.Fatalf("edit event payload = %T, want PostEditedPayload", events[2].Payload)
	}
	if editEvent.ID != rootPostPayload.ID || editEvent.Thread != threadPayload.ID ||
		editEvent.NewBody != "edited native editposttoken" || editEvent.Version != originalVersion+1 || editEvent.TS != 2234 {
		t.Fatalf("edit event = %+v, want deterministic edit payload", editEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-edit-post-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize edit event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	archiveTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin archive board setup: %v", err)
	}
	if err := insertBoard(archiveTx, "archive", "archive", "Archive board", "", 1); err != nil {
		archiveTx.Rollback() //nolint:errcheck
		t.Fatalf("insert archive board: %v", err)
	}
	if err := archiveTx.Commit(); err != nil {
		t.Fatalf("commit archive board setup: %v", err)
	}

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native title",
		Body:  "root post",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-thread-controls-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-thread-control-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}

	titlePayload, err := json.Marshal(proto.SetThreadTitlePayload{
		Thread: threadPayload.ID,
		Title:  " broker-native renamed thread ",
	})
	if err != nil {
		t.Fatalf("marshal title payload: %v", err)
	}
	threadPartition := LogPartition{Kind: partitionThread, Key: threadPayload.ID}
	titleRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-thread-title",
		Command:    proto.CmdSetThreadTitle,
		Payload:    titlePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce title command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain title once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, threadPartition); err != nil || got != titleRecord.Offset {
		t.Fatalf("title committed offset = %d, %v; want %d, nil", got, err, titleRecord.Offset)
	}

	movePayload, err := json.Marshal(proto.MoveThreadPayload{Thread: threadPayload.ID, ToBoard: "archive"})
	if err != nil {
		t.Fatalf("marshal move payload: %v", err)
	}
	moveDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  threadPartition,
		Offset:     titleRecord.Offset + 1,
		ActorID:    bob.ID,
		CID:        "cid-native-thread-move-denied",
		Command:    proto.CmdMoveThread,
		Payload:    movePayload,
		EnqueuedAt: 4234,
	})
	if moveDenied.Err == nil || moveDenied.Err.Code != proto.ErrForbidden || moveDenied.Err.Retryable {
		t.Fatalf("move reply without permission = %+v, want terminal forbidden", moveDenied)
	}

	lockPayload, err := json.Marshal(proto.LockThreadPayload{Thread: threadPayload.ID, Locked: true})
	if err != nil {
		t.Fatalf("marshal lock payload: %v", err)
	}
	lockReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  threadPartition,
		Offset:     titleRecord.Offset + 1,
		ActorID:    bob.ID,
		CID:        "cid-native-thread-lock-denied",
		Command:    proto.CmdLockThread,
		Payload:    lockPayload,
		EnqueuedAt: 3234,
	})
	if lockReply.Err == nil || lockReply.Err.Code != proto.ErrForbidden || lockReply.Err.Retryable {
		t.Fatalf("lock reply without permission = %+v, want terminal forbidden", lockReply)
	}
	canModerateThreads := true
	if err := setBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{CanModerateThreads: &canModerateThreads}); err != nil {
		t.Fatalf("grant bob thread moderation: %v", err)
	}
	lockRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-thread-lock",
		Command:    proto.CmdLockThread,
		Payload:    lockPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce lock command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain lock once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, threadPartition); err != nil || got != lockRecord.Offset {
		t.Fatalf("lock committed offset = %d, %v; want %d, nil", got, err, lockRecord.Offset)
	}
	moveRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-thread-move",
		Command:    proto.CmdMoveThread,
		Payload:    movePayload,
		EnqueuedAt: 4234,
	})
	if err != nil {
		t.Fatalf("produce move command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain move once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, threadPartition); err != nil || got != moveRecord.Offset {
		t.Fatalf("move committed offset = %d, %v; want %d, nil", got, err, moveRecord.Offset)
	}

	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay thread control events: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("events = %+v, want thread.new, post.appended, thread.title_set, thread.locked, thread.moved", events)
	}
	titleEvent, ok := events[2].Payload.(*proto.ThreadTitleSetPayload)
	if !ok {
		t.Fatalf("title event payload = %T, want ThreadTitleSetPayload", events[2].Payload)
	}
	if titleEvent.Thread != threadPayload.ID || titleEvent.Title != "broker-native renamed thread" || titleEvent.By != alice.ID || titleEvent.TS != 2234 {
		t.Fatalf("title event = %+v, want normalized deterministic title event", titleEvent)
	}
	lockEvent, ok := events[3].Payload.(*proto.ThreadLockedPayload)
	if !ok {
		t.Fatalf("lock event payload = %T, want ThreadLockedPayload", events[3].Payload)
	}
	if lockEvent.Thread != threadPayload.ID || !lockEvent.Locked || lockEvent.By != bob.ID || lockEvent.TS != 3234 {
		t.Fatalf("lock event = %+v, want deterministic lock event", lockEvent)
	}
	moveEvent, ok := events[4].Payload.(*proto.ThreadMovedPayload)
	if !ok {
		t.Fatalf("move event payload = %T, want ThreadMovedPayload", events[4].Payload)
	}
	if moveEvent.Thread != threadPayload.ID || moveEvent.FromBoard != "general" || moveEvent.ToBoard != "archive" || moveEvent.By != bob.ID || moveEvent.TS != 4234 {
		t.Fatalf("move event = %+v, want deterministic move event", moveEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-thread-control-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize thread control events: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native flags",
		Body:  "root post",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-post-flags-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-post-flags-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	rootPostPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
	postPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}

	marked := true
	forbiddenPayload, err := json.Marshal(proto.SetPostFlagPayload{
		Post:   rootPostPayload.ID,
		Marked: &marked,
	})
	if err != nil {
		t.Fatalf("marshal forbidden flag payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-post-flags-forbidden",
		Command:    proto.CmdSetPostFlag,
		Payload:    forbiddenPayload,
		EnqueuedAt: 2234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Retryable {
		t.Fatalf("forbidden flag reply = %+v, want terminal forbidden", forbidden)
	}

	canCurate := true
	if err := setBoardMember(c.DB, "general", alice.ID, true, BoardMemberPatch{CanCurate: &canCurate}); err != nil {
		t.Fatalf("grant alice curate: %v", err)
	}
	recommended := true
	tex := true
	mailBack := true
	flagPayload, err := json.Marshal(proto.SetPostFlagPayload{
		Post:        rootPostPayload.ID,
		Marked:      &marked,
		Recommended: &recommended,
		TeX:         &tex,
		MailBack:    &mailBack,
	})
	if err != nil {
		t.Fatalf("marshal flag payload: %v", err)
	}
	flagRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-post-flags-curator-author",
		Command:    proto.CmdSetPostFlag,
		Payload:    flagPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce flag command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain flag once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, postPartition); err != nil || got != flagRecord.Offset {
		t.Fatalf("flag committed offset = %d, %v; want %d, nil", got, err, flagRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay flag events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events after flag = %+v, want one post.flags_set event", events)
	}
	flagEvent, ok := events[2].Payload.(*proto.PostFlagsSetPayload)
	if !ok {
		t.Fatalf("flag event payload = %T, want PostFlagsSetPayload", events[2].Payload)
	}
	if flagEvent.ID != rootPostPayload.ID || flagEvent.Thread != rootPostPayload.Thread ||
		!flagEvent.Marked || !flagEvent.Recommended || flagEvent.NoReply || !flagEvent.TeX || !flagEvent.MailBack ||
		flagEvent.By != alice.ID || flagEvent.TS != 2234 {
		t.Fatalf("flag event = %+v, want curator/author flags", flagEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-post-flags-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize flag event: %v", err)
	}
	post, err := c.GetPost(rootPostPayload.ID)
	if err != nil {
		t.Fatalf("get post after flag: %v", err)
	}
	if post == nil || !post.Marked || !post.Recommended || post.NoReply || !post.TeX || !post.MailBack || post.UpdatedAt != 2234 {
		t.Fatalf("post after flag = %+v, want materialized curator/author flags", post)
	}

	noopRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-post-flags-noop",
		Command:    proto.CmdSetPostFlag,
		Payload:    flagPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce no-op flag command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain no-op flag once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, postPartition); err != nil || got != noopRecord.Offset {
		t.Fatalf("no-op flag committed offset = %d, %v; want %d, nil", got, err, noopRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay after no-op flag: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events after no-op = %+v, want no additional event", events)
	}

	canModerateThreads := true
	if err := setBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{CanModerateThreads: &canModerateThreads}); err != nil {
		t.Fatalf("grant bob thread moderation: %v", err)
	}
	noReply := true
	noReplyPayload, err := json.Marshal(proto.SetPostFlagPayload{
		Post:    rootPostPayload.ID,
		NoReply: &noReply,
	})
	if err != nil {
		t.Fatalf("marshal no-reply flag payload: %v", err)
	}
	noReplyRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-post-flags-no-reply",
		Command:    proto.CmdSetPostFlag,
		Payload:    noReplyPayload,
		EnqueuedAt: 4234,
	})
	if err != nil {
		t.Fatalf("produce no-reply flag command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain no-reply flag once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, postPartition); err != nil || got != noReplyRecord.Offset {
		t.Fatalf("no-reply flag committed offset = %d, %v; want %d, nil", got, err, noReplyRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay no-reply flag events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events after no-reply = %+v, want second post.flags_set event", events)
	}
	noReplyEvent, ok := events[3].Payload.(*proto.PostFlagsSetPayload)
	if !ok {
		t.Fatalf("no-reply event payload = %T, want PostFlagsSetPayload", events[3].Payload)
	}
	if noReplyEvent.ID != rootPostPayload.ID || !noReplyEvent.Marked || !noReplyEvent.Recommended ||
		!noReplyEvent.NoReply || !noReplyEvent.TeX || !noReplyEvent.MailBack || noReplyEvent.By != bob.ID || noReplyEvent.TS != 4234 {
		t.Fatalf("no-reply event = %+v, want merged moderated flags", noReplyEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-post-flags-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize no-reply event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Native mail-back",
		Body:  "root asks for mail-back",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-mailback-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mailback-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	boardEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(boardEvents) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", boardEvents)
	}
	rootPostPayload, ok := boardEvents[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", boardEvents[1].Payload)
	}

	mailBack := true
	flagPayload, err := json.Marshal(proto.SetPostFlagPayload{
		Post:     rootPostPayload.ID,
		MailBack: &mailBack,
	})
	if err != nil {
		t.Fatalf("marshal mail-back flag payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionPost, Key: rootPostPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-mailback-flag",
		Command:    proto.CmdSetPostFlag,
		Payload:    flagPayload,
		EnqueuedAt: 2234,
	}); err != nil {
		t.Fatalf("produce mail-back flag command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain flag once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mailback-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize flag event: %v", err)
	}

	replyBody := "native mail-back reply"
	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread:  rootPostPayload.Thread,
		ReplyTo: rootPostPayload.ID,
		Body:    replyBody,
	})
	if err != nil {
		t.Fatalf("marshal mail-back reply payload: %v", err)
	}
	replyRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: rootPostPayload.Thread},
		ActorID:    bob.ID,
		CID:        "cid-native-mailback-reply",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce mail-back reply command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain mail-back reply once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, LogPartition{Kind: partitionThread, Key: rootPostPayload.Thread}); err != nil || got != replyRecord.Offset {
		t.Fatalf("reply committed offset = %d, %v; want %d, nil", got, err, replyRecord.Offset)
	}
	boardEvents, err = eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board events after mail-back reply: %v", err)
	}
	if len(boardEvents) != 4 || boardEvents[3].Kind != proto.EvtPostAppended {
		t.Fatalf("board events after mail-back reply = %+v, want reply post.appended", boardEvents)
	}
	replyPostPayload, ok := boardEvents[3].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("reply post payload = %T, want PostAppendedPayload", boardEvents[3].Payload)
	}
	userPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	mailEvents, err := eventStore.ReplayPartition(ctx, userPartition.Kind, userPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay mail-back user events: %v", err)
	}
	if len(mailEvents) != 1 || mailEvents[0].Kind != proto.EvtMailSent {
		t.Fatalf("mail-back user events = %+v, want one mail.sent", mailEvents)
	}
	mailPayload, ok := mailEvents[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("mail-back payload = %T, want MailSentPayload", mailEvents[0].Payload)
	}
	if mailPayload.FromUserID != bob.ID || mailPayload.From != bob.Name || len(mailPayload.ToUserIDs) != 1 ||
		mailPayload.ToUserIDs[0] != alice.ID || mailPayload.SaveSent ||
		!strings.Contains(mailPayload.Subject, "Native mail-back") ||
		!strings.Contains(mailPayload.Body, replyBody) ||
		!strings.Contains(mailPayload.Body, rootPostPayload.ID) ||
		!strings.Contains(mailPayload.Body, replyPostPayload.ID) {
		t.Fatalf("mail-back payload = %+v, want automatic inbox-only article mail", mailPayload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mailback-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize reply post event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mailback-test",
		Partition: userPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize mail-back event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	relayEnabled := true
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{RelayEnabled: &relayEnabled}); err != nil {
		t.Fatalf("enable relay board setting: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Native relay topic",
		Body:  "first relay body",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-relay-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce relay create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain relay create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-relay-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize relay create events: %v", err)
	}
	boardEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay relay create events: %v", err)
	}
	if len(boardEvents) != 2 {
		t.Fatalf("relay create events = %+v, want thread.new and post.appended", boardEvents)
	}
	rootPostPayload, ok := boardEvents[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", boardEvents[1].Payload)
	}
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

	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread: rootPostPayload.Thread,
		Body:   "second relay body",
	})
	if err != nil {
		t.Fatalf("marshal append payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: rootPostPayload.Thread},
		ActorID:    bob.ID,
		CID:        "cid-native-relay-reply",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	}); err != nil {
		t.Fatalf("produce relay reply command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain relay reply once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-relay-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize relay reply event: %v", err)
	}
	boardEvents, err = eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay relay events after reply: %v", err)
	}
	if len(boardEvents) != 3 {
		t.Fatalf("relay events after reply = %+v, want one reply post.appended", boardEvents)
	}
	replyPostPayload, ok := boardEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("reply post payload = %T, want PostAppendedPayload", boardEvents[2].Payload)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin filter setup: %v", err)
	}
	if err := upsertContentFilter(tx, "filter_native", "classified", "global", true, admin.ID, 1000); err != nil {
		tx.Rollback()
		t.Fatalf("upsert content filter: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit filter setup: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	filterPartition := LogPartition{Kind: partitionBoard, Key: nativeFilterSystemBoardID}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Native content filter",
		Body:  "this classified body should enter review",
	})
	if err != nil {
		t.Fatalf("marshal filtered create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-content-filter-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce filtered create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain filtered create once: %v", err)
	}
	boardEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay filtered create board events: %v", err)
	}
	if len(boardEvents) != 3 || boardEvents[2].Kind != proto.EvtPostFlagged {
		t.Fatalf("filtered create board events = %+v, want thread.new, post.appended, post.flagged", boardEvents)
	}
	rootPostPayload, ok := boardEvents[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", boardEvents[1].Payload)
	}
	filterPayload, ok := boardEvents[2].Payload.(*proto.PostFlaggedPayload)
	if !ok {
		t.Fatalf("filter payload = %T, want PostFlaggedPayload", boardEvents[2].Payload)
	}
	if filterPayload.Kind != "content_filter" || filterPayload.PostID != rootPostPayload.ID ||
		filterPayload.Thread != rootPostPayload.Thread || filterPayload.Reporter != bob.ID ||
		!strings.Contains(filterPayload.Reason, "filter_native") {
		t.Fatalf("content filter payload = %+v, want durable content-filter review event", filterPayload)
	}
	filterEvents, err := eventStore.ReplayPartition(ctx, filterPartition.Kind, filterPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay generated filter events: %v", err)
	}
	if len(filterEvents) != 3 ||
		filterEvents[0].Kind != proto.EvtBoardCreated ||
		filterEvents[1].Kind != proto.EvtThreadNew ||
		filterEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("generated filter events = %+v, want board/thread/post log events", filterEvents)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-content-filter-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize filtered create board events: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-content-filter-test",
		Partition: filterPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize generated filter events: %v", err)
	}
	reviews, err := c.ListModerationReviews("open", 10, 0)
	if err != nil {
		t.Fatalf("list moderation reviews: %v", err)
	}
	if len(reviews) != 1 || reviews[0].ID != filterPayload.ReviewID ||
		reviews[0].Kind != "content_filter" || reviews[0].TargetID != rootPostPayload.ID ||
		reviews[0].Reporter != bob.ID || !strings.Contains(reviews[0].Reason, "filter_native") {
		t.Fatalf("reviews after native filtered create = %+v, want open content-filter review", reviews)
	}
	filterBoard, err := c.GetBoard(nativeFilterSystemBoardID)
	if err != nil {
		t.Fatalf("get filter board: %v", err)
	}
	if filterBoard == nil || filterBoard.Name != "Filter" {
		t.Fatalf("filter board = %+v, want generated Filter board", filterBoard)
	}
	filterThreads, err := c.ListThreads(nativeFilterSystemBoardID, 10, 0)
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

	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread: rootPostPayload.Thread,
		Body:   "another classified reply should also enter review",
	})
	if err != nil {
		t.Fatalf("marshal filtered append payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: rootPostPayload.Thread},
		ActorID:    bob.ID,
		CID:        "cid-native-content-filter-append",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	}); err != nil {
		t.Fatalf("produce filtered append command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain filtered append once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-content-filter-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize filtered append board events: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-content-filter-test",
		Partition: filterPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize filtered append filter events: %v", err)
	}
	reviews, err = c.ListModerationReviews("open", 10, 0)
	if err != nil {
		t.Fatalf("list moderation reviews after append: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("reviews after filtered append = %+v, want two content-filter reviews", reviews)
	}
	filterThreads, err = c.ListThreads(nativeFilterSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list filter threads after append: %v", err)
	}
	if len(filterThreads) != 2 {
		t.Fatalf("filter threads after append = %+v, want generated log per review", filterThreads)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsContentFilterSettings(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	filterPayload, err := json.Marshal(proto.SetContentFilterPayload{
		ID:      "native_filter_policy",
		Pattern: "classified",
		Scope:   "general",
	})
	if err != nil {
		t.Fatalf("marshal scoped filter payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-filter-denied",
		Command:    proto.CmdSetContentFilter,
		Payload:    filterPayload,
		EnqueuedAt: 1234,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("set content filter as non-admin = %+v, want terminal forbidden", denied)
	}
	missingScopePayload, err := json.Marshal(proto.SetContentFilterPayload{
		ID:      "missing_scope_filter",
		Pattern: "classified",
		Scope:   "missing",
	})
	if err != nil {
		t.Fatalf("marshal missing scope payload: %v", err)
	}
	missingScope := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-filter-missing-scope",
		Command:    proto.CmdSetContentFilter,
		Payload:    missingScopePayload,
		EnqueuedAt: 1234,
	})
	if missingScope.Err == nil || missingScope.Err.Code != proto.ErrNotFound || missingScope.Err.Retryable {
		t.Fatalf("set content filter for missing scope = %+v, want terminal not found", missingScope)
	}

	filterRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-filter-set",
		Command:    proto.CmdSetContentFilter,
		Payload:    filterPayload,
		EnqueuedAt: 1234,
	})
	if err != nil {
		t.Fatalf("produce scoped filter command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain scoped filter once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, boardPartition); err != nil || got != filterRecord.Offset {
		t.Fatalf("scoped filter committed offset = %d, %v; want %d, nil", got, err, filterRecord.Offset)
	}
	boardEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay scoped filter events: %v", err)
	}
	if len(boardEvents) != 1 || boardEvents[0].Kind != proto.EvtContentFilterSet {
		t.Fatalf("scoped filter events = %+v, want one content_filter.set", boardEvents)
	}
	setPayload, ok := boardEvents[0].Payload.(*proto.ContentFilterSetPayload)
	if !ok {
		t.Fatalf("scoped filter payload = %T, want ContentFilterSetPayload", boardEvents[0].Payload)
	}
	if setPayload.ID != "native_filter_policy" || setPayload.Pattern != "classified" ||
		setPayload.Scope != "general" || !setPayload.Active || setPayload.By != admin.ID || setPayload.TS != 1234 {
		t.Fatalf("scoped filter payload = %+v, want deterministic content filter event", setPayload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-content-filter-setting-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize scoped filter event: %v", err)
	}
	filters, err := c.ListContentFilters("general", true, 10, 0)
	if err != nil {
		t.Fatalf("list scoped content filters: %v", err)
	}
	if len(filters) != 1 || filters[0].ID != "native_filter_policy" || !filters[0].Active ||
		filters[0].CreatedBy != admin.ID || filters[0].UpdatedAt != 1234 {
		t.Fatalf("scoped filters after materialization = %+v, want active native filter", filters)
	}

	inactive := false
	updatePayload, err := json.Marshal(proto.SetContentFilterPayload{
		ID:      "native_filter_policy",
		Pattern: "classified",
		Scope:   "general",
		Active:  &inactive,
	})
	if err != nil {
		t.Fatalf("marshal inactive filter payload: %v", err)
	}
	updateRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-filter-update",
		Command:    proto.CmdSetContentFilter,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce inactive filter command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain inactive filter once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, boardPartition); err != nil || got != updateRecord.Offset {
		t.Fatalf("inactive filter committed offset = %d, %v; want %d, nil", got, err, updateRecord.Offset)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-content-filter-setting-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize inactive filter event: %v", err)
	}
	filters, err = c.ListContentFilters("general", true, 10, 0)
	if err != nil {
		t.Fatalf("list inactive scoped content filters: %v", err)
	}
	if len(filters) != 1 || filters[0].Active || filters[0].UpdatedAt != 2234 {
		t.Fatalf("scoped filters after inactive update = %+v, want inactive native filter", filters)
	}

	globalPayload, err := json.Marshal(proto.SetContentFilterPayload{
		Pattern: "global secret",
	})
	if err != nil {
		t.Fatalf("marshal global filter payload: %v", err)
	}
	globalCommandPartition := LogPartition{Kind: partitionBoard, Key: partitionGlobal}
	globalRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  globalCommandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-filter-global",
		Command:    proto.CmdSetContentFilter,
		Payload:    globalPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce global filter command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain global filter once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, globalCommandPartition); err != nil || got != globalRecord.Offset {
		t.Fatalf("global filter committed offset = %d, %v; want %d, nil", got, err, globalRecord.Offset)
	}
	globalEventPartition := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}
	globalEvents, err := eventStore.ReplayPartition(ctx, globalEventPartition.Kind, globalEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay global filter events: %v", err)
	}
	if len(globalEvents) != 1 || globalEvents[0].Kind != proto.EvtContentFilterSet {
		t.Fatalf("global filter events = %+v, want one global content_filter.set", globalEvents)
	}
	globalSetPayload, ok := globalEvents[0].Payload.(*proto.ContentFilterSetPayload)
	if !ok {
		t.Fatalf("global filter payload = %T, want ContentFilterSetPayload", globalEvents[0].Payload)
	}
	if !strings.HasPrefix(globalSetPayload.ID, "filter_") || globalSetPayload.Pattern != "global secret" ||
		globalSetPayload.Scope != "global" || !globalSetPayload.Active || globalSetPayload.By != admin.ID {
		t.Fatalf("global filter payload = %+v, want generated global content filter", globalSetPayload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-content-filter-setting-test",
		Partition: globalEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize global filter event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	accountPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	sanctionTS := nowMS()
	sanctionPayload, err := json.Marshal(proto.SanctionUserPayload{
		User:        alice.ID,
		Kind:        "mute",
		Scope:       "general",
		DurationSec: 60,
		Reason:      " cooldown ",
	})
	if err != nil {
		t.Fatalf("marshal sanction payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  accountPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-sanction-denied",
		Command:    proto.CmdSanctionUser,
		Payload:    sanctionPayload,
		EnqueuedAt: sanctionTS,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("sanction as non-moderator = %+v, want terminal forbidden", denied)
	}
	adminTargetPayload, err := json.Marshal(proto.SanctionUserPayload{User: admin.ID, Kind: "mute", Scope: "global"})
	if err != nil {
		t.Fatalf("marshal admin target sanction payload: %v", err)
	}
	adminTarget := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: admin.ID},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-sanction-admin-target",
		Command:    proto.CmdSanctionUser,
		Payload:    adminTargetPayload,
		EnqueuedAt: 1234,
	})
	if adminTarget.Err == nil || adminTarget.Err.Code != proto.ErrForbidden || adminTarget.Err.Retryable {
		t.Fatalf("sanction admin target = %+v, want terminal forbidden", adminTarget)
	}

	sanctionRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  accountPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-sanction-user",
		Command:    proto.CmdSanctionUser,
		Payload:    sanctionPayload,
		EnqueuedAt: sanctionTS,
	})
	if err != nil {
		t.Fatalf("produce sanction command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain sanction once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, accountPartition); err != nil || got != sanctionRecord.Offset {
		t.Fatalf("sanction committed offset = %d, %v; want %d, nil", got, err, sanctionRecord.Offset)
	}
	accountEvents, err := eventStore.ReplayPartition(ctx, accountPartition.Kind, accountPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay sanction account events: %v", err)
	}
	if len(accountEvents) != 1 || accountEvents[0].Kind != proto.EvtUserSanctioned {
		t.Fatalf("account sanction events = %+v, want one user.sanctioned", accountEvents)
	}
	sanctionEvent, ok := accountEvents[0].Payload.(*proto.UserSanctionedPayload)
	if !ok {
		t.Fatalf("sanction payload = %T, want UserSanctionedPayload", accountEvents[0].Payload)
	}
	if sanctionEvent.User != alice.ID || sanctionEvent.Kind != "mute" || sanctionEvent.Scope != "general" ||
		sanctionEvent.DurationSec != 60 || sanctionEvent.By != admin.ID || sanctionEvent.Reason != "cooldown" || sanctionEvent.TS != sanctionTS {
		t.Fatalf("sanction event = %+v, want deterministic board mute", sanctionEvent)
	}
	denypostPartition := LogPartition{Kind: partitionBoard, Key: nativeDenyPostSystemBoardID}
	denypostEvents, err := eventStore.ReplayPartition(ctx, denypostPartition.Kind, denypostPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay denypost events: %v", err)
	}
	if len(denypostEvents) != 3 ||
		denypostEvents[0].Kind != proto.EvtBoardCreated ||
		denypostEvents[1].Kind != proto.EvtThreadNew ||
		denypostEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("denypost events = %+v, want board/thread/post generated record", denypostEvents)
	}
	denyThread, ok := denypostEvents[1].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("denypost thread payload = %T, want ThreadNewPayload", denypostEvents[1].Payload)
	}
	if !strings.Contains(denyThread.Title, "Board posting denied: alice on general") {
		t.Fatalf("denypost thread title = %q, want board posting denial", denyThread.Title)
	}
	denyPost, ok := denypostEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("denypost post payload = %T, want PostAppendedPayload", denypostEvents[2].Payload)
	}
	for _, want := range []string{"# Board posting denied", "- Action: board posting denied", "- User: alice", "(general)", "- Kind: mute", "- Actor: admin", "- Reason: cooldown"} {
		if !strings.Contains(denyPost.Body, want) {
			t.Fatalf("denypost body missing %q:\n%s", want, denyPost.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-user-sanction-test",
		Partition: accountPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize sanction account event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-user-sanction-test",
		Partition: denypostPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize denypost events: %v", err)
	}
	if kind, ok := activeSanction(c.DB, alice.ID, "general"); !ok || kind != "mute" {
		t.Fatalf("active general sanction = %q,%v; want mute,true", kind, ok)
	}
	denypostThreads, err := c.ListThreads(nativeDenyPostSystemBoardID, 10, 0)
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
	if err := insertBoard(secretTx, "secret", "Secret", "Private board", "", 1); err != nil {
		secretTx.Rollback() //nolint:errcheck
		t.Fatalf("insert secret board: %v", err)
	}
	if err := secretTx.Commit(); err != nil {
		t.Fatalf("commit secret board: %v", err)
	}
	memberRead := true
	if err := setBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read mode: %v", err)
	}
	secretPayload, err := json.Marshal(proto.SanctionUserPayload{
		User:   alice.ID,
		Kind:   "ban",
		Scope:  "secret",
		Reason: "private board",
	})
	if err != nil {
		t.Fatalf("marshal secret sanction payload: %v", err)
	}
	secretRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  accountPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-sanction-secret",
		Command:    proto.CmdSanctionUser,
		Payload:    secretPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce secret sanction command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain secret sanction once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, accountPartition); err != nil || got != secretRecord.Offset {
		t.Fatalf("secret sanction committed offset = %d, %v; want %d, nil", got, err, secretRecord.Offset)
	}
	denypostEvents, err = eventStore.ReplayPartition(ctx, denypostPartition.Kind, denypostPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay denypost events after private sanction: %v", err)
	}
	if len(denypostEvents) != 3 {
		t.Fatalf("denypost events after private sanction = %+v, want no private-board generated record", denypostEvents)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-user-sanction-test",
		Partition: accountPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize secret sanction account event: %v", err)
	}
	if kind, ok := activeSanction(c.DB, alice.ID, "secret"); !ok || kind != "ban" {
		t.Fatalf("active secret sanction = %q,%v; want ban,true", kind, ok)
	}

	clearPayload, err := json.Marshal(proto.ClearUserSanctionPayload{
		User:   alice.ID,
		Kind:   "mute",
		Scope:  "general",
		Reason: " served ",
	})
	if err != nil {
		t.Fatalf("marshal clear sanction payload: %v", err)
	}
	clearDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  accountPartition,
		Offset:     3,
		ActorID:    bob.ID,
		CID:        "cid-native-clear-sanction-denied",
		Command:    proto.CmdClearUserSanction,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	if clearDenied.Err == nil || clearDenied.Err.Code != proto.ErrForbidden || clearDenied.Err.Retryable {
		t.Fatalf("clear sanction as non-moderator = %+v, want terminal forbidden", clearDenied)
	}
	clearRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  accountPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-clear-sanction",
		Command:    proto.CmdClearUserSanction,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce clear sanction command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain clear sanction once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, accountPartition); err != nil || got != clearRecord.Offset {
		t.Fatalf("clear sanction committed offset = %d, %v; want %d, nil", got, err, clearRecord.Offset)
	}
	accountEvents, err = eventStore.ReplayPartition(ctx, accountPartition.Kind, accountPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay clear sanction account events: %v", err)
	}
	if len(accountEvents) != 3 || accountEvents[2].Kind != proto.EvtUserSanctionCleared {
		t.Fatalf("clear sanction account events = %+v, want third user.sanction_cleared", accountEvents)
	}
	clearEvent, ok := accountEvents[2].Payload.(*proto.UserSanctionClearedPayload)
	if !ok {
		t.Fatalf("clear sanction payload = %T, want UserSanctionClearedPayload", accountEvents[2].Payload)
	}
	if clearEvent.User != alice.ID || clearEvent.Kind != "mute" || clearEvent.Scope != "general" ||
		clearEvent.By != admin.ID || clearEvent.Reason != "served" || clearEvent.TS != 3234 {
		t.Fatalf("clear sanction event = %+v, want deterministic board mute clear", clearEvent)
	}
	undenypostPartition := LogPartition{Kind: partitionBoard, Key: nativeUndenyPostSystemBoardID}
	undenypostEvents, err := eventStore.ReplayPartition(ctx, undenypostPartition.Kind, undenypostPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay undenypost events: %v", err)
	}
	if len(undenypostEvents) != 3 ||
		undenypostEvents[0].Kind != proto.EvtBoardCreated ||
		undenypostEvents[1].Kind != proto.EvtThreadNew ||
		undenypostEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("undenypost events = %+v, want board/thread/post generated record", undenypostEvents)
	}
	undenyBoard, ok := undenypostEvents[0].Payload.(*proto.BoardCreatedPayload)
	if !ok {
		t.Fatalf("undenypost board payload = %T, want BoardCreatedPayload", undenypostEvents[0].Payload)
	}
	undenyThread, ok := undenypostEvents[1].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("undenypost thread payload = %T, want ThreadNewPayload", undenypostEvents[1].Payload)
	}
	if !strings.Contains(undenyThread.Title, "Board posting restored: alice on general") {
		t.Fatalf("undenypost thread title = %q, want board posting restore", undenyThread.Title)
	}
	undenyPost, ok := undenypostEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("undenypost post payload = %T, want PostAppendedPayload", undenypostEvents[2].Payload)
	}
	for _, want := range []string{"# Board posting restored", "- Action: board posting restored", "- User: alice", "(general)", "- Kind: mute", "- Actor: admin", "- Reason: served"} {
		if !strings.Contains(undenyPost.Body, want) {
			t.Fatalf("undenypost body missing %q:\n%s", want, undenyPost.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-user-sanction-test",
		Partition: accountPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize clear sanction account event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-user-sanction-test",
		Partition: undenypostPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize undenypost events: %v", err)
	}
	if kind, ok := activeSanction(c.DB, alice.ID, "general"); ok {
		t.Fatalf("active general sanction after clear = %q,%v; want none", kind, ok)
	}
	if kind, ok := activeSanction(c.DB, alice.ID, "secret"); !ok || kind != "ban" {
		t.Fatalf("active secret sanction after clear = %q,%v; want ban,true", kind, ok)
	}
	undenypostThreads, err := c.ListThreads(nativeUndenyPostSystemBoardID, 10, 0)
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
	retryBoard, ok := retryEvents[1].Payload.(*proto.BoardCreatedPayload)
	if !ok {
		t.Fatalf("clear sanction retry board payload = %T, want BoardCreatedPayload", retryEvents[1].Payload)
	}
	if retryBoard.ID != undenyBoard.ID || retryBoard.Position != undenyBoard.Position || retryBoard.Name != undenyBoard.Name {
		t.Fatalf("clear sanction retry board = %+v, want stable board upsert %+v", retryBoard, undenyBoard)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBlessUser(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	selfPayload, err := json.Marshal(proto.BlessUserPayload{User: "alice"})
	if err != nil {
		t.Fatalf("marshal self blessing payload: %v", err)
	}
	self := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "alice"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-bless-self",
		Command:    proto.CmdBlessUser,
		Payload:    selfPayload,
		EnqueuedAt: 1234,
	})
	if self.Err == nil || self.Err.Code != proto.ErrValidationFailed || self.Err.Retryable {
		t.Fatalf("self blessing = %+v, want terminal validation failure", self)
	}
	if err := setUserRelationship(c.DB, bob.ID, carol.ID, "ignore", "", true); err != nil {
		t.Fatalf("set bob ignores carol: %v", err)
	}
	ignoredPayload, err := json.Marshal(proto.BlessUserPayload{User: "bob"})
	if err != nil {
		t.Fatalf("marshal ignored blessing payload: %v", err)
	}
	ignored := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "bob"},
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-bless-ignored",
		Command:    proto.CmdBlessUser,
		Payload:    ignoredPayload,
		EnqueuedAt: 1234,
	})
	if ignored.Err == nil || ignored.Err.Code != proto.ErrForbidden || ignored.Err.Retryable {
		t.Fatalf("ignored blessing = %+v, want terminal forbidden", ignored)
	}

	commandPartition := LogPartition{Kind: partitionUser, Key: "bob"}
	blessPayload, err := json.Marshal(proto.BlessUserPayload{
		User:    "bob",
		Message: " Good luck on finals. ",
	})
	if err != nil {
		t.Fatalf("marshal blessing payload: %v", err)
	}
	blessRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-bless-user",
		Command:    proto.CmdBlessUser,
		Payload:    blessPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce blessing command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain blessing once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != blessRecord.Offset {
		t.Fatalf("blessing committed offset = %d, %v; want %d, nil", got, err, blessRecord.Offset)
	}

	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	accountEvents, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay blessing account events: %v", err)
	}
	if len(accountEvents) != 1 || accountEvents[0].Kind != proto.EvtUserBlessed {
		t.Fatalf("account blessing events = %+v, want one user.blessed", accountEvents)
	}
	blessingEvent, ok := accountEvents[0].Payload.(*proto.UserBlessedPayload)
	if !ok {
		t.Fatalf("blessing payload = %T, want UserBlessedPayload", accountEvents[0].Payload)
	}
	if blessingEvent.ID == "" || blessingEvent.FromUserID != alice.ID || blessingEvent.From != "alice" ||
		blessingEvent.ToUserID != bob.ID || blessingEvent.To != "bob" || blessingEvent.Message != "Good luck on finals." || blessingEvent.TS != 3234 {
		t.Fatalf("blessing event = %+v, want deterministic alice->bob blessing", blessingEvent)
	}
	blessingPartition := LogPartition{Kind: partitionBoard, Key: nativeBlessingSystemBoardID}
	blessingBoardEvents, err := eventStore.ReplayPartition(ctx, blessingPartition.Kind, blessingPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay Blessing board events: %v", err)
	}
	if len(blessingBoardEvents) != 3 ||
		blessingBoardEvents[0].Kind != proto.EvtBoardCreated ||
		blessingBoardEvents[1].Kind != proto.EvtThreadNew ||
		blessingBoardEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("Blessing board events = %+v, want board/thread/post generated record", blessingBoardEvents)
	}
	blessingThread, ok := blessingBoardEvents[1].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("blessing thread payload = %T, want ThreadNewPayload", blessingBoardEvents[1].Payload)
	}
	if !strings.Contains(blessingThread.Title, "alice -> bob") {
		t.Fatalf("blessing thread title = %q, want alice -> bob", blessingThread.Title)
	}
	blessingPost, ok := blessingBoardEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("blessing post payload = %T, want PostAppendedPayload", blessingBoardEvents[2].Payload)
	}
	for _, want := range []string{"# Blessing for bob", "- From: alice", "- To: bob", "Good luck on finals.", "Generated public blessing record"} {
		if !strings.Contains(blessingPost.Body, want) {
			t.Fatalf("blessing post body missing %q:\n%s", want, blessingPost.Body)
		}
	}

	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-blessing-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize blessing account event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-blessing-test",
		Partition: blessingPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize Blessing board events: %v", err)
	}
	blessings, err := c.ListBlessings(10, 0)
	if err != nil {
		t.Fatalf("list blessings: %v", err)
	}
	if len(blessings) != 1 || blessings[0].ID != blessingEvent.ID || blessings[0].FromName != "alice" || blessings[0].ToName != "bob" {
		t.Fatalf("materialized blessings = %+v, want alice blessing bob", blessings)
	}
	threads, err := c.ListThreads(nativeBlessingSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list Blessing threads: %v", err)
	}
	if len(threads) != 1 || !strings.Contains(threads[0].Title, "alice -> bob") {
		t.Fatalf("materialized Blessing threads = %+v, want generated blessing thread", threads)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsSendMail(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	if err := setUserRelationship(c.DB, alice.ID, bob.ID, "ignore", "", true); err != nil {
		t.Fatalf("set alice ignores bob: %v", err)
	}
	blockedPayload, err := json.Marshal(proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Blocked",
		Body:    "please read",
	})
	if err != nil {
		t.Fatalf("marshal blocked mail payload: %v", err)
	}
	blocked := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: bob.ID},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-send-mail-blocked",
		Command:    proto.CmdSendMail,
		Payload:    blockedPayload,
		EnqueuedAt: 1234,
	})
	if blocked.Err == nil || blocked.Err.Code != proto.ErrForbidden || blocked.Err.Retryable {
		t.Fatalf("send mail blocked by ignore = %+v, want terminal forbidden", blocked)
	}
	if err := setUserRelationship(c.DB, alice.ID, bob.ID, "ignore", "", false); err != nil {
		t.Fatalf("clear alice ignores bob: %v", err)
	}
	if err := setUserRelationship(c.DB, bob.ID, alice.ID, "friend", "", true); err != nil {
		t.Fatalf("set bob friends alice: %v", err)
	}
	if err := setMailGroup(c.DB, bob.ID, "grp_lab", "lab", []string{alice.ID, carol.ID}); err != nil {
		t.Fatalf("set bob mail group: %v", err)
	}

	commandPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	payload, err := json.Marshal(proto.SendMailPayload{
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
	if err != nil {
		t.Fatalf("marshal send mail payload: %v", err)
	}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-send-mail",
		Command:    proto.CmdSendMail,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce send mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain send mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != record.Offset {
		t.Fatalf("send mail committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}

	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay send mail events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtMailSent {
		t.Fatalf("send mail events = %+v, want one mail.sent", events)
	}
	mailEvent, ok := events[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("mail event payload = %T, want MailSentPayload", events[0].Payload)
	}
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

	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-send-mail-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize send mail event: %v", err)
	}
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

	replyPayload, err := json.Marshal(proto.SendMailPayload{
		To:      []string{"bob"},
		Subject: "Re: Campus plans",
		Body:    "See you there.",
		ReplyTo: mailEvent.ID,
	})
	if err != nil {
		t.Fatalf("marshal reply mail payload: %v", err)
	}
	replyPartition := LogPartition{Kind: partitionMail, Key: alice.ID}
	replyRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  replyPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-send-mail-reply",
		Command:    proto.CmdSendMail,
		Payload:    replyPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce reply mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain reply mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, replyPartition); err != nil || got != replyRecord.Offset {
		t.Fatalf("reply mail committed offset = %d, %v; want %d, nil", got, err, replyRecord.Offset)
	}
	replyEventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-send-mail-test",
		Partition: replyEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize reply mail event: %v", err)
	}
	bobInbox, err := c.ListMail(bob.ID, "inbox", 10, 0, false)
	if err != nil {
		t.Fatalf("list bob inbox: %v", err)
	}
	if len(bobInbox) != 1 || bobInbox[0].FromName != "alice" || bobInbox[0].ParentID != mailEvent.ID {
		t.Fatalf("bob inbox = %+v, want alice reply to original native mail", bobInbox)
	}

	toAllPayload, err := json.Marshal(proto.SendMailPayload{
		ToAll:   true,
		Subject: "Campus bulletin",
		Body:    "Maintenance at midnight.",
	})
	if err != nil {
		t.Fatalf("marshal mail-all payload: %v", err)
	}
	nonAdminToAll := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: bob.ID},
		Offset:     2,
		ActorID:    bob.ID,
		CID:        "cid-native-send-mail-all-forbidden",
		Command:    proto.CmdSendMail,
		Payload:    toAllPayload,
		EnqueuedAt: 3234,
	})
	if nonAdminToAll.Err == nil || nonAdminToAll.Err.Code != proto.ErrForbidden || nonAdminToAll.Err.Retryable {
		t.Fatalf("non-admin mail-all = %+v, want terminal forbidden", nonAdminToAll)
	}
	adminPartition := LogPartition{Kind: partitionMail, Key: admin.ID}
	adminRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  adminPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-send-mail-all",
		Command:    proto.CmdSendMail,
		Payload:    toAllPayload,
		EnqueuedAt: 4234,
	})
	if err != nil {
		t.Fatalf("produce mail-all command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain mail-all once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, adminPartition); err != nil || got != adminRecord.Offset {
		t.Fatalf("mail-all committed offset = %d, %v; want %d, nil", got, err, adminRecord.Offset)
	}
	adminEventPartition := LogPartition{Kind: partitionUser, Key: admin.ID}
	adminEvents, err := eventStore.ReplayPartition(ctx, adminEventPartition.Kind, adminEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay admin mail events: %v", err)
	}
	if len(adminEvents) != 1 || adminEvents[0].Kind != proto.EvtMailSent {
		t.Fatalf("admin mail events = %+v, want mail.sent", adminEvents)
	}
	mailAllEvent, ok := adminEvents[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("mail-all payload = %T, want MailSentPayload", adminEvents[0].Payload)
	}
	if len(mailAllEvent.ToUserIDs) != 3 || mailAllEvent.ToUserIDs[0] != alice.ID || mailAllEvent.ToUserIDs[1] != bob.ID || mailAllEvent.ToUserIDs[2] != carol.ID {
		t.Fatalf("mail-all recipients = %+v, want all non-admin users by name", mailAllEvent.ToUserIDs)
	}
	sysmailPartition := LogPartition{Kind: partitionBoard, Key: nativeSysmailSystemBoardID}
	sysmailEvents, err := eventStore.ReplayPartition(ctx, sysmailPartition.Kind, sysmailPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay sysmail events: %v", err)
	}
	if len(sysmailEvents) != 3 || sysmailEvents[0].Kind != proto.EvtBoardCreated ||
		sysmailEvents[1].Kind != proto.EvtThreadNew || sysmailEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("sysmail events = %+v, want board, thread, and post", sysmailEvents)
	}
	sysmailPost, ok := sysmailEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("sysmail post payload = %T, want PostAppendedPayload", sysmailEvents[2].Payload)
	}
	for _, want := range []string{"# Sysop mail: Campus bulletin", "- Recipients: 3 users", "Maintenance at midnight.", "Generated restricted sysop mail record"} {
		if !strings.Contains(sysmailPost.Body, want) {
			t.Fatalf("sysmail body missing %q:\n%s", want, sysmailPost.Body)
		}
	}
}

func TestNativeCommandLogDecisionExecutorProjectsAttachMail(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sourcePartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	sourcePayload, err := json.Marshal(proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Campus plans",
		Body:    "Meet in the lab at six.",
	})
	if err != nil {
		t.Fatalf("marshal source mail payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    bob.ID,
		CID:        "cid-native-attach-mail-source",
		Command:    proto.CmdSendMail,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce source mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain source mail once: %v", err)
	}
	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay source mail events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtMailSent {
		t.Fatalf("source mail events = %+v, want mail.sent", events)
	}
	sourceMail, ok := events[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("source mail payload = %T, want MailSentPayload", events[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-attach-mail-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize source mail event: %v", err)
	}

	attachPartition := LogPartition{Kind: partitionMail, Key: sourceMail.ID}
	unauthorizedPayload, err := json.Marshal(proto.AttachMailPayload{
		Mail:        sourceMail.ID,
		Filename:    "not-mine.txt",
		ContentType: "text/plain",
		SizeBytes:   1,
	})
	if err != nil {
		t.Fatalf("marshal unauthorized attach payload: %v", err)
	}
	unauthorized := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  attachPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-attach-mail-forbidden",
		Command:    proto.CmdAttachMail,
		Payload:    unauthorizedPayload,
		EnqueuedAt: 2234,
	})
	if unauthorized.Err == nil || unauthorized.Err.Code != proto.ErrForbidden || unauthorized.Err.Retryable {
		t.Fatalf("unauthorized attach mail = %+v, want terminal forbidden", unauthorized)
	}

	attachmentID := "matt_native_mail_blob"
	if err := c.StageMailAttachmentBlob(attachmentID, bob.ID, []byte("lab bytes"), "text/plain"); err != nil {
		t.Fatalf("stage mail attachment blob: %v", err)
	}
	attachPayload, err := json.Marshal(proto.AttachMailPayload{
		ID:           attachmentID,
		Mail:         sourceMail.ID,
		Filename:     " lab.txt ",
		ContentType:  "text/plain",
		SizeBytes:    int64(len("lab bytes")),
		StagedBlobID: attachmentID,
	})
	if err != nil {
		t.Fatalf("marshal attach mail payload: %v", err)
	}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  attachPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-attach-mail",
		Command:    proto.CmdAttachMail,
		Payload:    attachPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce attach mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain attach mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, attachPartition); err != nil || got != record.Offset {
		t.Fatalf("attach mail committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay attach mail events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtMailAttachmentAdded {
		t.Fatalf("attach mail events = %+v, want mail.sent then mail.attachment_added", events)
	}
	attachmentEvent, ok := events[1].Payload.(*proto.MailAttachmentAddedPayload)
	if !ok {
		t.Fatalf("attach mail payload = %T, want MailAttachmentAddedPayload", events[1].Payload)
	}
	if attachmentEvent.ID != attachmentID || attachmentEvent.Mail != sourceMail.ID ||
		attachmentEvent.Filename != "lab.txt" || attachmentEvent.StagedBlobID != attachmentID ||
		attachmentEvent.AuthorID != bob.ID || attachmentEvent.TS != 3234 {
		t.Fatalf("attach mail event = %+v, want deterministic bob attachment", attachmentEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-attach-mail-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize attach mail event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sourcePartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	sourcePayload, err := json.Marshal(proto.SendMailPayload{
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
	if err != nil {
		t.Fatalf("marshal source mail payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    bob.ID,
		CID:        "cid-native-forward-source",
		Command:    proto.CmdSendMail,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce source mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain source mail once: %v", err)
	}
	sourceEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	sourceEvents, err := eventStore.ReplayPartition(ctx, sourceEventPartition.Kind, sourceEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay source mail event: %v", err)
	}
	if len(sourceEvents) != 1 || sourceEvents[0].Kind != proto.EvtMailSent {
		t.Fatalf("source mail events = %+v, want mail.sent", sourceEvents)
	}
	sourceMail, ok := sourceEvents[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("source mail payload = %T, want MailSentPayload", sourceEvents[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-forward-mail-test",
		Partition: sourceEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize source mail event: %v", err)
	}

	forwardPayload, err := json.Marshal(proto.ForwardMailPayload{
		Mail: sourceMail.ID,
		To:   []string{"carol"},
		Note: "Please see this.",
	})
	if err != nil {
		t.Fatalf("marshal forward mail payload: %v", err)
	}
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
	if missingCopy.Err == nil || missingCopy.Err.Code != proto.ErrNotFound || missingCopy.Err.Retryable {
		t.Fatalf("forward without mail copy = %+v, want terminal not found", missingCopy)
	}

	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  forwardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-forward-mail",
		Command:    proto.CmdForwardMail,
		Payload:    forwardPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce forward mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain forward mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, forwardPartition); err != nil || got != record.Offset {
		t.Fatalf("forward mail committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}

	forwardEventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	events, err := eventStore.ReplayPartition(ctx, forwardEventPartition.Kind, forwardEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay forward mail events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtMailSent {
		t.Fatalf("forward mail events = %+v, want one mail.sent", events)
	}
	forwardEvent, ok := events[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("forward mail payload = %T, want MailSentPayload", events[0].Payload)
	}
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

	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-forward-mail-test",
		Partition: forwardEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize forward mail event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Original article",
		Body:  "source body for excerpt",
	})
	if err != nil {
		t.Fatalf("marshal source thread payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-mail-post-author-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce source thread command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain source thread once: %v", err)
	}
	sourceEvents, err := eventStore.ReplayPartition(ctx, sourcePartition.Kind, sourcePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay source thread events: %v", err)
	}
	if len(sourceEvents) != 2 {
		t.Fatalf("source thread events = %+v, want thread and post", sourceEvents)
	}
	sourceThread, ok := sourceEvents[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("source thread payload = %T, want ThreadNewPayload", sourceEvents[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mail-post-author-test",
		Partition: sourcePartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize source thread events: %v", err)
	}
	sourcePosts, err := c.ListPosts(sourceThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}

	mailPayload, err := json.Marshal(proto.MailPostAuthorPayload{
		Post: sourcePosts[0].ID,
		Body: "Could you share the notes?",
	})
	if err != nil {
		t.Fatalf("marshal mail-post-author payload: %v", err)
	}
	mailPartition := LogPartition{Kind: partitionPost, Key: sourcePosts[0].ID}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  mailPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-post-author",
		Command:    proto.CmdMailPostAuthor,
		Payload:    mailPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce mail-post-author command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain mail-post-author once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, mailPartition); err != nil || got != record.Offset {
		t.Fatalf("mail-post-author committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	mailEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, mailEventPartition.Kind, mailEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay mail-post-author events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtMailSent {
		t.Fatalf("mail-post-author events = %+v, want mail.sent", events)
	}
	mailEvent, ok := events[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("mail-post-author payload = %T, want MailSentPayload", events[0].Payload)
	}
	if mailEvent.ID == "" || mailEvent.FromUserID != bob.ID || len(mailEvent.ToUserIDs) != 1 ||
		mailEvent.ToUserIDs[0] != alice.ID || mailEvent.Subject != "Re: Original article" || mailEvent.TS != 2234 {
		t.Fatalf("mail-post-author event = %+v, want bob mail to alice", mailEvent)
	}
	for _, want := range []string{"Could you share the notes?", "Sent from article reading context.", "Board: general", "Thread: Original article", "Article author: alice", "Mail author: bob", "source body for excerpt"} {
		if !strings.Contains(mailEvent.Body, want) {
			t.Fatalf("mail-post-author body missing %q:\n%s", want, mailEvent.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mail-post-author-test",
		Partition: mailEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize mail-post-author event: %v", err)
	}
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
	if err := insertBoard(secretTx, "secret", "Secret", "Members only", "", 2); err != nil {
		secretTx.Rollback() //nolint:errcheck
		t.Fatalf("insert secret board: %v", err)
	}
	if err := secretTx.Commit(); err != nil {
		t.Fatalf("commit secret board setup: %v", err)
	}
	memberRead := true
	if err := setBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read mode: %v", err)
	}
	secretPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private article",
		Body:  "hidden source",
	})
	if err != nil {
		t.Fatalf("marshal private source payload: %v", err)
	}
	secretPartition := LogPartition{Kind: partitionBoard, Key: "secret"}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  secretPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-mail-post-author-private-source",
		Command:    proto.CmdCreateThread,
		Payload:    secretPayload,
		EnqueuedAt: 3234,
	}); err != nil {
		t.Fatalf("produce private source command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain private source once: %v", err)
	}
	secretEvents, err := eventStore.ReplayPartition(ctx, secretPartition.Kind, secretPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay private source events: %v", err)
	}
	if len(secretEvents) != 2 {
		t.Fatalf("private source events = %+v, want thread and post", secretEvents)
	}
	secretThread, ok := secretEvents[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("private source thread payload = %T, want ThreadNewPayload", secretEvents[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mail-post-author-test",
		Partition: secretPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize private source events: %v", err)
	}
	secretPosts, err := c.ListPosts(secretThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list private source posts: %v", err)
	}
	if len(secretPosts) != 1 {
		t.Fatalf("private source posts = %+v, want one", secretPosts)
	}
	privateMailPayload, err := json.Marshal(proto.MailPostAuthorPayload{
		Post: secretPosts[0].ID,
		Body: "Can I see this?",
	})
	if err != nil {
		t.Fatalf("marshal private mail-post-author payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionPost, Key: secretPosts[0].ID},
		Offset:     2,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-post-author-private",
		Command:    proto.CmdMailPostAuthor,
		Payload:    privateMailPayload,
		EnqueuedAt: 4234,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("private board mail-post-author denied = %+v, want terminal forbidden", denied)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsCreateDigestDirectory(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	payload, err := json.Marshal(proto.CreateDigestDirectoryPayload{
		Board: "general",
		Kind:  "archive",
		Path:  " faq/empty/ ",
	})
	if err != nil {
		t.Fatalf("marshal digest directory payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-directory-forbidden",
		Command:    proto.CmdCreateDigestDirectory,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "board curator permission required" || forbidden.Err.Retryable {
		t.Fatalf("create digest directory as non-curator = %+v, want terminal curator permission error", forbidden)
	}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-directory",
		Command:    proto.CmdCreateDigestDirectory,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce digest directory command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain digest directory once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
		t.Fatalf("digest directory committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay digest directory events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtDigestDirectorySet {
		t.Fatalf("digest directory events = %+v, want one directory event", events)
	}
	directoryEvent, ok := events[0].Payload.(*proto.DigestDirectorySetPayload)
	if !ok {
		t.Fatalf("digest directory payload = %T, want DigestDirectorySetPayload", events[0].Payload)
	}
	if directoryEvent.ID != stableCommandLogDecisionID("dir_", record, 0) || directoryEvent.Board != "general" ||
		directoryEvent.Kind != "archive" || directoryEvent.Path != "faq/empty" ||
		directoryEvent.CreatedBy != admin.ID || directoryEvent.TS != 2234 {
		t.Fatalf("digest directory event = %+v, want deterministic archive directory", directoryEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-directory-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize digest directory event: %v", err)
	}
	tree, err := c.ListDigestPathTree("general", "archive")
	if err != nil {
		t.Fatalf("list digest path tree: %v", err)
	}
	nodes := map[string]DigestPathNode{}
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
	retryDirectory, ok := retryEvents[0].Payload.(*proto.DigestDirectorySetPayload)
	if !ok {
		t.Fatalf("digest directory retry payload = %T, want DigestDirectorySetPayload", retryEvents[0].Payload)
	}
	if retryDirectory.ID != directoryEvent.ID || retryDirectory.Board != directoryEvent.Board ||
		retryDirectory.Kind != directoryEvent.Kind || retryDirectory.Path != directoryEvent.Path {
		t.Fatalf("digest directory retry payload = %+v, want stable payload %+v", retryDirectory, directoryEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsCopyDigestPath(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	if _, err := upsertDigestEntry(c.DB, "dig_copy_source_post", "general", "post", "post_copy_source", "archive", "Copy source post", "faq/howto", "Source note", admin.ID); err != nil {
		t.Fatalf("upsert source digest entry: %v", err)
	}
	if _, err := upsertDigestEntry(c.DB, "dig_copy_source_thread", "general", "thread", "thread_copy_source", "archive", "Copy source thread", "faq/howto/deep", "Thread note", admin.ID); err != nil {
		t.Fatalf("upsert nested source digest entry: %v", err)
	}
	if _, err := upsertDigestDirectory(c.DB, "dir_copy_empty", "general", "archive", "faq/empty", admin.ID); err != nil {
		t.Fatalf("upsert source digest directory: %v", err)
	}
	if _, err := upsertDigestEntry(c.DB, "dig_copy_conflict", "general", "post", "post_copy_source", "archive", "Conflicting copy target", "conflict/howto", "", admin.ID); err != nil {
		t.Fatalf("upsert conflicting digest entry: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	payload, err := json.Marshal(proto.CopyDigestPathPayload{
		Board:    "general",
		FromPath: " faq/ ",
		ToPath:   " faq-copy/ ",
	})
	if err != nil {
		t.Fatalf("marshal copy digest path payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-copy-forbidden",
		Command:    proto.CmdCopyDigestPath,
		Payload:    payload,
		EnqueuedAt: 1234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "board curator permission required" || forbidden.Err.Retryable {
		t.Fatalf("copy digest path as non-curator = %+v, want terminal curator permission error", forbidden)
	}
	conflictPayload, err := json.Marshal(proto.CopyDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "conflict",
	})
	if err != nil {
		t.Fatalf("marshal conflicting copy digest path payload: %v", err)
	}
	conflict := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-copy-conflict",
		Command:    proto.CmdCopyDigestPath,
		Payload:    conflictPayload,
		EnqueuedAt: 1234,
	})
	if conflict.Err == nil || conflict.Err.Code != proto.ErrConflict || conflict.Err.Message != "digest path copy would overwrite an existing entry" || conflict.Err.Retryable {
		t.Fatalf("conflicting digest path copy = %+v, want terminal conflict", conflict)
	}

	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-copy",
		Command:    proto.CmdCopyDigestPath,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce copy digest path command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain copy digest path once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != record.Offset {
		t.Fatalf("copy digest path committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay copy digest path events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtDigestPathCopied {
		t.Fatalf("copy digest path events = %+v, want one path copied event", events)
	}
	copyEvent, ok := events[0].Payload.(*proto.DigestPathCopiedPayload)
	if !ok {
		t.Fatalf("copy digest path payload = %T, want DigestPathCopiedPayload", events[0].Payload)
	}
	wantEntryIDs := []string{stableCommandLogDecisionID("dig_", record, 0), stableCommandLogDecisionID("dig_", record, 1)}
	wantDirectoryIDs := []string{stableCommandLogDecisionID("dir_", record, 0)}
	if copyEvent.Board != "general" || copyEvent.Kind != "archive" ||
		copyEvent.FromPath != "faq" || copyEvent.ToPath != "faq-copy" ||
		copyEvent.Count != 3 || copyEvent.CreatedBy != admin.ID || copyEvent.TS != 2234 ||
		!sameStringSlice(copyEvent.EntryIDs, wantEntryIDs) ||
		!sameStringSlice(copyEvent.DirectoryIDs, wantDirectoryIDs) {
		t.Fatalf("copy digest path event = %+v, want deterministic archive subtree copy", copyEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-copy-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize copy digest path event: %v", err)
	}
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
	nodes := map[string]DigestPathNode{}
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
	retryCopy, ok := retryEvents[0].Payload.(*proto.DigestPathCopiedPayload)
	if !ok {
		t.Fatalf("copy digest path retry payload = %T, want DigestPathCopiedPayload", retryEvents[0].Payload)
	}
	if retryCopy.Count != copyEvent.Count || !sameStringSlice(retryCopy.EntryIDs, copyEvent.EntryIDs) ||
		!sameStringSlice(retryCopy.DirectoryIDs, copyEvent.DirectoryIDs) {
		t.Fatalf("copy digest path retry payload = %+v, want stable payload %+v", retryCopy, copyEvent)
	}

	descendantPayload, err := json.Marshal(proto.CopyDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "faq/nested-copy",
	})
	if err != nil {
		t.Fatalf("marshal descendant copy digest path payload: %v", err)
	}
	descendantRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-copy-descendant",
		Command:    proto.CmdCopyDigestPath,
		Payload:    descendantPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce descendant copy digest path command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain descendant copy digest path once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-copy-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize descendant copy digest path event: %v", err)
	}
	descendantRetryReply := executor.ExecuteCommandLogRecord(ctx, descendantRecord)
	if descendantRetryReply.Err != nil || descendantRetryReply.Result == nil || descendantRetryReply.Result.ID != "general:archive:3" {
		t.Fatalf("descendant copy retry reply = %+v, want original source count despite copied descendants", descendantRetryReply)
	}
	descendantRetryEvents, err := executor.DecideCommandLogEvents(ctx, descendantRecord, descendantRetryReply)
	if err != nil {
		t.Fatalf("decide descendant copy retry events: %v", err)
	}
	if len(descendantRetryEvents) != 1 || descendantRetryEvents[0].Kind != proto.EvtDigestPathCopied {
		t.Fatalf("descendant copy retry events = %+v, want one stable copied event", descendantRetryEvents)
	}
	descendantRetryCopy, ok := descendantRetryEvents[0].Payload.(*proto.DigestPathCopiedPayload)
	if !ok {
		t.Fatalf("descendant copy retry payload = %T, want DigestPathCopiedPayload", descendantRetryEvents[0].Payload)
	}
	if descendantRetryCopy.Count != 3 ||
		!sameStringSlice(descendantRetryCopy.EntryIDs, []string{stableCommandLogDecisionID("dig_", descendantRecord, 0), stableCommandLogDecisionID("dig_", descendantRecord, 1)}) ||
		!sameStringSlice(descendantRetryCopy.DirectoryIDs, []string{stableCommandLogDecisionID("dir_", descendantRecord, 0)}) {
		t.Fatalf("descendant copy retry payload = %+v, want stable original source count", descendantRetryCopy)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsMoveAndDeleteDigestPath(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	if _, err := upsertDigestEntry(c.DB, "dig_move_source_post", "general", "post", "post_move_source", "archive", "Move source post", "faq/howto", "Source note", admin.ID); err != nil {
		t.Fatalf("upsert source digest entry: %v", err)
	}
	if _, err := upsertDigestEntry(c.DB, "dig_move_source_thread", "general", "thread", "thread_move_source", "archive", "Move source thread", "faq/howto/deep", "Thread note", admin.ID); err != nil {
		t.Fatalf("upsert nested source digest entry: %v", err)
	}
	if _, err := upsertDigestDirectory(c.DB, "dir_move_empty", "general", "archive", "faq/empty", admin.ID); err != nil {
		t.Fatalf("upsert source digest directory: %v", err)
	}
	if _, err := upsertDigestEntry(c.DB, "dig_move_conflict", "general", "post", "post_move_source", "archive", "Conflicting move target", "conflict/howto", "", admin.ID); err != nil {
		t.Fatalf("upsert conflicting digest entry: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	movePayload, err := json.Marshal(proto.MoveDigestPathPayload{
		Board:    " general ",
		Kind:     "archive",
		FromPath: " faq/ ",
		ToPath:   " faq-moved/ ",
	})
	if err != nil {
		t.Fatalf("marshal move digest path payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-move-forbidden",
		Command:    proto.CmdMoveDigestPath,
		Payload:    movePayload,
		EnqueuedAt: 1234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "board curator permission required" || forbidden.Err.Retryable {
		t.Fatalf("move digest path as non-curator = %+v, want terminal curator permission error", forbidden)
	}
	invalidPayload, err := json.Marshal(proto.MoveDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "faq/nested",
	})
	if err != nil {
		t.Fatalf("marshal invalid move digest path payload: %v", err)
	}
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-move-invalid",
		Command:    proto.CmdMoveDigestPath,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	if invalid.Err == nil || invalid.Err.Code != proto.ErrValidationFailed ||
		invalid.Err.Message != "cannot move an archive path into itself" || invalid.Err.Retryable {
		t.Fatalf("invalid digest path move = %+v, want terminal self-move validation", invalid)
	}
	conflictPayload, err := json.Marshal(proto.MoveDigestPathPayload{
		Board:    "general",
		Kind:     "archive",
		FromPath: "faq",
		ToPath:   "conflict",
	})
	if err != nil {
		t.Fatalf("marshal conflicting move digest path payload: %v", err)
	}
	conflict := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  partition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-move-conflict",
		Command:    proto.CmdMoveDigestPath,
		Payload:    conflictPayload,
		EnqueuedAt: 1234,
	})
	if conflict.Err == nil || conflict.Err.Code != proto.ErrConflict ||
		conflict.Err.Message != "digest path move would overwrite an existing entry" || conflict.Err.Retryable {
		t.Fatalf("conflicting digest path move = %+v, want terminal conflict", conflict)
	}

	moveRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-move",
		Command:    proto.CmdMoveDigestPath,
		Payload:    movePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce move digest path command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain move digest path once: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay move digest path events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtDigestPathMoved {
		t.Fatalf("move digest path events = %+v, want one path moved event", events)
	}
	moveEvent, ok := events[0].Payload.(*proto.DigestPathMovedPayload)
	if !ok {
		t.Fatalf("move digest path payload = %T, want DigestPathMovedPayload", events[0].Payload)
	}
	if moveEvent.Board != "general" || moveEvent.Kind != "archive" ||
		moveEvent.FromPath != "faq" || moveEvent.ToPath != "faq-moved" ||
		moveEvent.Count != 3 || moveEvent.By != admin.ID || moveEvent.TS != 2234 {
		t.Fatalf("move digest path event = %+v, want deterministic archive subtree move", moveEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-move-delete-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize move digest path event: %v", err)
	}
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
	retryMove, ok := retryMoveEvents[0].Payload.(*proto.DigestPathMovedPayload)
	if !ok {
		t.Fatalf("move digest path retry payload = %T, want DigestPathMovedPayload", retryMoveEvents[0].Payload)
	}
	if retryMove.Count != moveEvent.Count || retryMove.FromPath != moveEvent.FromPath ||
		retryMove.ToPath != moveEvent.ToPath || retryMove.By != moveEvent.By || retryMove.TS != moveEvent.TS {
		t.Fatalf("move digest path retry payload = %+v, want stable payload %+v", retryMove, moveEvent)
	}

	deletePayload, err := json.Marshal(proto.DeleteDigestPathPayload{
		Board: "general",
		Kind:  "archive",
		Path:  "faq-moved",
	})
	if err != nil {
		t.Fatalf("marshal delete digest path payload: %v", err)
	}
	deleteRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-delete-path",
		Command:    proto.CmdDeleteDigestPath,
		Payload:    deletePayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce delete digest path command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain delete digest path once: %v", err)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay delete digest path events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtDigestPathDeleted {
		t.Fatalf("delete digest path events = %+v, want path moved then deleted", events)
	}
	deleteEvent, ok := events[1].Payload.(*proto.DigestPathDeletedPayload)
	if !ok {
		t.Fatalf("delete digest path payload = %T, want DigestPathDeletedPayload", events[1].Payload)
	}
	if deleteEvent.Board != "general" || deleteEvent.Kind != "archive" ||
		deleteEvent.Path != "faq-moved" || deleteEvent.Count != 3 ||
		deleteEvent.By != admin.ID || deleteEvent.TS != 3234 {
		t.Fatalf("delete digest path event = %+v, want deterministic archive subtree deletion", deleteEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-move-delete-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize delete digest path event: %v", err)
	}
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
	retryDelete, ok := retryDeleteEvents[0].Payload.(*proto.DigestPathDeletedPayload)
	if !ok {
		t.Fatalf("delete digest path retry payload = %T, want DigestPathDeletedPayload", retryDeleteEvents[0].Payload)
	}
	if retryDelete.Count != deleteEvent.Count || retryDelete.Path != deleteEvent.Path ||
		retryDelete.By != deleteEvent.By || retryDelete.TS != deleteEvent.TS {
		t.Fatalf("delete digest path retry payload = %+v, want stable payload %+v", retryDelete, deleteEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDigestCuration(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Curatable topic",
		Body:  "First public body.",
	})
	if err != nil {
		t.Fatalf("marshal source thread payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-curation-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce source thread command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain source thread once: %v", err)
	}
	sourceEvents, err := eventStore.ReplayPartition(ctx, sourcePartition.Kind, sourcePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay source events: %v", err)
	}
	if len(sourceEvents) != 2 {
		t.Fatalf("source events = %+v, want thread and post", sourceEvents)
	}
	sourceThread, ok := sourceEvents[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("source thread payload = %T, want ThreadNewPayload", sourceEvents[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-curation-test",
		Partition: sourcePartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize source events: %v", err)
	}
	sourcePosts, err := c.ListPosts(sourceThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}

	postPayload, err := json.Marshal(proto.CuratePostPayload{
		Post: sourcePosts[0].ID,
		Kind: "DiGeSt",
		Path: "/faq/",
		Note: "Useful local note.",
	})
	if err != nil {
		t.Fatalf("marshal curate post payload: %v", err)
	}
	postPartition := LogPartition{Kind: partitionPost, Key: sourcePosts[0].ID}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-curation-forbidden",
		Command:    proto.CmdCuratePost,
		Payload:    postPayload,
		EnqueuedAt: 2234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "board curator permission required" || forbidden.Err.Retryable {
		t.Fatalf("curate post as non-curator = %+v, want terminal curator permission error", forbidden)
	}
	postRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-curation-post",
		Command:    proto.CmdCuratePost,
		Payload:    postPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce curate post command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain curate post once: %v", err)
	}
	generalEvents, err := eventStore.ReplayPartition(ctx, sourcePartition.Kind, sourcePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay general events after curate post: %v", err)
	}
	if len(generalEvents) != 3 || generalEvents[2].Kind != proto.EvtDigestEntryUpserted {
		t.Fatalf("general events after curate post = %+v, want digest entry event", generalEvents)
	}
	postDigest, ok := generalEvents[2].Payload.(*proto.DigestEntryUpsertedPayload)
	if !ok {
		t.Fatalf("post digest payload = %T, want DigestEntryUpsertedPayload", generalEvents[2].Payload)
	}
	wantPostTitle := fmt.Sprintf("Curatable topic #%d", sourcePosts[0].CreatedSeq)
	if postDigest.ID != stableCommandLogDecisionID("dig_", postRecord, 0) || postDigest.Board != "general" ||
		postDigest.TargetKind != "post" || postDigest.TargetID != sourcePosts[0].ID ||
		postDigest.Kind != "digest" || postDigest.Title != wantPostTitle ||
		postDigest.Path != "faq" || postDigest.Note != "Useful local note." ||
		postDigest.CreatedBy != admin.ID || postDigest.TS != 2234 {
		t.Fatalf("post digest event = %+v, want deterministic post digest", postDigest)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-curation-test",
		Partition: sourcePartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize post digest event: %v", err)
	}
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
	retryPostDigest, ok := retryPostEvents[0].Payload.(*proto.DigestEntryUpsertedPayload)
	if !ok {
		t.Fatalf("curate post retry payload = %T, want DigestEntryUpsertedPayload", retryPostEvents[0].Payload)
	}
	if retryPostDigest.ID != postDigest.ID || retryPostDigest.Kind != "digest" || retryPostDigest.Path != "faq" {
		t.Fatalf("curate post retry payload = %+v, want stable digest payload %+v", retryPostDigest, postDigest)
	}

	recommendPayload, err := json.Marshal(proto.CurateThreadPayload{
		Thread: sourceThread.ID,
		Kind:   "recommended",
		Title:  "Front page pick",
		Path:   "frontpage",
		Note:   "Worth reading.",
	})
	if err != nil {
		t.Fatalf("marshal recommended thread payload: %v", err)
	}
	threadPartition := LogPartition{Kind: partitionThread, Key: sourceThread.ID}
	recommendRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-curation-recommend",
		Command:    proto.CmdCurateThread,
		Payload:    recommendPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce recommended thread command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain recommended thread once: %v", err)
	}
	generalEvents, err = eventStore.ReplayPartition(ctx, sourcePartition.Kind, sourcePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay general events after recommended curation: %v", err)
	}
	if len(generalEvents) != 4 || generalEvents[3].Kind != proto.EvtDigestEntryUpserted {
		t.Fatalf("general events after recommended curation = %+v, want digest entry event", generalEvents)
	}
	recommendedDigest, ok := generalEvents[3].Payload.(*proto.DigestEntryUpsertedPayload)
	if !ok {
		t.Fatalf("recommended digest payload = %T, want DigestEntryUpsertedPayload", generalEvents[3].Payload)
	}
	if recommendedDigest.ID != stableCommandLogDecisionID("dig_", recommendRecord, 0) ||
		recommendedDigest.TargetKind != "thread" || recommendedDigest.TargetID != sourceThread.ID ||
		recommendedDigest.Kind != "recommended" || recommendedDigest.Title != "Front page pick" ||
		recommendedDigest.Path != "frontpage" || recommendedDigest.Note != "Worth reading." {
		t.Fatalf("recommended digest event = %+v, want deterministic thread recommendation", recommendedDigest)
	}
	recommendPartition := LogPartition{Kind: partitionBoard, Key: nativeRecommendSystemBoardID}
	recommendEvents, err := eventStore.ReplayPartition(ctx, recommendPartition.Kind, recommendPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay recommend mirror events: %v", err)
	}
	if len(recommendEvents) != 3 || recommendEvents[0].Kind != proto.EvtBoardCreated ||
		recommendEvents[1].Kind != proto.EvtThreadNew || recommendEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("recommend mirror events = %+v, want board, thread, post", recommendEvents)
	}
	recommendThread, ok := recommendEvents[1].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("recommend mirror thread payload = %T, want ThreadNewPayload", recommendEvents[1].Payload)
	}
	if recommendThread.ID != "recommend_thr_"+recommendedDigest.ID || recommendThread.Board != nativeRecommendSystemBoardID ||
		recommendThread.Title != "Front page pick" || recommendThread.AuthorID != admin.ID || recommendThread.TS != 3234 {
		t.Fatalf("recommend mirror thread = %+v, want deterministic mirror thread", recommendThread)
	}
	recommendPost, ok := recommendEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("recommend mirror post payload = %T, want PostAppendedPayload", recommendEvents[2].Payload)
	}
	for _, want := range []string{"Front page pick", "Board: General (general)", "Kind: recommended", "Path: frontpage", "Note: Worth reading.", "From: alice", "First public body."} {
		if !strings.Contains(recommendPost.Body, want) {
			t.Fatalf("recommend mirror body missing %q:\n%s", want, recommendPost.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-curation-test",
		Partition: sourcePartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize recommended digest event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-curation-test",
		Partition: recommendPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize recommend mirror events: %v", err)
	}
	recommendThreads, err := c.ListThreads(nativeRecommendSystemBoardID, 10, 0)
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
	if len(retryRecommendEvents) != 1 || retryRecommendEvents[0].Kind != proto.EvtDigestEntryUpserted {
		t.Fatalf("recommended retry events = %+v, want digest event without duplicate mirror", retryRecommendEvents)
	}

	announcementPayload, err := json.Marshal(proto.CurateThreadPayload{
		Thread: sourceThread.ID,
		Kind:   "announcement",
		Title:  "Public announcement",
		Note:   "Campus-wide note.",
	})
	if err != nil {
		t.Fatalf("marshal announcement thread payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  threadPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-curation-announcement",
		Command:    proto.CmdCurateThread,
		Payload:    announcementPayload,
		EnqueuedAt: 4234,
	}); err != nil {
		t.Fatalf("produce announcement thread command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain announcement thread once: %v", err)
	}
	generalEvents, err = eventStore.ReplayPartition(ctx, sourcePartition.Kind, sourcePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay general events after announcement curation: %v", err)
	}
	if len(generalEvents) != 5 || generalEvents[4].Kind != proto.EvtDigestEntryUpserted {
		t.Fatalf("general events after announcement curation = %+v, want digest entry event", generalEvents)
	}
	announcementDigest, ok := generalEvents[4].Payload.(*proto.DigestEntryUpsertedPayload)
	if !ok {
		t.Fatalf("announcement digest payload = %T, want DigestEntryUpsertedPayload", generalEvents[4].Payload)
	}
	if announcementDigest.Kind != "announcement" || announcementDigest.Title != "Public announcement" ||
		announcementDigest.Note != "Campus-wide note." {
		t.Fatalf("announcement digest event = %+v, want deterministic announcement", announcementDigest)
	}
	announcementPartition := LogPartition{Kind: partitionBoard, Key: nativeAnnouncementSystemBoardID}
	announcementEvents, err := eventStore.ReplayPartition(ctx, announcementPartition.Kind, announcementPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay announcement mirror events: %v", err)
	}
	if len(announcementEvents) != 3 || announcementEvents[0].Kind != proto.EvtBoardCreated ||
		announcementEvents[1].Kind != proto.EvtThreadNew || announcementEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("announcement mirror events = %+v, want board, thread, post", announcementEvents)
	}
	announcementThread, ok := announcementEvents[1].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("announcement mirror thread payload = %T, want ThreadNewPayload", announcementEvents[1].Payload)
	}
	if announcementThread.ID != "ann_thr_"+announcementDigest.ID || announcementThread.Board != nativeAnnouncementSystemBoardID ||
		announcementThread.Title != "Public announcement" || announcementThread.AuthorID != admin.ID || announcementThread.TS != 4234 {
		t.Fatalf("announcement mirror thread = %+v, want deterministic mirror thread", announcementThread)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDigestEntryMaintenance(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Digest maintenance source",
		Body:  "Original source body.",
	})
	if err != nil {
		t.Fatalf("marshal source thread payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-maintenance-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce source thread command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain source thread once: %v", err)
	}
	sourceEvents, err := eventStore.ReplayPartition(ctx, sourcePartition.Kind, sourcePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay source events: %v", err)
	}
	if len(sourceEvents) != 2 {
		t.Fatalf("source events = %+v, want thread and post", sourceEvents)
	}
	maintenanceAfter := sourceEvents[len(sourceEvents)-1].PartitionOffset
	if maintenanceAfter <= 0 {
		maintenanceAfter = int64(len(sourceEvents))
	}
	sourceThread, ok := sourceEvents[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("source thread payload = %T, want ThreadNewPayload", sourceEvents[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-maintenance-test",
		Partition: sourcePartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize source events: %v", err)
	}
	sourcePosts, err := c.ListPosts(sourceThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}
	entryID, err := upsertDigestEntry(c.DB, "dig_maintenance", "general", "post", sourcePosts[0].ID, "archive", "Original title", "faq/original", "Original note", admin.ID)
	if err != nil {
		t.Fatalf("upsert digest entry: %v", err)
	}
	if _, err := upsertDigestEntry(c.DB, "dig_maintenance_conflict", "general", "post", sourcePosts[0].ID, "archive", "Conflict title", "faq/conflict", "", admin.ID); err != nil {
		t.Fatalf("upsert conflicting digest entry: %v", err)
	}
	commandPartition := LogPartition{Kind: partitionBoard, Key: entryID}

	newTitle := "Edited archive title"
	newPath := "faq/edited"
	newNote := "Edited note"
	updatePayload, err := json.Marshal(proto.UpdateDigestEntryPayload{
		Entry: entryID,
		Title: &newTitle,
		Path:  &newPath,
		Note:  &newNote,
	})
	if err != nil {
		t.Fatalf("marshal update digest payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-maintenance-forbidden",
		Command:    proto.CmdUpdateDigestEntry,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Message != "board curator permission required" || forbidden.Err.Retryable {
		t.Fatalf("update digest as non-curator = %+v, want terminal curator permission error", forbidden)
	}
	emptyTitle := ""
	emptyTitlePayload, err := json.Marshal(proto.UpdateDigestEntryPayload{
		Entry: entryID,
		Title: &emptyTitle,
	})
	if err != nil {
		t.Fatalf("marshal empty-title update payload: %v", err)
	}
	invalidTitle := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-empty-title",
		Command:    proto.CmdUpdateDigestEntry,
		Payload:    emptyTitlePayload,
		EnqueuedAt: 2234,
	})
	if invalidTitle.Err == nil || invalidTitle.Err.Code != proto.ErrValidationFailed || invalidTitle.Err.Message != "title is required" || invalidTitle.Err.Retryable {
		t.Fatalf("empty-title digest update = %+v, want terminal validation error", invalidTitle)
	}
	conflictPath := "faq/conflict"
	conflictPayload, err := json.Marshal(proto.UpdateDigestEntryPayload{
		Entry: entryID,
		Path:  &conflictPath,
	})
	if err != nil {
		t.Fatalf("marshal conflict update payload: %v", err)
	}
	conflict := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-conflict",
		Command:    proto.CmdUpdateDigestEntry,
		Payload:    conflictPayload,
		EnqueuedAt: 2234,
	})
	if conflict.Err == nil || conflict.Err.Code != proto.ErrConflict || conflict.Err.Message != "digest entry already exists at that path" || conflict.Err.Retryable {
		t.Fatalf("conflicting digest update = %+v, want terminal conflict", conflict)
	}

	updateRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-update",
		Command:    proto.CmdUpdateDigestEntry,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce update digest command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain update digest once: %v", err)
	}
	eventPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, maintenanceAfter, 10)
	if err != nil {
		t.Fatalf("replay update digest events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtDigestEntryUpdated {
		t.Fatalf("update digest events = %+v, want digest.entry_updated", events)
	}
	updateEvent, ok := events[0].Payload.(*proto.DigestEntryUpdatedPayload)
	if !ok {
		t.Fatalf("update digest payload = %T, want DigestEntryUpdatedPayload", events[0].Payload)
	}
	if updateEvent.ID != entryID || updateEvent.Board != "general" || updateEvent.TargetKind != "post" ||
		updateEvent.TargetID != sourcePosts[0].ID || updateEvent.Kind != "archive" ||
		updateEvent.Title != newTitle || updateEvent.Path != newPath || updateEvent.Note != newNote ||
		updateEvent.By != admin.ID || updateEvent.TS != 2234 {
		t.Fatalf("update digest event = %+v, want deterministic metadata update", updateEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-maintenance-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize update digest event: %v", err)
	}
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
	retryUpdatePayload, ok := retryUpdateEvents[0].Payload.(*proto.DigestEntryUpdatedPayload)
	if !ok {
		t.Fatalf("update digest retry payload = %T, want DigestEntryUpdatedPayload", retryUpdateEvents[0].Payload)
	}
	if retryUpdatePayload.ID != updateEvent.ID || retryUpdatePayload.Title != updateEvent.Title ||
		retryUpdatePayload.Path != updateEvent.Path || retryUpdatePayload.Note != updateEvent.Note {
		t.Fatalf("update digest retry payload = %+v, want stable payload %+v", retryUpdatePayload, updateEvent)
	}

	bodyPayload, err := json.Marshal(proto.SetDigestEntryBodyPayload{
		Entry: entryID,
		Body:  "Edited archive body.",
	})
	if err != nil {
		t.Fatalf("marshal set digest body payload: %v", err)
	}
	bodyRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-body",
		Command:    proto.CmdSetDigestEntryBody,
		Payload:    bodyPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce set digest body command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain set digest body once: %v", err)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, maintenanceAfter, 10)
	if err != nil {
		t.Fatalf("replay body digest events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtDigestEntryBodySet {
		t.Fatalf("body digest events = %+v, want digest.entry_body_set", events)
	}
	bodyEvent, ok := events[1].Payload.(*proto.DigestEntryBodySetPayload)
	if !ok {
		t.Fatalf("body digest payload = %T, want DigestEntryBodySetPayload", events[1].Payload)
	}
	if bodyEvent.ID != entryID || bodyEvent.Board != "general" || bodyEvent.Kind != "archive" ||
		bodyEvent.Body != "Edited archive body." || !bodyEvent.Edited || bodyEvent.By != admin.ID || bodyEvent.TS != 3234 {
		t.Fatalf("body digest event = %+v, want deterministic body edit", bodyEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-maintenance-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize body digest event: %v", err)
	}
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

	resetPayload, err := json.Marshal(proto.SetDigestEntryBodyPayload{
		Entry: entryID,
		Body:  "ignored on reset",
		Reset: true,
	})
	if err != nil {
		t.Fatalf("marshal reset digest body payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-reset",
		Command:    proto.CmdSetDigestEntryBody,
		Payload:    resetPayload,
		EnqueuedAt: 4234,
	}); err != nil {
		t.Fatalf("produce reset digest body command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain reset digest body once: %v", err)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, maintenanceAfter, 10)
	if err != nil {
		t.Fatalf("replay reset digest events: %v", err)
	}
	if len(events) != 3 || events[2].Kind != proto.EvtDigestEntryBodySet {
		t.Fatalf("reset digest events = %+v, want digest.entry_body_set reset", events)
	}
	resetEvent, ok := events[2].Payload.(*proto.DigestEntryBodySetPayload)
	if !ok {
		t.Fatalf("reset digest payload = %T, want DigestEntryBodySetPayload", events[2].Payload)
	}
	if resetEvent.ID != entryID || resetEvent.Body != "" || resetEvent.Edited || resetEvent.By != admin.ID || resetEvent.TS != 4234 {
		t.Fatalf("reset digest event = %+v, want deterministic reset", resetEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-maintenance-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize reset digest event: %v", err)
	}
	export, err = c.GetDigestExport(entryID)
	if err != nil {
		t.Fatalf("get reset digest export: %v", err)
	}
	if export == nil || export.Entry.BodyEdited || export.Body != "Original source body." {
		t.Fatalf("reset digest export = %+v, want source body restored", export)
	}

	removePayload, err := json.Marshal(proto.RemoveDigestEntryPayload{
		Entry: entryID,
	})
	if err != nil {
		t.Fatalf("marshal remove digest payload: %v", err)
	}
	removeRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-maintenance-remove",
		Command:    proto.CmdRemoveDigestEntry,
		Payload:    removePayload,
		EnqueuedAt: 5234,
	})
	if err != nil {
		t.Fatalf("produce remove digest command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain remove digest once: %v", err)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, maintenanceAfter, 10)
	if err != nil {
		t.Fatalf("replay remove digest events: %v", err)
	}
	if len(events) != 4 || events[3].Kind != proto.EvtDigestEntryRemoved {
		t.Fatalf("remove digest events = %+v, want digest.entry_removed", events)
	}
	removeEvent, ok := events[3].Payload.(*proto.DigestEntryRemovedPayload)
	if !ok {
		t.Fatalf("remove digest payload = %T, want DigestEntryRemovedPayload", events[3].Payload)
	}
	if removeEvent.ID != entryID || removeEvent.Board != "general" || removeEvent.Kind != "archive" ||
		removeEvent.By != admin.ID || removeEvent.TS != 5234 {
		t.Fatalf("remove digest event = %+v, want deterministic removal", removeEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-maintenance-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize remove digest event: %v", err)
	}
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
	retryRemovePayload, ok := retryRemoveEvents[0].Payload.(*proto.DigestEntryRemovedPayload)
	if !ok {
		t.Fatalf("remove digest retry payload = %T, want DigestEntryRemovedPayload", retryRemoveEvents[0].Payload)
	}
	if retryRemovePayload.ID != removeEvent.ID || retryRemovePayload.Board != removeEvent.Board ||
		retryRemovePayload.Kind != removeEvent.Kind || retryRemovePayload.By != removeEvent.By ||
		retryRemovePayload.TS != removeEvent.TS {
		t.Fatalf("remove digest retry payload = %+v, want stable payload %+v", retryRemovePayload, removeEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsSendDigestEntryMail(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Archive source",
		Body:  "Useful digest body.",
	})
	if err != nil {
		t.Fatalf("marshal source thread payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-digest-mail-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce source thread command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain source thread once: %v", err)
	}
	sourceEvents, err := eventStore.ReplayPartition(ctx, sourcePartition.Kind, sourcePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay source thread events: %v", err)
	}
	if len(sourceEvents) != 2 {
		t.Fatalf("source thread events = %+v, want thread and post", sourceEvents)
	}
	sourceThread, ok := sourceEvents[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("source thread payload = %T, want ThreadNewPayload", sourceEvents[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-mail-test",
		Partition: sourcePartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize source thread events: %v", err)
	}
	sourcePosts, err := c.ListPosts(sourceThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}
	entryID, err := upsertDigestEntry(c.DB, "dig_native", "general", "post", sourcePosts[0].ID, "archive", "Archive child", "faq/howto", "Useful digest note", bob.ID)
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

	mailPayload, err := json.Marshal(proto.SendDigestEntryMailPayload{
		Entry: entryID,
		To:    []string{"alice"},
		Note:  "Please keep this one.",
	})
	if err != nil {
		t.Fatalf("marshal digest mail payload: %v", err)
	}
	mailPartition := LogPartition{Kind: partitionMail, Key: entryID}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  mailPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-digest-mail",
		Command:    proto.CmdSendDigestEntryMail,
		Payload:    mailPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce digest mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain digest mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, mailPartition); err != nil || got != record.Offset {
		t.Fatalf("digest mail committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}
	mailEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, mailEventPartition.Kind, mailEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay digest mail events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtMailSent {
		t.Fatalf("digest mail events = %+v, want mail.sent", events)
	}
	mailEvent, ok := events[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("digest mail payload = %T, want MailSentPayload", events[0].Payload)
	}
	if mailEvent.ID == "" || mailEvent.FromUserID != bob.ID || len(mailEvent.ToUserIDs) != 1 ||
		mailEvent.ToUserIDs[0] != alice.ID || mailEvent.Subject != "Archive: Archive child" || mailEvent.TS != 2234 {
		t.Fatalf("digest mail event = %+v, want bob mail to alice", mailEvent)
	}
	for _, want := range []string{"Please keep this one.", "Archive child", "Board: General (general)", "Kind: archive", "Path: faq/howto", "Note: Useful digest note", "Useful digest body."} {
		if !strings.Contains(mailEvent.Body, want) {
			t.Fatalf("digest mail body missing %q:\n%s", want, mailEvent.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-mail-test",
		Partition: mailEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize digest mail event: %v", err)
	}
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
	if err := insertBoard(secretTx, "secret", "Secret", "Members only", "", 2); err != nil {
		secretTx.Rollback() //nolint:errcheck
		t.Fatalf("insert secret board: %v", err)
	}
	if err := secretTx.Commit(); err != nil {
		t.Fatalf("commit secret board setup: %v", err)
	}
	memberRead := true
	if err := setBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read mode: %v", err)
	}
	secretPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private archive source",
		Body:  "hidden digest body",
	})
	if err != nil {
		t.Fatalf("marshal private source payload: %v", err)
	}
	secretPartition := LogPartition{Kind: partitionBoard, Key: "secret"}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  secretPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-digest-mail-private-source",
		Command:    proto.CmdCreateThread,
		Payload:    secretPayload,
		EnqueuedAt: 3234,
	}); err != nil {
		t.Fatalf("produce private source command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain private source once: %v", err)
	}
	secretEvents, err := eventStore.ReplayPartition(ctx, secretPartition.Kind, secretPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay private source events: %v", err)
	}
	if len(secretEvents) != 2 {
		t.Fatalf("private source events = %+v, want thread and post", secretEvents)
	}
	secretThread, ok := secretEvents[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("private source thread payload = %T, want ThreadNewPayload", secretEvents[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-digest-mail-test",
		Partition: secretPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize private source events: %v", err)
	}
	secretPosts, err := c.ListPosts(secretThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list private source posts: %v", err)
	}
	if len(secretPosts) != 1 {
		t.Fatalf("private source posts = %+v, want one", secretPosts)
	}
	secretEntryID, err := upsertDigestEntry(c.DB, "dig_secret_native", "secret", "post", secretPosts[0].ID, "archive", "Secret archive", "", "", admin.ID)
	if err != nil {
		t.Fatalf("upsert private digest entry: %v", err)
	}
	privateMailPayload, err := json.Marshal(proto.SendDigestEntryMailPayload{
		Entry: secretEntryID,
		To:    []string{"alice"},
	})
	if err != nil {
		t.Fatalf("marshal private digest mail payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: secretEntryID},
		Offset:     2,
		ActorID:    bob.ID,
		CID:        "cid-native-digest-mail-private",
		Command:    proto.CmdSendDigestEntryMail,
		Payload:    privateMailPayload,
		EnqueuedAt: 4234,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("private digest mail denied = %+v, want terminal forbidden", denied)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsSetMailGroup(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	commandPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	invalidPayload, err := json.Marshal(proto.SetMailGroupPayload{
		Name:    "lab",
		Members: []string{"bob"},
	})
	if err != nil {
		t.Fatalf("marshal invalid mail group payload: %v", err)
	}
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group-invalid",
		Command:    proto.CmdSetMailGroup,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	if invalid.Err == nil || invalid.Err.Code != proto.ErrValidationFailed || invalid.Err.Retryable {
		t.Fatalf("invalid mail group decision = %+v, want terminal validation failure", invalid)
	}

	payload, err := json.Marshal(proto.SetMailGroupPayload{
		Name:    " lab ",
		Members: []string{"alice", "carol", "alice"},
	})
	if err != nil {
		t.Fatalf("marshal mail group payload: %v", err)
	}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group",
		Command:    proto.CmdSetMailGroup,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce mail group command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain mail group once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != record.Offset {
		t.Fatalf("mail group committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}

	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay mail group events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtMailGroupSet {
		t.Fatalf("mail group events = %+v, want one mail.group_set", events)
	}
	groupEvent, ok := events[0].Payload.(*proto.MailGroupSetPayload)
	if !ok {
		t.Fatalf("mail group payload = %T, want MailGroupSetPayload", events[0].Payload)
	}
	if groupEvent.ID == "" || groupEvent.OwnerID != bob.ID || groupEvent.Name != "lab" ||
		len(groupEvent.MemberIDs) != 2 || groupEvent.MemberIDs[0] != alice.ID ||
		groupEvent.MemberIDs[1] != carol.ID || groupEvent.TS != 2234 {
		t.Fatalf("mail group event = %+v, want normalized deduped group", groupEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mail-group-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize mail group event: %v", err)
	}
	groups, err := c.ListMailGroups(bob.ID)
	if err != nil {
		t.Fatalf("list mail groups: %v", err)
	}
	var lab *MailGroup
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

	invalidDeletePayload, err := json.Marshal(proto.DeleteMailGroupPayload{Group: " "})
	if err != nil {
		t.Fatalf("marshal invalid delete mail group payload: %v", err)
	}
	invalidDelete := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: "missing"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group-delete-invalid",
		Command:    proto.CmdDeleteMailGroup,
		Payload:    invalidDeletePayload,
		EnqueuedAt: 3234,
	})
	if invalidDelete.Err == nil || invalidDelete.Err.Code != proto.ErrValidationFailed ||
		invalidDelete.Err.Message != "group is required" || invalidDelete.Err.Retryable {
		t.Fatalf("invalid delete mail group decision = %+v, want terminal group-required failure", invalidDelete)
	}

	missingDeletePayload, err := json.Marshal(proto.DeleteMailGroupPayload{Group: "missing"})
	if err != nil {
		t.Fatalf("marshal missing delete mail group payload: %v", err)
	}
	missingDelete := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionMail, Key: "missing"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group-delete-missing",
		Command:    proto.CmdDeleteMailGroup,
		Payload:    missingDeletePayload,
		EnqueuedAt: 3234,
	})
	if missingDelete.Err == nil || missingDelete.Err.Code != proto.ErrNotFound ||
		missingDelete.Err.Message != "mail group not found" || missingDelete.Err.Retryable {
		t.Fatalf("missing delete mail group decision = %+v, want terminal not-found failure", missingDelete)
	}

	deletePayload, err := json.Marshal(proto.DeleteMailGroupPayload{Group: groupEvent.ID})
	if err != nil {
		t.Fatalf("marshal delete mail group payload: %v", err)
	}
	deletePartition := LogPartition{Kind: partitionMail, Key: groupEvent.ID}
	deleteRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    bob.ID,
		CID:        "cid-native-mail-group-delete",
		Command:    proto.CmdDeleteMailGroup,
		Payload:    deletePayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce delete mail group command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain delete mail group once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, deletePartition); err != nil || got != deleteRecord.Offset {
		t.Fatalf("delete mail group committed offset = %d, %v; want %d, nil", got, err, deleteRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay delete mail group events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtMailGroupDeleted {
		t.Fatalf("delete mail group events = %+v, want set then deleted", events)
	}
	deletedEvent, ok := events[1].Payload.(*proto.MailGroupDeletedPayload)
	if !ok {
		t.Fatalf("delete mail group payload = %T, want MailGroupDeletedPayload", events[1].Payload)
	}
	if deletedEvent.ID != groupEvent.ID || deletedEvent.OwnerID != bob.ID || deletedEvent.TS != 3234 {
		t.Fatalf("delete mail group event = %+v, want stable deleted group", deletedEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-mail-group-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize delete mail group event: %v", err)
	}
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
	retryDeleted, ok := retryDeleteEvents[0].Payload.(*proto.MailGroupDeletedPayload)
	if !ok {
		t.Fatalf("delete mail group retry payload = %T, want MailGroupDeletedPayload", retryDeleteEvents[0].Payload)
	}
	if retryDeleted.ID != deletedEvent.ID || retryDeleted.OwnerID != deletedEvent.OwnerID || retryDeleted.TS != deletedEvent.TS {
		t.Fatalf("delete mail group retry payload = %+v, want %+v", retryDeleted, deletedEvent)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsUpdateMail(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sendPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	sendPayload, err := json.Marshal(proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Campus plans",
		Body:    "Meet by the old terminal.",
	})
	if err != nil {
		t.Fatalf("marshal send mail payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sendPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-update-mail-send",
		Command:    proto.CmdSendMail,
		Payload:    sendPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce send mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain send mail once: %v", err)
	}
	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay send mail events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtMailSent {
		t.Fatalf("send mail events = %+v, want one mail.sent", events)
	}
	mailEvent, ok := events[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("send mail payload = %T, want MailSentPayload", events[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-update-mail-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize send mail event: %v", err)
	}

	updatePartition := LogPartition{Kind: partitionMail, Key: mailEvent.ID}
	emptyPayload, err := json.Marshal(proto.UpdateMailPayload{Mail: mailEvent.ID})
	if err != nil {
		t.Fatalf("marshal empty update mail payload: %v", err)
	}
	empty := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  updatePartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-update-mail-empty",
		Command:    proto.CmdUpdateMail,
		Payload:    emptyPayload,
		EnqueuedAt: 1734,
	})
	if empty.Err == nil || empty.Err.Code != proto.ErrValidationFailed || empty.Err.Retryable {
		t.Fatalf("empty update mail decision = %+v, want terminal validation failure", empty)
	}
	read := true
	carolPayload, err := json.Marshal(proto.UpdateMailPayload{Mail: mailEvent.ID, Read: &read})
	if err != nil {
		t.Fatalf("marshal carol update mail payload: %v", err)
	}
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  updatePartition,
		Offset:     2,
		ActorID:    carol.ID,
		CID:        "cid-native-update-mail-missing",
		Command:    proto.CmdUpdateMail,
		Payload:    carolPayload,
		EnqueuedAt: 1834,
	})
	if missing.Err == nil || missing.Err.Code != proto.ErrNotFound || missing.Err.Retryable {
		t.Fatalf("missing update mail decision = %+v, want terminal not found", missing)
	}

	kept := true
	mailbox := "Keep"
	updatePayload, err := json.Marshal(proto.UpdateMailPayload{
		Mail:    mailEvent.ID,
		Mailbox: &mailbox,
		Read:    &read,
		Kept:    &kept,
	})
	if err != nil {
		t.Fatalf("marshal update mail payload: %v", err)
	}
	updateRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  updatePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-update-mail",
		Command:    proto.CmdUpdateMail,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce update mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain update mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, updatePartition); err != nil || got != updateRecord.Offset {
		t.Fatalf("update mail committed offset = %d, %v; want %d, nil", got, err, updateRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay update mail events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtMailCopyUpdated {
		t.Fatalf("update mail events = %+v, want mail.sent then mail.copy_updated", events)
	}
	updateEvent, ok := events[1].Payload.(*proto.MailCopyUpdatedPayload)
	if !ok {
		t.Fatalf("update mail payload = %T, want MailCopyUpdatedPayload", events[1].Payload)
	}
	if updateEvent.Mail != mailEvent.ID || updateEvent.UserID != alice.ID || updateEvent.Mailbox == nil ||
		*updateEvent.Mailbox != "keep" || updateEvent.Read == nil || !*updateEvent.Read ||
		updateEvent.Kept == nil || !*updateEvent.Kept || updateEvent.TS != 2234 {
		t.Fatalf("update mail event = %+v, want normalized keep/read/kept update", updateEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-update-mail-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize update mail event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sendPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	sendPayload, err := json.Marshal(proto.SendMailPayload{
		To:      []string{"alice"},
		Subject: "Campus plans",
		Body:    "Please archive this.",
	})
	if err != nil {
		t.Fatalf("marshal send mail payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sendPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-delete-mail-send",
		Command:    proto.CmdSendMail,
		Payload:    sendPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce send mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain send mail once: %v", err)
	}
	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay send mail events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtMailSent {
		t.Fatalf("send mail events = %+v, want one mail.sent", events)
	}
	mailEvent, ok := events[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("send mail payload = %T, want MailSentPayload", events[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-delete-mail-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize send mail event: %v", err)
	}

	deletePartition := LogPartition{Kind: partitionMail, Key: mailEvent.ID}
	deletePayload, err := json.Marshal(proto.DeleteMailPayload{Mail: mailEvent.ID})
	if err != nil {
		t.Fatalf("marshal delete mail payload: %v", err)
	}
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  deletePartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-delete-mail-missing",
		Command:    proto.CmdDeleteMail,
		Payload:    deletePayload,
		EnqueuedAt: 1834,
	})
	if missing.Err == nil || missing.Err.Code != proto.ErrNotFound || missing.Err.Retryable {
		t.Fatalf("missing delete mail decision = %+v, want terminal not found", missing)
	}

	deleteRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-mail",
		Command:    proto.CmdDeleteMail,
		Payload:    deletePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce delete mail command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain delete mail once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, deletePartition); err != nil || got != deleteRecord.Offset {
		t.Fatalf("delete mail committed offset = %d, %v; want %d, nil", got, err, deleteRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay delete mail events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtMailCopyUpdated {
		t.Fatalf("delete mail events = %+v, want mail.sent then mail.copy_updated", events)
	}
	deleteEvent, ok := events[1].Payload.(*proto.MailCopyUpdatedPayload)
	if !ok {
		t.Fatalf("delete mail payload = %T, want MailCopyUpdatedPayload", events[1].Payload)
	}
	if deleteEvent.Mail != mailEvent.ID || deleteEvent.UserID != alice.ID || deleteEvent.Mailbox == nil ||
		*deleteEvent.Mailbox != "trash" || deleteEvent.Read != nil || deleteEvent.Kept != nil || deleteEvent.TS != 2234 {
		t.Fatalf("delete mail event = %+v, want trash mailbox update only", deleteEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-delete-mail-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize delete mail event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sendPartition := LogPartition{Kind: partitionMail, Key: bob.ID}
	for i, subject := range []string{"Campus one", "Campus two"} {
		payload, err := json.Marshal(proto.SendMailPayload{
			To:      []string{"alice"},
			Subject: subject,
			Body:    "Please archive this.",
		})
		if err != nil {
			t.Fatalf("marshal send mail payload %d: %v", i, err)
		}
		if _, err := commandLog.Produce(ctx, CommandLogRecord{
			Partition:  sendPartition,
			ActorID:    bob.ID,
			CID:        fmt.Sprintf("cid-native-delete-mail-range-send-%d", i),
			Command:    proto.CmdSendMail,
			Payload:    payload,
			EnqueuedAt: int64(1234 + i),
		}); err != nil {
			t.Fatalf("produce send mail command %d: %v", i, err)
		}
		if _, err := worker.DrainOnce(ctx); err != nil {
			t.Fatalf("drain send mail %d once: %v", i, err)
		}
	}

	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay send mail events: %v", err)
	}
	if len(events) != 2 || events[0].Kind != proto.EvtMailSent || events[1].Kind != proto.EvtMailSent {
		t.Fatalf("send mail events = %+v, want two mail.sent events", events)
	}
	firstMail, ok := events[0].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("first send mail payload = %T, want MailSentPayload", events[0].Payload)
	}
	secondMail, ok := events[1].Payload.(*proto.MailSentPayload)
	if !ok {
		t.Fatalf("second send mail payload = %T, want MailSentPayload", events[1].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-delete-mail-range-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize send mail events: %v", err)
	}

	rangePartition := LogPartition{Kind: partitionMail, Key: firstMail.ID}
	missingPayload, err := json.Marshal(proto.DeleteMailRangePayload{Mail: []string{firstMail.ID, "missing_mail"}})
	if err != nil {
		t.Fatalf("marshal missing range payload: %v", err)
	}
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  rangePartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-mail-range-missing",
		Command:    proto.CmdDeleteMailRange,
		Payload:    missingPayload,
		EnqueuedAt: 1834,
	})
	if missing.Err == nil || missing.Err.Code != proto.ErrNotFound || missing.Err.Retryable {
		t.Fatalf("missing range delete decision = %+v, want terminal not found", missing)
	}

	rangePayload, err := json.Marshal(proto.DeleteMailRangePayload{Mail: []string{firstMail.ID, secondMail.ID, firstMail.ID}})
	if err != nil {
		t.Fatalf("marshal range payload: %v", err)
	}
	rangeRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  rangePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-mail-range",
		Command:    proto.CmdDeleteMailRange,
		Payload:    rangePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce range delete command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain range delete once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, rangePartition); err != nil || got != rangeRecord.Offset {
		t.Fatalf("range delete committed offset = %d, %v; want %d, nil", got, err, rangeRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay range delete events: %v", err)
	}
	if len(events) != 4 || events[2].Kind != proto.EvtMailCopyUpdated || events[3].Kind != proto.EvtMailCopyUpdated {
		t.Fatalf("range delete events = %+v, want two sends then two copy updates", events)
	}
	deleted := map[string]bool{}
	for _, event := range events[2:] {
		updateEvent, ok := event.Payload.(*proto.MailCopyUpdatedPayload)
		if !ok {
			t.Fatalf("range delete payload = %T, want MailCopyUpdatedPayload", event.Payload)
		}
		if updateEvent.UserID != alice.ID || updateEvent.Mailbox == nil || *updateEvent.Mailbox != "trash" ||
			updateEvent.Read != nil || updateEvent.Kept != nil || updateEvent.TS != 2234 {
			t.Fatalf("range delete event = %+v, want trash update only", updateEvent)
		}
		deleted[updateEvent.Mail] = true
	}
	if !deleted[firstMail.ID] || !deleted[secondMail.ID] || len(deleted) != 2 {
		t.Fatalf("range delete event ids = %+v, want both unique mails", deleted)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-delete-mail-range-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize range delete events: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	if err := setDirectMessageSettings(c.DB, alice.ID, "friends"); err != nil {
		t.Fatalf("set alice direct-message policy: %v", err)
	}
	blockedPayload, err := json.Marshal(proto.SendDirectMessagePayload{
		To:   "alice",
		Body: "blocked short ping",
	})
	if err != nil {
		t.Fatalf("marshal blocked direct message payload: %v", err)
	}
	blocked := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "alice"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-blocked",
		Command:    proto.CmdSendDirectMessage,
		Payload:    blockedPayload,
		EnqueuedAt: 1234,
	})
	if blocked.Err == nil || blocked.Err.Code != proto.ErrForbidden || blocked.Err.Retryable {
		t.Fatalf("direct message blocked by friends policy = %+v, want terminal forbidden", blocked)
	}
	if err := setUserRelationship(c.DB, alice.ID, bob.ID, "friend", "", true); err != nil {
		t.Fatalf("set alice friends bob: %v", err)
	}

	commandPartition := LogPartition{Kind: partitionUser, Key: "alice"}
	payload, err := json.Marshal(proto.SendDirectMessagePayload{
		To:   "alice",
		Body: " Short ping ",
	})
	if err != nil {
		t.Fatalf("marshal direct message payload: %v", err)
	}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message",
		Command:    proto.CmdSendDirectMessage,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce direct message command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain direct message once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != record.Offset {
		t.Fatalf("direct message committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}

	eventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay direct message events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtDirectMessageSent {
		t.Fatalf("direct message events = %+v, want one direct_message.sent", events)
	}
	dmEvent, ok := events[0].Payload.(*proto.DirectMessageSentPayload)
	if !ok {
		t.Fatalf("direct message payload = %T, want DirectMessageSentPayload", events[0].Payload)
	}
	if dmEvent.ID == "" || dmEvent.FromUserID != bob.ID || dmEvent.From != "bob" ||
		dmEvent.ToUserID != alice.ID || dmEvent.To != "alice" || dmEvent.Body != "Short ping" || dmEvent.TS != 2234 {
		t.Fatalf("direct message event = %+v, want deterministic bob->alice message", dmEvent)
	}
	if dmEvent.ConversationID != nativeDirectConversationID(bob.ID, alice.ID) {
		t.Fatalf("conversation id = %q, want %q", dmEvent.ConversationID, nativeDirectConversationID(bob.ID, alice.ID))
	}

	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-direct-message-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize direct message event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sendPartition := LogPartition{Kind: partitionUser, Key: "alice"}
	sendPayload, err := json.Marshal(proto.SendDirectMessagePayload{
		To:   "alice",
		Body: "read me",
	})
	if err != nil {
		t.Fatalf("marshal direct message payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sendPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-read-send",
		Command:    proto.CmdSendDirectMessage,
		Payload:    sendPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce direct message command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain direct message send once: %v", err)
	}

	senderEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, senderEventPartition.Kind, senderEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay direct message send events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtDirectMessageSent {
		t.Fatalf("direct message send events = %+v, want one send event", events)
	}
	dmEvent, ok := events[0].Payload.(*proto.DirectMessageSentPayload)
	if !ok {
		t.Fatalf("direct message payload = %T, want DirectMessageSentPayload", events[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-direct-message-read-test",
		Partition: senderEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize direct message send event: %v", err)
	}
	unread, err := c.CountUnreadDirectMessages(alice.ID)
	if err != nil {
		t.Fatalf("count unread direct messages after send: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unread direct messages after send = %d, want 1", unread)
	}

	readPartition := LogPartition{Kind: partitionUser, Key: dmEvent.ID}
	readPayload, err := json.Marshal(proto.MarkDirectMessageReadPayload{Message: dmEvent.ID})
	if err != nil {
		t.Fatalf("marshal direct message read payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  readPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-read-sender",
		Command:    proto.CmdMarkDirectMessageRead,
		Payload:    readPayload,
		EnqueuedAt: 1734,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrNotFound || denied.Err.Retryable {
		t.Fatalf("sender direct-message read decision = %+v, want terminal not found", denied)
	}

	readRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  readPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-read",
		Command:    proto.CmdMarkDirectMessageRead,
		Payload:    readPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce direct message read command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain direct message read once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, readPartition); err != nil || got != readRecord.Offset {
		t.Fatalf("direct message read committed offset = %d, %v; want %d, nil", got, err, readRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, senderEventPartition.Kind, senderEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay direct message read events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtDirectMessageRead {
		t.Fatalf("direct message read events = %+v, want send then read", events)
	}
	readEvent, ok := events[1].Payload.(*proto.DirectMessageReadPayload)
	if !ok {
		t.Fatalf("direct message read payload = %T, want DirectMessageReadPayload", events[1].Payload)
	}
	if readEvent.MessageID != dmEvent.ID || readEvent.UserID != alice.ID || readEvent.ReadAt != 2234 || readEvent.TS != 2234 {
		t.Fatalf("direct message read event = %+v, want alice read at event timestamp", readEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-direct-message-read-test",
		Partition: senderEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize direct message read event: %v", err)
	}
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

	noopRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  readPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-read-noop",
		Command:    proto.CmdMarkDirectMessageRead,
		Payload:    readPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce no-op direct message read command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain no-op direct message read once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, readPartition); err != nil || got != noopRecord.Offset {
		t.Fatalf("no-op direct message read committed offset = %d, %v; want %d, nil", got, err, noopRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, senderEventPartition.Kind, senderEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay after no-op direct message read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events after no-op direct message read = %+v, want no additional event", events)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsDirectMessageDelete(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sendPayload, err := json.Marshal(proto.SendDirectMessagePayload{
		To:   "alice",
		Body: "delete me",
	})
	if err != nil {
		t.Fatalf("marshal direct message payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "alice"},
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-delete-send",
		Command:    proto.CmdSendDirectMessage,
		Payload:    sendPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce direct message command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain direct message send once: %v", err)
	}

	senderEventPartition := LogPartition{Kind: partitionUser, Key: bob.ID}
	events, err := eventStore.ReplayPartition(ctx, senderEventPartition.Kind, senderEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay direct message send events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtDirectMessageSent {
		t.Fatalf("direct message send events = %+v, want one send event", events)
	}
	dmEvent, ok := events[0].Payload.(*proto.DirectMessageSentPayload)
	if !ok {
		t.Fatalf("direct message payload = %T, want DirectMessageSentPayload", events[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-direct-message-delete-test",
		Partition: senderEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize direct message send event: %v", err)
	}

	deletePartition := LogPartition{Kind: partitionUser, Key: dmEvent.ID}
	deletePayload, err := json.Marshal(proto.DeleteDirectMessagePayload{Message: dmEvent.ID})
	if err != nil {
		t.Fatalf("marshal direct message delete payload: %v", err)
	}
	recipientDelete, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-delete-recipient",
		Command:    proto.CmdDeleteDirectMessage,
		Payload:    deletePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce recipient direct message delete command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain recipient direct message delete once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, deletePartition); err != nil || got != recipientDelete.Offset {
		t.Fatalf("recipient direct message delete committed offset = %d, %v; want %d, nil", got, err, recipientDelete.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, senderEventPartition.Kind, senderEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay recipient direct message delete events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtDirectMessageDeleted {
		t.Fatalf("recipient direct message delete events = %+v, want send then delete", events)
	}
	deleteEvent, ok := events[1].Payload.(*proto.DirectMessageDeletedPayload)
	if !ok {
		t.Fatalf("direct message delete payload = %T, want DirectMessageDeletedPayload", events[1].Payload)
	}
	if deleteEvent.MessageID != dmEvent.ID || deleteEvent.UserID != alice.ID || deleteEvent.SenderDeleted ||
		!deleteEvent.RecipientDeleted || deleteEvent.TS != 2234 {
		t.Fatalf("recipient direct message delete event = %+v, want recipient-only delete", deleteEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-direct-message-delete-test",
		Partition: senderEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize recipient direct message delete event: %v", err)
	}
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

	noopDelete, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-delete-recipient-noop",
		Command:    proto.CmdDeleteDirectMessage,
		Payload:    deletePayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce no-op recipient direct message delete command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain no-op recipient direct message delete once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, deletePartition); err != nil || got != noopDelete.Offset {
		t.Fatalf("no-op recipient direct message delete committed offset = %d, %v; want %d, nil", got, err, noopDelete.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, senderEventPartition.Kind, senderEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay after no-op recipient direct message delete: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events after no-op recipient direct message delete = %+v, want no additional event", events)
	}

	senderDelete, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  deletePartition,
		ActorID:    bob.ID,
		CID:        "cid-native-direct-message-delete-sender",
		Command:    proto.CmdDeleteDirectMessage,
		Payload:    deletePayload,
		EnqueuedAt: 4234,
	})
	if err != nil {
		t.Fatalf("produce sender direct message delete command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain sender direct message delete once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, deletePartition); err != nil || got != senderDelete.Offset {
		t.Fatalf("sender direct message delete committed offset = %d, %v; want %d, nil", got, err, senderDelete.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, senderEventPartition.Kind, senderEventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay sender direct message delete events: %v", err)
	}
	if len(events) != 3 || events[2].Kind != proto.EvtDirectMessageDeleted {
		t.Fatalf("sender direct message delete events = %+v, want second delete", events)
	}
	senderDeleteEvent, ok := events[2].Payload.(*proto.DirectMessageDeletedPayload)
	if !ok {
		t.Fatalf("sender direct message delete payload = %T, want DirectMessageDeletedPayload", events[2].Payload)
	}
	if senderDeleteEvent.UserID != bob.ID || !senderDeleteEvent.SenderDeleted ||
		senderDeleteEvent.RecipientDeleted || senderDeleteEvent.TS != 4234 {
		t.Fatalf("sender direct message delete event = %+v, want sender-only delete", senderDeleteEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-direct-message-delete-test",
		Partition: senderEventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize sender direct message delete event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	commandPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	invalidPayload, err := json.Marshal(proto.SetDirectMessageSettingsPayload{Policy: "strangers"})
	if err != nil {
		t.Fatalf("marshal invalid direct-message settings payload: %v", err)
	}
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-settings-invalid",
		Command:    proto.CmdSetDirectMessageSettings,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	if invalid.Err == nil || invalid.Err.Code != proto.ErrValidationFailed || invalid.Err.Retryable {
		t.Fatalf("invalid direct-message settings decision = %+v, want terminal validation failure", invalid)
	}

	payload, err := json.Marshal(proto.SetDirectMessageSettingsPayload{Policy: "friends-only"})
	if err != nil {
		t.Fatalf("marshal direct-message settings payload: %v", err)
	}
	record, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-direct-message-settings",
		Command:    proto.CmdSetDirectMessageSettings,
		Payload:    payload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce direct-message settings command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain direct-message settings once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != record.Offset {
		t.Fatalf("direct-message settings committed offset = %d, %v; want %d, nil", got, err, record.Offset)
	}

	events, err := eventStore.ReplayPartition(ctx, commandPartition.Kind, commandPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay direct-message settings events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtDirectMessageSettingsSet {
		t.Fatalf("direct-message settings events = %+v, want one settings event", events)
	}
	settingsEvent, ok := events[0].Payload.(*proto.DirectMessageSettingsSetPayload)
	if !ok {
		t.Fatalf("direct-message settings payload = %T, want DirectMessageSettingsSetPayload", events[0].Payload)
	}
	if settingsEvent.UserID != alice.ID || settingsEvent.Policy != "friends" || settingsEvent.TS != 2234 {
		t.Fatalf("direct-message settings event = %+v, want normalized friends policy", settingsEvent)
	}

	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-direct-message-settings-test",
		Partition: commandPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize direct-message settings event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	commandPartition := LogPartition{Kind: partitionUser, Key: "bob"}
	invalidPayload, err := json.Marshal(proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "enemy",
		Active: true,
	})
	if err != nil {
		t.Fatalf("marshal invalid relationship payload: %v", err)
	}
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-relationship-invalid",
		Command:    proto.CmdSetUserRelationship,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	if invalid.Err == nil || invalid.Err.Code != proto.ErrValidationFailed || invalid.Err.Retryable {
		t.Fatalf("invalid relationship decision = %+v, want terminal validation failure", invalid)
	}

	addPayload, err := json.Marshal(proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "friends",
		Active: true,
		Note:   " lab partner ",
	})
	if err != nil {
		t.Fatalf("marshal relationship add payload: %v", err)
	}
	addRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-relationship-add",
		Command:    proto.CmdSetUserRelationship,
		Payload:    addPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce relationship add command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain relationship add once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != addRecord.Offset {
		t.Fatalf("relationship add committed offset = %d, %v; want %d, nil", got, err, addRecord.Offset)
	}

	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay relationship events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtUserRelationshipSet {
		t.Fatalf("relationship events = %+v, want one relationship event", events)
	}
	addEvent, ok := events[0].Payload.(*proto.UserRelationshipSetPayload)
	if !ok {
		t.Fatalf("relationship payload = %T, want UserRelationshipSetPayload", events[0].Payload)
	}
	if addEvent.UserID != alice.ID || addEvent.TargetUserID != bob.ID || addEvent.Kind != "friend" ||
		!addEvent.Active || addEvent.Note != "lab partner" || addEvent.TS != 2234 {
		t.Fatalf("relationship add event = %+v, want normalized friend add", addEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-relationship-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize relationship add event: %v", err)
	}
	friends, err := c.ListSocialUsers(alice.ID, "friends", false)
	if err != nil {
		t.Fatalf("list friends after relationship add: %v", err)
	}
	if len(friends) != 1 || friends[0].Name != "bob" || friends[0].Note != "lab partner" {
		t.Fatalf("friends after relationship add = %+v, want bob with note", friends)
	}

	removePayload, err := json.Marshal(proto.SetUserRelationshipPayload{
		User:   "bob",
		Kind:   "follow",
		Active: false,
	})
	if err != nil {
		t.Fatalf("marshal relationship remove payload: %v", err)
	}
	removeRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-relationship-remove",
		Command:    proto.CmdSetUserRelationship,
		Payload:    removePayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce relationship remove command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain relationship remove once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != removeRecord.Offset {
		t.Fatalf("relationship remove committed offset = %d, %v; want %d, nil", got, err, removeRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay relationship remove events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtUserRelationshipSet {
		t.Fatalf("relationship remove events = %+v, want add then remove", events)
	}
	removeEvent, ok := events[1].Payload.(*proto.UserRelationshipSetPayload)
	if !ok {
		t.Fatalf("relationship remove payload = %T, want UserRelationshipSetPayload", events[1].Payload)
	}
	if removeEvent.UserID != alice.ID || removeEvent.TargetUserID != bob.ID || removeEvent.Kind != "friend" ||
		removeEvent.Active || removeEvent.TS != 3234 {
		t.Fatalf("relationship remove event = %+v, want normalized friend remove", removeEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-relationship-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize relationship remove event: %v", err)
	}
	friends, err = c.ListSocialUsers(alice.ID, "friends", false)
	if err != nil {
		t.Fatalf("list friends after relationship remove: %v", err)
	}
	if len(friends) != 0 {
		t.Fatalf("friends after relationship remove = %+v, want none", friends)
	}

	noopRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-relationship-remove-noop",
		Command:    proto.CmdSetUserRelationship,
		Payload:    removePayload,
		EnqueuedAt: 4234,
	})
	if err != nil {
		t.Fatalf("produce no-op relationship remove command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain no-op relationship remove once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != noopRecord.Offset {
		t.Fatalf("no-op relationship remove committed offset = %d, %v; want %d, nil", got, err, noopRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay after no-op relationship remove: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events after no-op relationship remove = %+v, want no additional event", events)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsLoginWatch(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}
	if err := setUserRelationship(c.DB, alice.ID, bob.ID, "friend", "", true); err != nil {
		t.Fatalf("seed alice friend bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	commandPartition := LogPartition{Kind: partitionUser, Key: "bob"}
	forbiddenPayload, err := json.Marshal(proto.SetLoginWatchPayload{User: "bob", Active: true})
	if err != nil {
		t.Fatalf("marshal forbidden login watch payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-login-watch-forbidden",
		Command:    proto.CmdSetLoginWatch,
		Payload:    forbiddenPayload,
		EnqueuedAt: 1234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Retryable {
		t.Fatalf("forbidden login watch decision = %+v, want terminal forbidden", forbidden)
	}

	watchPayload, err := json.Marshal(proto.SetLoginWatchPayload{User: "bob", Active: true})
	if err != nil {
		t.Fatalf("marshal login watch payload: %v", err)
	}
	watchRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-login-watch",
		Command:    proto.CmdSetLoginWatch,
		Payload:    watchPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce login watch command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain login watch once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != watchRecord.Offset {
		t.Fatalf("login watch committed offset = %d, %v; want %d, nil", got, err, watchRecord.Offset)
	}
	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay login watch events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtUserRelationshipSet {
		t.Fatalf("login watch events = %+v, want one relationship event", events)
	}
	watchEvent, ok := events[0].Payload.(*proto.UserRelationshipSetPayload)
	if !ok {
		t.Fatalf("login watch relationship payload = %T, want UserRelationshipSetPayload", events[0].Payload)
	}
	if watchEvent.UserID != alice.ID || watchEvent.TargetUserID != bob.ID || watchEvent.Kind != "login_watch" ||
		!watchEvent.Active || watchEvent.TS != 2234 {
		t.Fatalf("login watch relationship event = %+v, want active login watch", watchEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-login-watch-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize login watch event: %v", err)
	}
	if exists, err := nativeRelationshipExists(c.DB, alice.ID, bob.ID, "login_watch"); err != nil || !exists {
		t.Fatalf("login watch relationship exists = %v, %v; want true, nil", exists, err)
	}

	cancelPayload, err := json.Marshal(proto.SetLoginWatchPayload{User: "bob", Active: false})
	if err != nil {
		t.Fatalf("marshal login watch cancel payload: %v", err)
	}
	cancelRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-login-watch-cancel",
		Command:    proto.CmdSetLoginWatch,
		Payload:    cancelPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce login watch cancel command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain login watch cancel once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != cancelRecord.Offset {
		t.Fatalf("login watch cancel committed offset = %d, %v; want %d, nil", got, err, cancelRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay login watch cancel events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtUserRelationshipSet {
		t.Fatalf("login watch cancel events = %+v, want second relationship event", events)
	}
	cancelEvent, ok := events[1].Payload.(*proto.UserRelationshipSetPayload)
	if !ok {
		t.Fatalf("login watch cancel payload = %T, want UserRelationshipSetPayload", events[1].Payload)
	}
	if cancelEvent.UserID != alice.ID || cancelEvent.TargetUserID != bob.ID || cancelEvent.Kind != "login_watch" ||
		cancelEvent.Active || cancelEvent.TS != 3234 {
		t.Fatalf("login watch cancel event = %+v, want inactive login watch", cancelEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-login-watch-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize login watch cancel event: %v", err)
	}
	if exists, err := nativeRelationshipExists(c.DB, alice.ID, bob.ID, "login_watch"); err != nil || exists {
		t.Fatalf("login watch relationship after cancel = %v, %v; want false, nil", exists, err)
	}

	if err := setUserPresence(c.DB, bob.ID, "web", "active", "reading", "general", "", "General", "127.0.0.1", nowMS()); err != nil {
		t.Fatalf("seed bob presence: %v", err)
	}
	onlineRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-login-watch-online",
		Command:    proto.CmdSetLoginWatch,
		Payload:    watchPayload,
		EnqueuedAt: 4234,
	})
	if err != nil {
		t.Fatalf("produce online login watch command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain online login watch once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != onlineRecord.Offset {
		t.Fatalf("online login watch committed offset = %d, %v; want %d, nil", got, err, onlineRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay online login watch events: %v", err)
	}
	if len(events) != 4 || events[2].Kind != proto.EvtNotificationCreated || events[3].Kind != proto.EvtUserRelationshipSet {
		t.Fatalf("online login watch events = %+v, want notification then relationship clear", events)
	}
	notificationEvent, ok := events[2].Payload.(*proto.NotificationCreatedPayload)
	if !ok {
		t.Fatalf("online login watch notification payload = %T, want NotificationCreatedPayload", events[2].Payload)
	}
	if notificationEvent.ID != stableCommandLogDecisionID("notif_", onlineRecord, 0) ||
		notificationEvent.UserID != alice.ID || notificationEvent.Kind != "login" ||
		notificationEvent.Actor != "bob" || notificationEvent.ThreadID != "" ||
		notificationEvent.PostID != "" || notificationEvent.TS != 4234 {
		t.Fatalf("online login watch notification = %+v, want login notification for alice", notificationEvent)
	}
	clearEvent, ok := events[3].Payload.(*proto.UserRelationshipSetPayload)
	if !ok {
		t.Fatalf("online login watch clear payload = %T, want UserRelationshipSetPayload", events[3].Payload)
	}
	if clearEvent.UserID != alice.ID || clearEvent.TargetUserID != bob.ID || clearEvent.Kind != "login_watch" ||
		clearEvent.Active || clearEvent.TS != 4234 {
		t.Fatalf("online login watch clear event = %+v, want inactive login watch", clearEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-login-watch-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize online login watch events: %v", err)
	}
	notifications, err := c.ListNotifications(alice.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("list login watch notifications: %v", err)
	}
	if len(notifications) != 1 || notifications[0].Kind != "login" || notifications[0].Actor != "bob" ||
		notifications[0].ThreadID != "" || notifications[0].PostID != "" {
		t.Fatalf("login watch notifications = %+v, want one login notification from bob", notifications)
	}
	if exists, err := nativeRelationshipExists(c.DB, alice.ID, bob.ID, "login_watch"); err != nil || exists {
		t.Fatalf("login watch relationship after online notification = %v, %v; want false, nil", exists, err)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsBoardZap(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	missingPayload, err := json.Marshal(proto.SetBoardZapPayload{Board: "missing", Zapped: true})
	if err != nil {
		t.Fatalf("marshal missing board zap payload: %v", err)
	}
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-zap-missing",
		Command:    proto.CmdSetBoardZap,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	if missing.Err == nil || missing.Err.Code != proto.ErrNotFound || missing.Err.Retryable {
		t.Fatalf("missing board zap decision = %+v, want terminal not found", missing)
	}

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	zapPayload, err := json.Marshal(proto.SetBoardZapPayload{Board: "general", Zapped: true})
	if err != nil {
		t.Fatalf("marshal board zap payload: %v", err)
	}
	zapRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-zap",
		Command:    proto.CmdSetBoardZap,
		Payload:    zapPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce board zap command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board zap once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != zapRecord.Offset {
		t.Fatalf("board zap committed offset = %d, %v; want %d, nil", got, err, zapRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board zap events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtBoardZapSet {
		t.Fatalf("board zap events = %+v, want one board.zap_set", events)
	}
	zapEvent, ok := events[0].Payload.(*proto.BoardZapSetPayload)
	if !ok {
		t.Fatalf("board zap payload = %T, want BoardZapSetPayload", events[0].Payload)
	}
	if zapEvent.UserID != alice.ID || zapEvent.Board != "general" || !zapEvent.Zapped || zapEvent.TS != 2234 {
		t.Fatalf("board zap event = %+v, want alice zapping general", zapEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-zap-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board zap event: %v", err)
	}
	summaries, err := c.ListBoardSummaries(alice.ID, false)
	if err != nil {
		t.Fatalf("list board summaries after zap: %v", err)
	}
	general := findBoardSummaryForTest(summaries, "general")
	if general == nil || !general.Zapped {
		t.Fatalf("general summary after zap = %+v, want zapped", general)
	}

	clearPayload, err := json.Marshal(proto.SetBoardZapPayload{Board: "general", Zapped: false})
	if err != nil {
		t.Fatalf("marshal board zap clear payload: %v", err)
	}
	clearRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-zap-clear",
		Command:    proto.CmdSetBoardZap,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce board zap clear command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board zap clear once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != clearRecord.Offset {
		t.Fatalf("board zap clear committed offset = %d, %v; want %d, nil", got, err, clearRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board zap clear events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtBoardZapSet {
		t.Fatalf("board zap clear events = %+v, want second board.zap_set", events)
	}
	clearEvent, ok := events[1].Payload.(*proto.BoardZapSetPayload)
	if !ok {
		t.Fatalf("board zap clear payload = %T, want BoardZapSetPayload", events[1].Payload)
	}
	if clearEvent.UserID != alice.ID || clearEvent.Board != "general" || clearEvent.Zapped || clearEvent.TS != 3234 {
		t.Fatalf("board zap clear event = %+v, want alice clearing general zap", clearEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-zap-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board zap clear event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	missingBoardPayload, err := json.Marshal(proto.SetBoardFavoritePayload{Board: "missing", Favorite: true})
	if err != nil {
		t.Fatalf("marshal missing board favorite payload: %v", err)
	}
	missingBoard := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-board-favorite-missing-board",
		Command:    proto.CmdSetBoardFavorite,
		Payload:    missingBoardPayload,
		EnqueuedAt: 1234,
	})
	if missingBoard.Err == nil || missingBoard.Err.Code != proto.ErrNotFound || missingBoard.Err.Retryable {
		t.Fatalf("missing board favorite decision = %+v, want terminal not found", missingBoard)
	}

	techTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin tech board setup: %v", err)
	}
	if err := insertBoard(techTx, "tech", "Tech", "Technology discussion", "", 1); err != nil {
		techTx.Rollback() //nolint:errcheck
		t.Fatalf("insert tech board: %v", err)
	}
	if err := techTx.Commit(); err != nil {
		t.Fatalf("commit tech board setup: %v", err)
	}

	missingFolderPayload, err := json.Marshal(proto.SetBoardFavoritePayload{Board: "tech", Favorite: true, FolderID: "missing"})
	if err != nil {
		t.Fatalf("marshal missing folder favorite payload: %v", err)
	}
	missingFolder := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "tech"},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-board-favorite-missing-folder",
		Command:    proto.CmdSetBoardFavorite,
		Payload:    missingFolderPayload,
		EnqueuedAt: 1334,
	})
	if missingFolder.Err == nil || missingFolder.Err.Code != proto.ErrNotFound || missingFolder.Err.Retryable {
		t.Fatalf("missing folder favorite decision = %+v, want terminal not found", missingFolder)
	}

	partition := LogPartition{Kind: partitionBoard, Key: "tech"}
	position := 0
	favoritePayload, err := json.Marshal(proto.SetBoardFavoritePayload{Board: "tech", Favorite: true, Position: &position})
	if err != nil {
		t.Fatalf("marshal board favorite payload: %v", err)
	}
	favoriteRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-favorite",
		Command:    proto.CmdSetBoardFavorite,
		Payload:    favoritePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce board favorite command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board favorite once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != favoriteRecord.Offset {
		t.Fatalf("board favorite committed offset = %d, %v; want %d, nil", got, err, favoriteRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board favorite events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtBoardFavoriteSet {
		t.Fatalf("board favorite events = %+v, want one board.favorite_set", events)
	}
	favoriteEvent, ok := events[0].Payload.(*proto.BoardFavoriteSetPayload)
	if !ok {
		t.Fatalf("board favorite payload = %T, want BoardFavoriteSetPayload", events[0].Payload)
	}
	if favoriteEvent.UserID != alice.ID || favoriteEvent.Board != "tech" || !favoriteEvent.Favorite ||
		favoriteEvent.Position == nil || *favoriteEvent.Position != 0 || favoriteEvent.TS != 2234 {
		t.Fatalf("board favorite event = %+v, want alice favoriting tech at position 0", favoriteEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-favorite-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board favorite event: %v", err)
	}
	favorites, err := c.ListFavoriteBoards(alice.ID)
	if err != nil {
		t.Fatalf("list favorite boards after favorite: %v", err)
	}
	if !hasBoardForTest(favorites, "tech") {
		t.Fatalf("favorite boards after favorite = %+v, want tech", favorites)
	}

	clearPayload, err := json.Marshal(proto.SetBoardFavoritePayload{Board: "tech", Favorite: false})
	if err != nil {
		t.Fatalf("marshal board favorite clear payload: %v", err)
	}
	clearRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-board-favorite-clear",
		Command:    proto.CmdSetBoardFavorite,
		Payload:    clearPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce board favorite clear command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain board favorite clear once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != clearRecord.Offset {
		t.Fatalf("board favorite clear committed offset = %d, %v; want %d, nil", got, err, clearRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay board favorite clear events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtBoardFavoriteSet {
		t.Fatalf("board favorite clear events = %+v, want second board.favorite_set", events)
	}
	clearEvent, ok := events[1].Payload.(*proto.BoardFavoriteSetPayload)
	if !ok {
		t.Fatalf("board favorite clear payload = %T, want BoardFavoriteSetPayload", events[1].Payload)
	}
	if clearEvent.UserID != alice.ID || clearEvent.Board != "tech" || clearEvent.Favorite || clearEvent.TS != 3234 {
		t.Fatalf("board favorite clear event = %+v, want alice clearing tech favorite", clearEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-board-favorite-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize board favorite clear event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	emptyNamePayload, err := json.Marshal(proto.CreateFavoriteFolderPayload{Name: "   "})
	if err != nil {
		t.Fatalf("marshal empty favorite folder payload: %v", err)
	}
	emptyName := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-favorite-folder-empty-name",
		Command:    proto.CmdCreateFavoriteFolder,
		Payload:    emptyNamePayload,
		EnqueuedAt: 1234,
	})
	if emptyName.Err == nil || emptyName.Err.Code != proto.ErrValidationFailed || emptyName.Err.Retryable {
		t.Fatalf("empty favorite folder decision = %+v, want terminal validation failure", emptyName)
	}

	missingParentPayload, err := json.Marshal(proto.CreateFavoriteFolderPayload{Name: "Work", ParentID: "missing"})
	if err != nil {
		t.Fatalf("marshal missing parent favorite folder payload: %v", err)
	}
	missingParent := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-favorite-folder-missing-parent",
		Command:    proto.CmdCreateFavoriteFolder,
		Payload:    missingParentPayload,
		EnqueuedAt: 1334,
	})
	if missingParent.Err == nil || missingParent.Err.Code != proto.ErrNotFound || missingParent.Err.Retryable {
		t.Fatalf("missing parent favorite folder decision = %+v, want terminal not found", missingParent)
	}

	partition := LogPartition{Kind: partitionUser, Key: alice.ID}
	rootPayload, err := json.Marshal(proto.CreateFavoriteFolderPayload{Name: " Work "})
	if err != nil {
		t.Fatalf("marshal root favorite folder payload: %v", err)
	}
	rootRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-favorite-folder-root",
		Command:    proto.CmdCreateFavoriteFolder,
		Payload:    rootPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce root favorite folder command: %v", err)
	}
	rootID := stableCommandLogDecisionID("favfld_", rootRecord, 0)
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain root favorite folder once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != rootRecord.Offset {
		t.Fatalf("root favorite folder committed offset = %d, %v; want %d, nil", got, err, rootRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay root favorite folder events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtFavoriteFolderCreated {
		t.Fatalf("root favorite folder events = %+v, want one favorite_folder.created", events)
	}
	rootEvent, ok := events[0].Payload.(*proto.FavoriteFolderCreatedPayload)
	if !ok {
		t.Fatalf("root favorite folder payload = %T, want FavoriteFolderCreatedPayload", events[0].Payload)
	}
	if rootEvent.ID != rootID || rootEvent.UserID != alice.ID || rootEvent.ParentID != "" ||
		rootEvent.Name != "Work" || rootEvent.Position != 0 || rootEvent.TS != 2234 {
		t.Fatalf("root favorite folder event = %+v, want normalized Work root folder", rootEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-favorite-folder-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize root favorite folder event: %v", err)
	}
	tree, err := c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatalf("list favorite tree after root folder: %v", err)
	}
	rootFolder := findFavoriteFolderForTest(tree, rootID)
	if rootFolder == nil || rootFolder.Name != "Work" || rootFolder.ParentID != "" || rootFolder.Position != 0 {
		t.Fatalf("root favorite folder after materialize = %+v in tree %+v", rootFolder, tree)
	}

	childPosition := 0
	childPayload, err := json.Marshal(proto.CreateFavoriteFolderPayload{Name: "Child", ParentID: rootID, Position: &childPosition})
	if err != nil {
		t.Fatalf("marshal child favorite folder payload: %v", err)
	}
	childRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-favorite-folder-child",
		Command:    proto.CmdCreateFavoriteFolder,
		Payload:    childPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce child favorite folder command: %v", err)
	}
	childID := stableCommandLogDecisionID("favfld_", childRecord, 0)
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain child favorite folder once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != childRecord.Offset {
		t.Fatalf("child favorite folder committed offset = %d, %v; want %d, nil", got, err, childRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay child favorite folder events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtFavoriteFolderCreated {
		t.Fatalf("child favorite folder events = %+v, want second favorite_folder.created", events)
	}
	childEvent, ok := events[1].Payload.(*proto.FavoriteFolderCreatedPayload)
	if !ok {
		t.Fatalf("child favorite folder payload = %T, want FavoriteFolderCreatedPayload", events[1].Payload)
	}
	if childEvent.ID != childID || childEvent.UserID != alice.ID || childEvent.ParentID != rootID ||
		childEvent.Name != "Child" || childEvent.Position != 0 || childEvent.TS != 3234 {
		t.Fatalf("child favorite folder event = %+v, want child under root folder", childEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-favorite-folder-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize child favorite folder event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	const workID = "favfld_work"
	const projectID = "favfld_project"
	if err := createFavoriteFolder(c.DB, alice.ID, workID, "", "Work", nil); err != nil {
		t.Fatalf("create work folder setup: %v", err)
	}
	if err := createFavoriteFolder(c.DB, alice.ID, projectID, workID, "Projects", nil); err != nil {
		t.Fatalf("create project folder setup: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	missingPayload, err := json.Marshal(proto.UpdateFavoriteFolderPayload{Folder: "missing", Name: "Nope"})
	if err != nil {
		t.Fatalf("marshal missing update favorite folder payload: %v", err)
	}
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-update-favorite-folder-missing",
		Command:    proto.CmdUpdateFavoriteFolder,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	if missing.Err == nil || missing.Err.Code != proto.ErrNotFound || missing.Err.Retryable {
		t.Fatalf("missing favorite folder update decision = %+v, want terminal not found", missing)
	}

	selfParent := workID
	selfPayload, err := json.Marshal(proto.UpdateFavoriteFolderPayload{Folder: workID, ParentID: &selfParent})
	if err != nil {
		t.Fatalf("marshal self-parent update favorite folder payload: %v", err)
	}
	self := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: workID},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-update-favorite-folder-self",
		Command:    proto.CmdUpdateFavoriteFolder,
		Payload:    selfPayload,
		EnqueuedAt: 1334,
	})
	if self.Err == nil || self.Err.Code != proto.ErrValidationFailed || self.Err.Retryable {
		t.Fatalf("self-parent favorite folder update decision = %+v, want terminal validation failure", self)
	}

	descendantParent := projectID
	descendantPayload, err := json.Marshal(proto.UpdateFavoriteFolderPayload{Folder: workID, ParentID: &descendantParent})
	if err != nil {
		t.Fatalf("marshal descendant update favorite folder payload: %v", err)
	}
	descendant := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: workID},
		Offset:     3,
		ActorID:    alice.ID,
		CID:        "cid-native-update-favorite-folder-descendant",
		Command:    proto.CmdUpdateFavoriteFolder,
		Payload:    descendantPayload,
		EnqueuedAt: 1434,
	})
	if descendant.Err == nil || descendant.Err.Code != proto.ErrValidationFailed || descendant.Err.Retryable {
		t.Fatalf("descendant favorite folder update decision = %+v, want terminal validation failure", descendant)
	}

	rootParent := ""
	zero := 0
	updatePayload, err := json.Marshal(proto.UpdateFavoriteFolderPayload{
		Folder:   projectID,
		Name:     " Projects Renamed ",
		ParentID: &rootParent,
		Position: &zero,
	})
	if err != nil {
		t.Fatalf("marshal update favorite folder payload: %v", err)
	}
	commandPartition := LogPartition{Kind: partitionUser, Key: projectID}
	updateRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-update-favorite-folder",
		Command:    proto.CmdUpdateFavoriteFolder,
		Payload:    updatePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce update favorite folder command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain update favorite folder once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != updateRecord.Offset {
		t.Fatalf("update favorite folder committed offset = %d, %v; want %d, nil", got, err, updateRecord.Offset)
	}
	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay update favorite folder events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtFavoriteFolderUpdated {
		t.Fatalf("update favorite folder events = %+v, want one favorite_folder.updated", events)
	}
	updateEvent, ok := events[0].Payload.(*proto.FavoriteFolderUpdatedPayload)
	if !ok {
		t.Fatalf("update favorite folder payload = %T, want FavoriteFolderUpdatedPayload", events[0].Payload)
	}
	if updateEvent.ID != projectID || updateEvent.UserID != alice.ID || updateEvent.ParentID != "" ||
		updateEvent.Name != "Projects Renamed" || updateEvent.Position != 0 || updateEvent.TS != 2234 {
		t.Fatalf("update favorite folder event = %+v, want renamed root project folder", updateEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-update-favorite-folder-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize update favorite folder event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	techTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin tech board setup: %v", err)
	}
	if err := insertBoard(techTx, "tech", "Tech", "Technology discussion", "", 1); err != nil {
		techTx.Rollback() //nolint:errcheck
		t.Fatalf("insert tech board: %v", err)
	}
	if err := techTx.Commit(); err != nil {
		t.Fatalf("commit tech board setup: %v", err)
	}
	const workID = "favfld_work"
	const childID = "favfld_child"
	if err := createFavoriteFolder(c.DB, alice.ID, workID, "", "Work", nil); err != nil {
		t.Fatalf("create work folder setup: %v", err)
	}
	if err := createFavoriteFolder(c.DB, alice.ID, childID, workID, "Child", nil); err != nil {
		t.Fatalf("create child folder setup: %v", err)
	}
	if err := setBoardFavorite(c.DB, alice.ID, "tech", workID, nil, true); err != nil {
		t.Fatalf("favorite tech in work setup: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	missingPayload, err := json.Marshal(proto.DeleteFavoriteFolderPayload{Folder: "missing"})
	if err != nil {
		t.Fatalf("marshal missing delete favorite folder payload: %v", err)
	}
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-favorite-folder-missing",
		Command:    proto.CmdDeleteFavoriteFolder,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	if missing.Err == nil || missing.Err.Code != proto.ErrNotFound || missing.Err.Retryable {
		t.Fatalf("missing favorite folder delete decision = %+v, want terminal not found", missing)
	}

	commandPartition := LogPartition{Kind: partitionUser, Key: workID}
	deletePayload, err := json.Marshal(proto.DeleteFavoriteFolderPayload{Folder: workID})
	if err != nil {
		t.Fatalf("marshal delete favorite folder payload: %v", err)
	}
	deleteRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-delete-favorite-folder",
		Command:    proto.CmdDeleteFavoriteFolder,
		Payload:    deletePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce delete favorite folder command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain delete favorite folder once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != deleteRecord.Offset {
		t.Fatalf("delete favorite folder committed offset = %d, %v; want %d, nil", got, err, deleteRecord.Offset)
	}
	eventPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	events, err := eventStore.ReplayPartition(ctx, eventPartition.Kind, eventPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay delete favorite folder events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtFavoriteFolderDeleted {
		t.Fatalf("delete favorite folder events = %+v, want one favorite_folder.deleted", events)
	}
	deleteEvent, ok := events[0].Payload.(*proto.FavoriteFolderDeletedPayload)
	if !ok {
		t.Fatalf("delete favorite folder payload = %T, want FavoriteFolderDeletedPayload", events[0].Payload)
	}
	if deleteEvent.ID != workID || deleteEvent.UserID != alice.ID || deleteEvent.ParentID != "" || deleteEvent.TS != 2234 {
		t.Fatalf("delete favorite folder event = %+v, want work folder deleted to root", deleteEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-delete-favorite-folder-test",
		Partition: eventPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize delete favorite folder event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	techTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin tech board setup: %v", err)
	}
	if err := insertBoard(techTx, "tech", "Tech", "Technology discussion", "", 1); err != nil {
		techTx.Rollback() //nolint:errcheck
		t.Fatalf("insert tech board: %v", err)
	}
	if err := techTx.Commit(); err != nil {
		t.Fatalf("commit tech board setup: %v", err)
	}
	const workFolderID = "favfld_work"
	if err := createFavoriteFolder(c.DB, alice.ID, workFolderID, "", "Work", nil); err != nil {
		t.Fatalf("create work folder setup: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	missingBoardPayload, err := json.Marshal(proto.MoveBoardFavoritePayload{Board: "missing", FolderID: workFolderID})
	if err != nil {
		t.Fatalf("marshal missing board move favorite payload: %v", err)
	}
	missingBoard := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "missing"},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-move-favorite-missing-board",
		Command:    proto.CmdMoveBoardFavorite,
		Payload:    missingBoardPayload,
		EnqueuedAt: 1234,
	})
	if missingBoard.Err == nil || missingBoard.Err.Code != proto.ErrNotFound || missingBoard.Err.Retryable {
		t.Fatalf("missing board move favorite decision = %+v, want terminal not found", missingBoard)
	}

	missingFolderPayload, err := json.Marshal(proto.MoveBoardFavoritePayload{Board: "tech", FolderID: "missing"})
	if err != nil {
		t.Fatalf("marshal missing folder move favorite payload: %v", err)
	}
	missingFolder := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "tech"},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-move-favorite-missing-folder",
		Command:    proto.CmdMoveBoardFavorite,
		Payload:    missingFolderPayload,
		EnqueuedAt: 1334,
	})
	if missingFolder.Err == nil || missingFolder.Err.Code != proto.ErrNotFound || missingFolder.Err.Retryable {
		t.Fatalf("missing folder move favorite decision = %+v, want terminal not found", missingFolder)
	}

	partition := LogPartition{Kind: partitionBoard, Key: "tech"}
	position := 0
	movePayload, err := json.Marshal(proto.MoveBoardFavoritePayload{Board: "tech", FolderID: workFolderID, Position: &position})
	if err != nil {
		t.Fatalf("marshal move favorite payload: %v", err)
	}
	moveRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-move-favorite",
		Command:    proto.CmdMoveBoardFavorite,
		Payload:    movePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce move favorite command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain move favorite once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != moveRecord.Offset {
		t.Fatalf("move favorite committed offset = %d, %v; want %d, nil", got, err, moveRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay move favorite events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtBoardFavoriteSet {
		t.Fatalf("move favorite events = %+v, want one board.favorite_set", events)
	}
	moveEvent, ok := events[0].Payload.(*proto.BoardFavoriteSetPayload)
	if !ok {
		t.Fatalf("move favorite payload = %T, want BoardFavoriteSetPayload", events[0].Payload)
	}
	if moveEvent.UserID != alice.ID || moveEvent.Board != "tech" || !moveEvent.Favorite ||
		moveEvent.FolderID != workFolderID || moveEvent.Position == nil || *moveEvent.Position != 0 || moveEvent.TS != 2234 {
		t.Fatalf("move favorite event = %+v, want tech moved into work at position 0", moveEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-move-favorite-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize move favorite event: %v", err)
	}
	tree, err := c.ListFavoriteTree(alice.ID)
	if err != nil {
		t.Fatalf("list favorite tree after move: %v", err)
	}
	techFavorite := findFavoriteBoardEntryForTest(tree, "tech")
	if techFavorite == nil || techFavorite.FolderID != workFolderID || techFavorite.Position != 0 {
		t.Fatalf("tech favorite after move = %+v in tree %+v", techFavorite, tree)
	}

	rootPayload, err := json.Marshal(proto.MoveBoardFavoritePayload{Board: "tech"})
	if err != nil {
		t.Fatalf("marshal move favorite root payload: %v", err)
	}
	rootRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-move-favorite-root",
		Command:    proto.CmdMoveBoardFavorite,
		Payload:    rootPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce move favorite root command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain move favorite root once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != rootRecord.Offset {
		t.Fatalf("move favorite root committed offset = %d, %v; want %d, nil", got, err, rootRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay move favorite root events: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtBoardFavoriteSet {
		t.Fatalf("move favorite root events = %+v, want second board.favorite_set", events)
	}
	rootEvent, ok := events[1].Payload.(*proto.BoardFavoriteSetPayload)
	if !ok {
		t.Fatalf("move favorite root payload = %T, want BoardFavoriteSetPayload", events[1].Payload)
	}
	if rootEvent.UserID != alice.ID || rootEvent.Board != "tech" || !rootEvent.Favorite ||
		rootEvent.FolderID != "" || rootEvent.Position != nil || rootEvent.TS != 3234 {
		t.Fatalf("move favorite root event = %+v, want tech moved to root", rootEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-move-favorite-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize move favorite root event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

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
		if err := insertBoard(boardTx, board.id, board.name, board.description, "", i+1); err != nil {
			boardTx.Rollback() //nolint:errcheck
			t.Fatalf("insert board %s: %v", board.id, err)
		}
	}
	if err := boardTx.Commit(); err != nil {
		t.Fatalf("commit board setup: %v", err)
	}
	const oldFolderID = "favfld_old"
	if err := createFavoriteFolder(c.DB, alice.ID, oldFolderID, "", "Old", nil); err != nil {
		t.Fatalf("create old folder setup: %v", err)
	}
	if err := setBoardFavorite(c.DB, alice.ID, "old", oldFolderID, nil, true); err != nil {
		t.Fatalf("favorite old board setup: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	duplicatePayload, err := json.Marshal(proto.ImportFavoriteTreePayload{
		Folders: []proto.ImportFavoriteFolderPayload{
			{ID: "src", Name: "One"},
			{ID: "src", Name: "Two"},
		},
	})
	if err != nil {
		t.Fatalf("marshal duplicate import payload: %v", err)
	}
	duplicate := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-import-favorite-duplicate",
		Command:    proto.CmdImportFavoriteTree,
		Payload:    duplicatePayload,
		EnqueuedAt: 1234,
	})
	if duplicate.Err == nil || duplicate.Err.Code != proto.ErrValidationFailed || duplicate.Err.Retryable {
		t.Fatalf("duplicate favorite import decision = %+v, want terminal validation failure", duplicate)
	}

	cyclePayload, err := json.Marshal(proto.ImportFavoriteTreePayload{
		Folders: []proto.ImportFavoriteFolderPayload{
			{ID: "a", ParentID: "b", Name: "A"},
			{ID: "b", ParentID: "a", Name: "B"},
		},
	})
	if err != nil {
		t.Fatalf("marshal cycle import payload: %v", err)
	}
	cycle := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     2,
		ActorID:    alice.ID,
		CID:        "cid-native-import-favorite-cycle",
		Command:    proto.CmdImportFavoriteTree,
		Payload:    cyclePayload,
		EnqueuedAt: 1334,
	})
	if cycle.Err == nil || cycle.Err.Code != proto.ErrValidationFailed || cycle.Err.Retryable {
		t.Fatalf("cycle favorite import decision = %+v, want terminal validation failure", cycle)
	}

	missingBoardPayload, err := json.Marshal(proto.ImportFavoriteTreePayload{
		Boards: []proto.ImportFavoriteBoardPayload{{ID: "missing"}},
	})
	if err != nil {
		t.Fatalf("marshal missing board import payload: %v", err)
	}
	missingBoard := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: alice.ID},
		Offset:     3,
		ActorID:    alice.ID,
		CID:        "cid-native-import-favorite-missing-board",
		Command:    proto.CmdImportFavoriteTree,
		Payload:    missingBoardPayload,
		EnqueuedAt: 1434,
	})
	if missingBoard.Err == nil || missingBoard.Err.Code != proto.ErrValidationFailed || missingBoard.Err.Retryable {
		t.Fatalf("missing board favorite import decision = %+v, want terminal validation failure", missingBoard)
	}

	replace := true
	importPayload, err := json.Marshal(proto.ImportFavoriteTreePayload{
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
	if err != nil {
		t.Fatalf("marshal import favorite tree payload: %v", err)
	}
	partition := LogPartition{Kind: partitionUser, Key: alice.ID}
	importRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-import-favorite-tree",
		Command:    proto.CmdImportFavoriteTree,
		Payload:    importPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce import favorite tree command: %v", err)
	}
	rootID := stableCommandLogDecisionID("favfld_", importRecord, 0)
	childID := stableCommandLogDecisionID("favfld_", importRecord, 1)
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain import favorite tree once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, partition); err != nil || got != importRecord.Offset {
		t.Fatalf("import favorite tree committed offset = %d, %v; want %d, nil", got, err, importRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay import favorite tree events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtFavoriteTreeImported {
		t.Fatalf("import favorite tree events = %+v, want one favorite_tree.imported", events)
	}
	importEvent, ok := events[0].Payload.(*proto.FavoriteTreeImportedPayload)
	if !ok {
		t.Fatalf("import favorite tree payload = %T, want FavoriteTreeImportedPayload", events[0].Payload)
	}
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
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-import-favorite-tree-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize import favorite tree event: %v", err)
	}
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
	retryEvent, ok := retryEvents[0].Payload.(*proto.FavoriteTreeImportedPayload)
	if !ok {
		t.Fatalf("retry import favorite tree payload = %T, want FavoriteTreeImportedPayload", retryEvents[0].Payload)
	}
	if len(retryEvent.Folders) != 2 || retryEvent.Folders[0].ID != rootID || retryEvent.Folders[1].ID != childID ||
		len(retryEvent.Boards) != 2 || retryEvent.Boards[0].FolderID != childID {
		t.Fatalf("retry import favorite tree payload = %+v, want same mapped folder ids", retryEvent)
	}
}

func findBoardSummaryForTest(summaries []BoardSummary, boardID string) *BoardSummary {
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

func findFavoriteFolderForTest(tree *FavoriteTree, folderID string) *FavoriteFolder {
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

func findFavoriteBoardEntryForTest(tree *FavoriteTree, boardID string) *FavoriteBoardEntry {
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	campusTx, err := c.DB.Begin()
	if err != nil {
		t.Fatalf("begin campus board setup: %v", err)
	}
	if err := insertBoard(campusTx, "campus", "Campus", "Shared campus notes", "", 1); err != nil {
		campusTx.Rollback() //nolint:errcheck
		t.Fatalf("insert campus board: %v", err)
	}
	if err := campusTx.Commit(); err != nil {
		t.Fatalf("commit campus board setup: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	sourcePartition := LogPartition{Kind: partitionBoard, Key: "general"}
	sourcePayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Original article",
		Body:  "source body",
	})
	if err != nil {
		t.Fatalf("marshal source create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  sourcePartition,
		ActorID:    alice.ID,
		CID:        "cid-native-repost-source",
		Command:    proto.CmdCreateThread,
		Payload:    sourcePayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce source create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain source create once: %v", err)
	}
	sourceEvents, err := eventStore.ReplayPartition(ctx, sourcePartition.Kind, sourcePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay source events: %v", err)
	}
	if len(sourceEvents) != 2 {
		t.Fatalf("source events = %+v, want thread and post", sourceEvents)
	}
	sourceThreadPayload, ok := sourceEvents[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("source thread payload = %T, want ThreadNewPayload", sourceEvents[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-repost-test",
		Partition: sourcePartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize source events: %v", err)
	}
	sourcePosts, err := c.ListPosts(sourceThreadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list source posts: %v", err)
	}
	if len(sourcePosts) != 1 {
		t.Fatalf("source posts = %+v, want one", sourcePosts)
	}

	repostPartition := LogPartition{Kind: partitionBoard, Key: "campus"}
	repostPayload, err := json.Marshal(proto.RepostPostPayload{
		Post:  sourcePosts[0].ID,
		Board: "campus",
		Title: "Shared original article",
	})
	if err != nil {
		t.Fatalf("marshal repost payload: %v", err)
	}
	repostRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  repostPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-repost-post",
		Command:    proto.CmdRepostPost,
		Payload:    repostPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce repost command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain repost once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, repostPartition); err != nil || got != repostRecord.Offset {
		t.Fatalf("repost committed offset = %d, %v; want %d, nil", got, err, repostRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, repostPartition.Kind, repostPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay repost events: %v", err)
	}
	if len(events) != 2 || events[0].Kind != proto.EvtThreadNew || events[1].Kind != proto.EvtPostAppended {
		t.Fatalf("repost events = %+v, want thread.new then post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("repost thread payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	postPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("repost post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
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

	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-repost-test",
		Partition: repostPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize repost events: %v", err)
	}
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
	if err := insertBoard(secretTx, "secret", "Secret", "Members only", "", 2); err != nil {
		secretTx.Rollback() //nolint:errcheck
		t.Fatalf("insert secret board: %v", err)
	}
	if err := secretTx.Commit(); err != nil {
		t.Fatalf("commit secret board setup: %v", err)
	}
	memberRead := true
	if err := setBoardSettings(c.DB, "secret", BoardSettingsPatch{MemberReadMode: &memberRead}); err != nil {
		t.Fatalf("set secret member-read mode: %v", err)
	}
	secretPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "secret",
		Title: "Private article",
		Body:  "hidden source",
	})
	if err != nil {
		t.Fatalf("marshal private source create payload: %v", err)
	}
	secretPartition := LogPartition{Kind: partitionBoard, Key: "secret"}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  secretPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-repost-private-source-create",
		Command:    proto.CmdCreateThread,
		Payload:    secretPayload,
		EnqueuedAt: 3234,
	}); err != nil {
		t.Fatalf("produce private source create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain private source create once: %v", err)
	}
	secretEvents, err := eventStore.ReplayPartition(ctx, secretPartition.Kind, secretPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay private source events: %v", err)
	}
	if len(secretEvents) != 2 {
		t.Fatalf("private source events = %+v, want thread and post", secretEvents)
	}
	secretThreadPayload, ok := secretEvents[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("private source thread payload = %T, want ThreadNewPayload", secretEvents[0].Payload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-repost-test",
		Partition: secretPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize private source events: %v", err)
	}
	secretPosts, err := c.ListPosts(secretThreadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list private source posts: %v", err)
	}
	if len(secretPosts) != 1 {
		t.Fatalf("private source posts = %+v, want one", secretPosts)
	}
	privateRepostPayload, err := json.Marshal(proto.RepostPostPayload{
		Post:  secretPosts[0].ID,
		Board: "campus",
	})
	if err != nil {
		t.Fatalf("marshal private repost payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  repostPartition,
		Offset:     2,
		ActorID:    bob.ID,
		CID:        "cid-native-repost-private-source",
		Command:    proto.CmdRepostPost,
		Payload:    privateRepostPayload,
		EnqueuedAt: 3334,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("private source repost denied = %+v, want terminal forbidden", denied)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsRoleChanges(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	userPartition := LogPartition{Kind: partitionUser, Key: alice.ID}
	grantPayload, err := json.Marshal(proto.GrantRolePayload{User: alice.ID, Role: "moderator"})
	if err != nil {
		t.Fatalf("marshal grant payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  userPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-grant-role-denied",
		Command:    proto.CmdGrantRole,
		Payload:    grantPayload,
		EnqueuedAt: 1234,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("grant role as non-admin = %+v, want terminal forbidden", denied)
	}
	missingPayload, err := json.Marshal(proto.GrantRolePayload{User: "usr_missing", Role: "moderator"})
	if err != nil {
		t.Fatalf("marshal missing grant payload: %v", err)
	}
	missing := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionUser, Key: "usr_missing"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-grant-role-missing",
		Command:    proto.CmdGrantRole,
		Payload:    missingPayload,
		EnqueuedAt: 1234,
	})
	if missing.Err == nil || missing.Err.Code != proto.ErrNotFound || missing.Err.Retryable {
		t.Fatalf("grant missing user = %+v, want terminal not found", missing)
	}

	grantRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  userPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-grant-role",
		Command:    proto.CmdGrantRole,
		Payload:    grantPayload,
		EnqueuedAt: 1234,
	})
	if err != nil {
		t.Fatalf("produce grant role command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain grant role once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, userPartition); err != nil || got != grantRecord.Offset {
		t.Fatalf("grant committed offset = %d, %v; want %d, nil", got, err, grantRecord.Offset)
	}
	accountEvents, err := eventStore.ReplayPartition(ctx, userPartition.Kind, userPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay account role events: %v", err)
	}
	if len(accountEvents) != 1 || accountEvents[0].Kind != proto.EvtRoleGranted {
		t.Fatalf("account role events = %+v, want one role.granted", accountEvents)
	}
	grantEvent, ok := accountEvents[0].Payload.(*proto.RoleGrantedPayload)
	if !ok {
		t.Fatalf("grant payload = %T, want RoleGrantedPayload", accountEvents[0].Payload)
	}
	if grantEvent.User != alice.ID || grantEvent.Role != "moderator" || grantEvent.By != admin.ID || grantEvent.TS != 1234 {
		t.Fatalf("grant event = %+v, want deterministic role grant", grantEvent)
	}
	syssecurityPartition := LogPartition{Kind: partitionBoard, Key: nativeSyssecuritySystemBoardID}
	syssecurityEvents, err := eventStore.ReplayPartition(ctx, syssecurityPartition.Kind, syssecurityPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay syssecurity grant events: %v", err)
	}
	if len(syssecurityEvents) != 3 ||
		syssecurityEvents[0].Kind != proto.EvtBoardCreated ||
		syssecurityEvents[1].Kind != proto.EvtThreadNew ||
		syssecurityEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("syssecurity grant events = %+v, want board/thread/post audit events", syssecurityEvents)
	}
	grantAuditPost, ok := syssecurityEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("grant audit payload = %T, want PostAppendedPayload", syssecurityEvents[2].Payload)
	}
	for _, want := range []string{"Action: role granted", "User: alice", "Role: moderator", "Actor: admin"} {
		if !strings.Contains(grantAuditPost.Body, want) {
			t.Fatalf("grant audit body missing %q:\n%s", want, grantAuditPost.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-role-change-test",
		Partition: userPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize grant account event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-role-change-test",
		Partition: syssecurityPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize grant syssecurity events: %v", err)
	}
	aliceUser, err := getUserByID(c.DB, alice.ID)
	if err != nil {
		t.Fatalf("get alice after grant: %v", err)
	}
	if aliceUser == nil || aliceUser.Role != "moderator" {
		t.Fatalf("alice after grant = %+v, want moderator role", aliceUser)
	}
	syssecurityThreads, err := c.ListThreads(nativeSyssecuritySystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list syssecurity after grant: %v", err)
	}
	if len(syssecurityThreads) != 1 {
		t.Fatalf("syssecurity threads after grant = %+v, want one audit thread", syssecurityThreads)
	}

	revokePayload, err := json.Marshal(proto.RevokeRolePayload{User: alice.ID, Role: "moderator"})
	if err != nil {
		t.Fatalf("marshal revoke payload: %v", err)
	}
	revokeRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  userPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-revoke-role",
		Command:    proto.CmdRevokeRole,
		Payload:    revokePayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce revoke role command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain revoke role once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, userPartition); err != nil || got != revokeRecord.Offset {
		t.Fatalf("revoke committed offset = %d, %v; want %d, nil", got, err, revokeRecord.Offset)
	}
	accountEvents, err = eventStore.ReplayPartition(ctx, userPartition.Kind, userPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay account role events after revoke: %v", err)
	}
	if len(accountEvents) != 2 || accountEvents[1].Kind != proto.EvtRoleRevoked {
		t.Fatalf("account role events after revoke = %+v, want role.granted then role.revoked", accountEvents)
	}
	revokeEvent, ok := accountEvents[1].Payload.(*proto.RoleRevokedPayload)
	if !ok {
		t.Fatalf("revoke payload = %T, want RoleRevokedPayload", accountEvents[1].Payload)
	}
	if revokeEvent.User != alice.ID || revokeEvent.Role != "moderator" || revokeEvent.By != admin.ID || revokeEvent.TS != 2234 {
		t.Fatalf("revoke event = %+v, want deterministic role revoke", revokeEvent)
	}
	syssecurityEvents, err = eventStore.ReplayPartition(ctx, syssecurityPartition.Kind, syssecurityPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay syssecurity events after revoke: %v", err)
	}
	if len(syssecurityEvents) != 5 ||
		syssecurityEvents[3].Kind != proto.EvtThreadNew ||
		syssecurityEvents[4].Kind != proto.EvtPostAppended {
		t.Fatalf("syssecurity events after revoke = %+v, want grant board/thread/post then revoke thread/post", syssecurityEvents)
	}
	revokeAuditPost, ok := syssecurityEvents[4].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("revoke audit payload = %T, want PostAppendedPayload", syssecurityEvents[4].Payload)
	}
	for _, want := range []string{"Action: role revoked", "User: alice", "Role: moderator", "Actor: admin"} {
		if !strings.Contains(revokeAuditPost.Body, want) {
			t.Fatalf("revoke audit body missing %q:\n%s", want, revokeAuditPost.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-role-change-test",
		Partition: userPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize revoke account event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-role-change-test",
		Partition: syssecurityPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize revoke syssecurity events: %v", err)
	}
	aliceUser, err = getUserByID(c.DB, alice.ID)
	if err != nil {
		t.Fatalf("get alice after revoke: %v", err)
	}
	if aliceUser == nil || aliceUser.Role != "user" {
		t.Fatalf("alice after revoke = %+v, want user role", aliceUser)
	}
	syssecurityThreads, err = c.ListThreads(nativeSyssecuritySystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list syssecurity after revoke: %v", err)
	}
	if len(syssecurityThreads) != 2 {
		t.Fatalf("syssecurity threads after revoke = %+v, want two audit threads", syssecurityThreads)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsPublishStatsSnapshot(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	commandPartition := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}
	statsPayload, err := json.Marshal(proto.PublishStatsSnapshotPayload{Date: "2026-06-04"})
	if err != nil {
		t.Fatalf("marshal stats snapshot payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-stats-denied",
		Command:    proto.CmdPublishStatsSnapshot,
		Payload:    statsPayload,
		EnqueuedAt: 1234,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("publish stats as non-admin = %+v, want terminal forbidden", denied)
	}
	invalidPayload, err := json.Marshal(proto.PublishStatsSnapshotPayload{Date: "06/04/2026"})
	if err != nil {
		t.Fatalf("marshal invalid stats snapshot payload: %v", err)
	}
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  commandPartition,
		Offset:     2,
		ActorID:    admin.ID,
		CID:        "cid-native-stats-invalid",
		Command:    proto.CmdPublishStatsSnapshot,
		Payload:    invalidPayload,
		EnqueuedAt: 1334,
	})
	if invalid.Err == nil || invalid.Err.Code != proto.ErrValidationFailed || invalid.Err.Retryable {
		t.Fatalf("publish stats invalid date = %+v, want terminal validation", invalid)
	}

	statsRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-stats-snapshot",
		Command:    proto.CmdPublishStatsSnapshot,
		Payload:    statsPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce stats snapshot command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain stats snapshot once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != statsRecord.Offset {
		t.Fatalf("stats snapshot committed offset = %d, %v; want %d, nil", got, err, statsRecord.Offset)
	}

	globalEvents, err := eventStore.ReplayPartition(ctx, partitionGlobal, partitionGlobal, 0, 10)
	if err != nil {
		t.Fatalf("replay stats global events: %v", err)
	}
	if len(globalEvents) != 1 || globalEvents[0].Kind != proto.EvtCommunityStatsSnapshotRecorded {
		t.Fatalf("stats global events = %+v, want one community_stats.snapshot_recorded", globalEvents)
	}
	snapshotEvent, ok := globalEvents[0].Payload.(*proto.CommunityStatsSnapshotRecordedPayload)
	if !ok {
		t.Fatalf("stats global payload = %T, want CommunityStatsSnapshotRecordedPayload", globalEvents[0].Payload)
	}
	if snapshotEvent.Day != "2026-06-04" || snapshotEvent.SnapshotAt != 2234 || snapshotEvent.TotalUsers != 2 {
		t.Fatalf("stats snapshot event = %+v, want 2026-06-04 current stats", snapshotEvent)
	}

	boardEvents, err := eventStore.ReplayPartition(ctx, partitionBoard, "BBSLists", 0, 100)
	if err != nil {
		t.Fatalf("replay BBSLists events: %v", err)
	}
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

	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-stats-snapshot-test",
		Partition: commandPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize stats global event: %v", err)
	}
	statsBoardPartition := LogPartition{Kind: partitionBoard, Key: "BBSLists"}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-stats-snapshot-test",
		Partition: statsBoardPartition,
		Limit:     100,
	}); err != nil {
		t.Fatalf("materialize BBSLists events: %v", err)
	}
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

	weeklyPayload, err := json.Marshal(proto.PublishStatsSnapshotPayload{Date: "2026-06-07"})
	if err != nil {
		t.Fatalf("marshal weekly stats snapshot payload: %v", err)
	}
	weeklyRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  commandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-stats-snapshot-weekly",
		Command:    proto.CmdPublishStatsSnapshot,
		Payload:    weeklyPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce weekly stats snapshot command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain weekly stats snapshot once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, commandPartition); err != nil || got != weeklyRecord.Offset {
		t.Fatalf("weekly stats snapshot committed offset = %d, %v; want %d, nil", got, err, weeklyRecord.Offset)
	}
	weeklyBoardEvents, err := eventStore.ReplayPartition(ctx, partitionBoard, "BBSLists", 29, 100)
	if err != nil {
		t.Fatalf("replay weekly BBSLists events: %v", err)
	}
	if len(weeklyBoardEvents) != 32 {
		t.Fatalf("weekly BBSLists event count = %d, want 16 thread/post pairs: %+v", len(weeklyBoardEvents), weeklyBoardEvents)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-stats-snapshot-test",
		Partition: commandPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize weekly stats global event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-stats-snapshot-test",
		Partition: statsBoardPartition,
		Limit:     100,
	}); err != nil {
		t.Fatalf("materialize weekly BBSLists events: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	defaultCommandPartition := LogPartition{Kind: partitionBoard, Key: partitionGlobal}
	noticePayload, err := json.Marshal(proto.PublishSystemNoticePayload{
		Title:  "Campus notice",
		Body:   "Maintenance tonight at 23:00.",
		Source: "operator broadcast",
	})
	if err != nil {
		t.Fatalf("marshal notice payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  defaultCommandPartition,
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-system-notice-denied",
		Command:    proto.CmdPublishSystemNotice,
		Payload:    noticePayload,
		EnqueuedAt: 1234,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("publish system notice as non-admin = %+v, want terminal forbidden", denied)
	}
	invalidPayload, err := json.Marshal(proto.PublishSystemNoticePayload{
		Board: "Filter",
		Title: "Filtered",
		Body:  "not a public notice board",
	})
	if err != nil {
		t.Fatalf("marshal invalid notice payload: %v", err)
	}
	invalid := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "Filter"},
		Offset:     1,
		ActorID:    admin.ID,
		CID:        "cid-native-system-notice-invalid",
		Command:    proto.CmdPublishSystemNotice,
		Payload:    invalidPayload,
		EnqueuedAt: 1234,
	})
	if invalid.Err == nil || invalid.Err.Code != proto.ErrValidationFailed || invalid.Err.Retryable {
		t.Fatalf("publish system notice to invalid board = %+v, want terminal validation", invalid)
	}

	noticeRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  defaultCommandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-system-notice",
		Command:    proto.CmdPublishSystemNotice,
		Payload:    noticePayload,
		EnqueuedAt: 1234,
	})
	if err != nil {
		t.Fatalf("produce system notice command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain system notice once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, defaultCommandPartition); err != nil || got != noticeRecord.Offset {
		t.Fatalf("system notice committed offset = %d, %v; want %d, nil", got, err, noticeRecord.Offset)
	}
	notepadPartition := LogPartition{Kind: partitionBoard, Key: "notepad"}
	notepadEvents, err := eventStore.ReplayPartition(ctx, notepadPartition.Kind, notepadPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay notepad notice events: %v", err)
	}
	if len(notepadEvents) != 3 ||
		notepadEvents[0].Kind != proto.EvtBoardCreated ||
		notepadEvents[1].Kind != proto.EvtThreadNew ||
		notepadEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("notepad notice events = %+v, want board/thread/post", notepadEvents)
	}
	threadEvent, ok := notepadEvents[1].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("notice thread payload = %T, want ThreadNewPayload", notepadEvents[1].Payload)
	}
	postEvent, ok := notepadEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("notice post payload = %T, want PostAppendedPayload", notepadEvents[2].Payload)
	}
	if threadEvent.Board != "notepad" || threadEvent.Title != "Campus notice" ||
		postEvent.Thread != threadEvent.ID || postEvent.AuthorID != admin.ID {
		t.Fatalf("notice thread/post payloads = %+v / %+v, want deterministic notepad notice", threadEvent, postEvent)
	}
	for _, want := range []string{"# Campus notice", "Notice board: notepad", "Actor: admin", "Source: operator broadcast", "Maintenance tonight at 23:00.", "Generated public system notice"} {
		if !strings.Contains(postEvent.Body, want) {
			t.Fatalf("notice body missing %q:\n%s", want, postEvent.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-system-notice-test",
		Partition: notepadPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize notepad notice events: %v", err)
	}
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

	secondPayload, err := json.Marshal(proto.PublishSystemNoticePayload{
		Board: "notepad",
		Title: "Second notice",
		Body:  "Another maintenance window.",
	})
	if err != nil {
		t.Fatalf("marshal second notice payload: %v", err)
	}
	secondCommandPartition := LogPartition{Kind: partitionBoard, Key: "notepad"}
	secondRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  secondCommandPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-system-notice-second",
		Command:    proto.CmdPublishSystemNotice,
		Payload:    secondPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce second system notice command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain second system notice once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, secondCommandPartition); err != nil || got != secondRecord.Offset {
		t.Fatalf("second notice committed offset = %d, %v; want %d, nil", got, err, secondRecord.Offset)
	}
	notepadEvents, err = eventStore.ReplayPartition(ctx, notepadPartition.Kind, notepadPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay notepad events after second notice: %v", err)
	}
	if len(notepadEvents) != 5 ||
		notepadEvents[3].Kind != proto.EvtThreadNew ||
		notepadEvents[4].Kind != proto.EvtPostAppended {
		t.Fatalf("notepad events after second notice = %+v, want original board/thread/post plus second thread/post", notepadEvents)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-system-notice-test",
		Partition: notepadPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize second notepad notice events: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	moderationPartition := LogPartition{Kind: partitionBoard, Key: nativeModerationSystemBoardID}
	globalPartition := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Native moderation review",
		Body:  "public body should stay out of logs",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-moderation-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-moderation-review-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	boardEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(boardEvents) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", boardEvents)
	}
	rootPostPayload, ok := boardEvents[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", boardEvents[1].Payload)
	}

	flagPayload, err := json.Marshal(proto.FlagPostPayload{
		Post:   rootPostPayload.ID,
		Reason: "sensitive report reason",
	})
	if err != nil {
		t.Fatalf("marshal flag payload: %v", err)
	}
	postPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}
	flagRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-flag-post",
		Command:    proto.CmdFlagPost,
		Payload:    flagPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce flag command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain flag once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, postPartition); err != nil || got != flagRecord.Offset {
		t.Fatalf("flag committed offset = %d, %v; want %d, nil", got, err, flagRecord.Offset)
	}
	boardEvents, err = eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay flag events: %v", err)
	}
	if len(boardEvents) != 3 || boardEvents[2].Kind != proto.EvtPostFlagged {
		t.Fatalf("flag events = %+v, want post.flagged appended to board partition", boardEvents)
	}
	flagEvent, ok := boardEvents[2].Payload.(*proto.PostFlaggedPayload)
	if !ok {
		t.Fatalf("flag payload = %T, want PostFlaggedPayload", boardEvents[2].Payload)
	}
	if flagEvent.Kind != "post_flag" || flagEvent.PostID != rootPostPayload.ID || flagEvent.Thread != rootPostPayload.Thread ||
		flagEvent.Reporter != bob.ID || flagEvent.Reason != "sensitive report reason" || flagEvent.TS != 2234 {
		t.Fatalf("flag event = %+v, want deterministic post flag review", flagEvent)
	}
	moderationEvents, err := eventStore.ReplayPartition(ctx, moderationPartition.Kind, moderationPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay generated moderation events: %v", err)
	}
	if len(moderationEvents) != 3 ||
		moderationEvents[0].Kind != proto.EvtBoardCreated ||
		moderationEvents[1].Kind != proto.EvtThreadNew ||
		moderationEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("generated moderation events = %+v, want board/thread/post log events", moderationEvents)
	}
	flagLogPost, ok := moderationEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("flag log payload = %T, want PostAppendedPayload", moderationEvents[2].Payload)
	}
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
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-moderation-review-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize flag event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-moderation-review-test",
		Partition: moderationPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize generated flag log: %v", err)
	}
	reviews, err := c.ListModerationReviews("open", 10, 0)
	if err != nil {
		t.Fatalf("list open reviews: %v", err)
	}
	if len(reviews) != 1 || reviews[0].ID != flagEvent.ReviewID || reviews[0].Kind != "post_flag" ||
		reviews[0].TargetID != rootPostPayload.ID || reviews[0].Reporter != bob.ID ||
		reviews[0].Reason != "sensitive report reason" {
		t.Fatalf("open reviews after native flag = %+v, want projected post flag review", reviews)
	}
	moderationThreads, err := c.ListThreads(nativeModerationSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list moderation threads after flag: %v", err)
	}
	if len(moderationThreads) != 1 || moderationThreads[0].ID != "mod_flag_thr_"+flagEvent.ReviewID {
		t.Fatalf("moderation threads after flag = %+v, want generated flag thread", moderationThreads)
	}

	resolvePayload, err := json.Marshal(proto.ResolveReviewPayload{
		Review:     flagEvent.ReviewID,
		Resolution: "private moderator note",
	})
	if err != nil {
		t.Fatalf("marshal resolve payload: %v", err)
	}
	resolveDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionReview, Key: flagEvent.ReviewID},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-resolve-review-denied",
		Command:    proto.CmdResolveReview,
		Payload:    resolvePayload,
		EnqueuedAt: 3234,
	})
	if resolveDenied.Err == nil || resolveDenied.Err.Code != proto.ErrForbidden || resolveDenied.Err.Retryable {
		t.Fatalf("resolve reply without permission = %+v, want terminal forbidden", resolveDenied)
	}
	reviewPartition := LogPartition{Kind: partitionReview, Key: flagEvent.ReviewID}
	resolveRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  reviewPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-resolve-review",
		Command:    proto.CmdResolveReview,
		Payload:    resolvePayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce resolve command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain resolve once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, reviewPartition); err != nil || got != resolveRecord.Offset {
		t.Fatalf("resolve committed offset = %d, %v; want %d, nil", got, err, resolveRecord.Offset)
	}
	globalEvents, err := eventStore.ReplayPartition(ctx, globalPartition.Kind, globalPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay resolve events: %v", err)
	}
	if len(globalEvents) != 1 || globalEvents[0].Kind != proto.EvtReviewResolved {
		t.Fatalf("global resolve events = %+v, want one review.resolved", globalEvents)
	}
	resolveEvent, ok := globalEvents[0].Payload.(*proto.ReviewResolvedPayload)
	if !ok {
		t.Fatalf("resolve payload = %T, want ReviewResolvedPayload", globalEvents[0].Payload)
	}
	if resolveEvent.ReviewID != flagEvent.ReviewID || resolveEvent.Resolution != "private moderator note" || resolveEvent.By != admin.ID || resolveEvent.TS != 3234 {
		t.Fatalf("resolve event = %+v, want deterministic review resolution", resolveEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-moderation-review-test",
		Partition: globalPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize resolve event: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-moderation-review-test",
		Partition: moderationPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize generated resolve log: %v", err)
	}
	resolvedReviews, err := c.ListModerationReviews("resolved", 10, 0)
	if err != nil {
		t.Fatalf("list resolved reviews: %v", err)
	}
	if len(resolvedReviews) != 1 || resolvedReviews[0].ID != flagEvent.ReviewID ||
		resolvedReviews[0].Actor != admin.ID || resolvedReviews[0].Resolution != "private moderator note" {
		t.Fatalf("resolved reviews = %+v, want projected native resolution", resolvedReviews)
	}
	moderationThreads, err = c.ListThreads(nativeModerationSystemBoardID, 10, 0)
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}
	if err := setNativeDecisionTestTrustLevel(c, alice.ID, 2); err != nil {
		t.Fatalf("set alice trust level: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Native vote result poll",
		Body:  "[poll]\nBest option?\nOption A\nOption B\n[/poll]",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-poll-result-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce poll create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain poll create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-poll-result-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize poll create events: %v", err)
	}
	boardEvents, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay poll create events: %v", err)
	}
	if len(boardEvents) != 2 || boardEvents[1].Kind != proto.EvtPostAppended {
		t.Fatalf("poll create events = %+v, want thread.new and post.appended", boardEvents)
	}
	rootPostPayload, ok := boardEvents[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root poll post payload = %T, want PostAppendedPayload", boardEvents[1].Payload)
	}
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

	publishPayload, err := json.Marshal(proto.PublishPollResultPayload{Poll: rootPoll.ID})
	if err != nil {
		t.Fatalf("marshal publish payload: %v", err)
	}
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
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("publish reply without permission = %+v, want terminal forbidden", denied)
	}
	canManagePolls := true
	if err := setBoardMember(c.DB, "general", carol.ID, true, BoardMemberPatch{CanManagePolls: &canManagePolls}); err != nil {
		t.Fatalf("set carol poll manager: %v", err)
	}
	publishRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  pollPartition,
		ActorID:    carol.ID,
		CID:        "cid-native-poll-result-publish",
		Command:    proto.CmdPublishPollResult,
		Payload:    publishPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce publish poll result command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain publish poll result once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, pollPartition); err != nil || got != publishRecord.Offset {
		t.Fatalf("publish committed offset = %d, %v; want %d, nil", got, err, publishRecord.Offset)
	}
	votePartition := LogPartition{Kind: partitionBoard, Key: nativeVoteSystemBoardID}
	voteEvents, err := eventStore.ReplayPartition(ctx, votePartition.Kind, votePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay generated vote events: %v", err)
	}
	if len(voteEvents) != 3 ||
		voteEvents[0].Kind != proto.EvtBoardCreated ||
		voteEvents[1].Kind != proto.EvtThreadNew ||
		voteEvents[2].Kind != proto.EvtPostAppended {
		t.Fatalf("generated vote events = %+v, want board/thread/post result events", voteEvents)
	}
	resultThread, ok := voteEvents[1].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("vote thread payload = %T, want ThreadNewPayload", voteEvents[1].Payload)
	}
	resultPost, ok := voteEvents[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("vote post payload = %T, want PostAppendedPayload", voteEvents[2].Payload)
	}
	if resultThread.ID != "vote_poll_"+rootPoll.ID || resultThread.Board != nativeVoteSystemBoardID ||
		!strings.Contains(resultThread.Title, "Best option?") || resultPost.Thread != resultThread.ID ||
		resultPost.ID != "vote_poll_post_"+rootPoll.ID {
		t.Fatalf("result thread/post payloads = %+v / %+v, want deterministic vote result records", resultThread, resultPost)
	}
	for _, want := range []string{"# Poll result: Best option?", "Source thread: Native vote result poll", "Source board: general", "Total votes: 1", "Option A: 1 vote", "Option B: 0 vote", "Generated public poll result"} {
		if !strings.Contains(resultPost.Body, want) {
			t.Fatalf("generated vote result body missing %q:\n%s", want, resultPost.Body)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-poll-result-test",
		Partition: votePartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize generated vote events: %v", err)
	}
	voteBoard, err := c.GetBoard(nativeVoteSystemBoardID)
	if err != nil {
		t.Fatalf("get vote board: %v", err)
	}
	if voteBoard == nil || voteBoard.Name != nativeVoteSystemBoardID {
		t.Fatalf("vote board = %+v, want generated vote board", voteBoard)
	}
	resultPosts, err := c.ListPosts(resultThread.ID, 10, 0)
	if err != nil {
		t.Fatalf("list vote result posts: %v", err)
	}
	if len(resultPosts) != 1 || resultPosts[0].Body != resultPost.Body {
		t.Fatalf("result posts = %+v, want materialized generated result post", resultPosts)
	}

	duplicateRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  pollPartition,
		ActorID:    carol.ID,
		CID:        "cid-native-poll-result-duplicate",
		Command:    proto.CmdPublishPollResult,
		Payload:    publishPayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce duplicate publish poll result command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain duplicate publish poll result once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, pollPartition); err != nil || got != duplicateRecord.Offset {
		t.Fatalf("duplicate publish committed offset = %d, %v; want %d, nil", got, err, duplicateRecord.Offset)
	}
	voteEvents, err = eventStore.ReplayPartition(ctx, votePartition.Kind, votePartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay generated vote events after duplicate: %v", err)
	}
	if len(voteEvents) != 3 {
		t.Fatalf("generated vote events after duplicate = %+v, want no duplicate events", voteEvents)
	}
	voteThreads, err := c.ListThreads(nativeVoteSystemBoardID, 10, 0)
	if err != nil {
		t.Fatalf("list vote threads after duplicate: %v", err)
	}
	if len(voteThreads) != 1 || voteThreads[0].ID != resultThread.ID {
		t.Fatalf("vote threads after duplicate = %+v, want single result thread", voteThreads)
	}
}

func TestNativeCommandLogDecisionExecutorProjectsRedactAndRestorePost(t *testing.T) {
	ctx := context.Background()
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native redaction",
		Body:  "redactsearchtoken visible body",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-redact-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-redact-restore-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	rootPostPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
	postPartition := LogPartition{Kind: partitionPost, Key: rootPostPayload.ID}
	if search, err := c.SearchReadablePosts(alice, "redactsearchtoken", "", 10); err != nil || len(search) != 1 {
		t.Fatalf("search before redact = %+v, %v; want visible post", search, err)
	}

	redactPayload, err := json.Marshal(proto.RedactPostPayload{
		Post:   rootPostPayload.ID,
		Reason: "author cleanup",
	})
	if err != nil {
		t.Fatalf("marshal redact payload: %v", err)
	}
	forbidden := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-redact-forbidden",
		Command:    proto.CmdRedactPost,
		Payload:    redactPayload,
		EnqueuedAt: 2234,
	})
	if forbidden.Err == nil || forbidden.Err.Code != proto.ErrForbidden || forbidden.Err.Retryable {
		t.Fatalf("forbidden redact reply = %+v, want terminal forbidden", forbidden)
	}
	redactRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-redact-author",
		Command:    proto.CmdRedactPost,
		Payload:    redactPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce redact command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain redact once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, postPartition); err != nil || got != redactRecord.Offset {
		t.Fatalf("redact committed offset = %d, %v; want %d, nil", got, err, redactRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay redact events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events after redact = %+v, want post.redacted", events)
	}
	redactEvent, ok := events[2].Payload.(*proto.PostRedactedPayload)
	if !ok {
		t.Fatalf("redact event payload = %T, want PostRedactedPayload", events[2].Payload)
	}
	if redactEvent.ID != rootPostPayload.ID || redactEvent.Thread != rootPostPayload.Thread ||
		redactEvent.By != alice.ID || redactEvent.Reason != "author cleanup" || redactEvent.TS != 2234 {
		t.Fatalf("redact event = %+v, want deterministic author redaction", redactEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-redact-restore-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize redact event: %v", err)
	}
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

	restorePayload, err := json.Marshal(proto.RestorePostPayload{Post: rootPostPayload.ID})
	if err != nil {
		t.Fatalf("marshal restore payload: %v", err)
	}
	restoreDenied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     redactRecord.Offset + 1,
		ActorID:    bob.ID,
		CID:        "cid-native-restore-forbidden",
		Command:    proto.CmdRestorePost,
		Payload:    restorePayload,
		EnqueuedAt: 3234,
	})
	if restoreDenied.Err == nil || restoreDenied.Err.Code != proto.ErrForbidden || restoreDenied.Err.Retryable {
		t.Fatalf("restore denied reply = %+v, want terminal forbidden", restoreDenied)
	}
	canModeratePosts := true
	if err := setBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{CanModeratePosts: &canModeratePosts}); err != nil {
		t.Fatalf("grant bob post moderation: %v", err)
	}
	restoreRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-restore",
		Command:    proto.CmdRestorePost,
		Payload:    restorePayload,
		EnqueuedAt: 3234,
	})
	if err != nil {
		t.Fatalf("produce restore command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain restore once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, postPartition); err != nil || got != restoreRecord.Offset {
		t.Fatalf("restore committed offset = %d, %v; want %d, nil", got, err, restoreRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay restore events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events after restore = %+v, want post.restored", events)
	}
	restoreEvent, ok := events[3].Payload.(*proto.PostRestoredPayload)
	if !ok {
		t.Fatalf("restore event payload = %T, want PostRestoredPayload", events[3].Payload)
	}
	if restoreEvent.ID != rootPostPayload.ID || restoreEvent.Thread != rootPostPayload.Thread || restoreEvent.By != bob.ID || restoreEvent.TS != 3234 {
		t.Fatalf("restore event = %+v, want deterministic restore", restoreEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-redact-restore-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize restore event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	go c.Run(ctx)
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Range moderation",
		Body:  "first range post",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	createReply := c.ExecCmd(ctx, bob, proto.CmdCreateThread, createPayload, "cid-sql-range-create")
	if createReply.Err != nil || createReply.Result == nil {
		t.Fatalf("create range thread reply = %+v", createReply)
	}
	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread: createReply.Result.ID,
		Body:   "second range post",
	})
	if err != nil {
		t.Fatalf("marshal append payload: %v", err)
	}
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
	if err := setBoardMember(c.DB, "general", bob.ID, true, BoardMemberPatch{CanModeratePosts: &canModeratePosts}); err != nil {
		t.Fatalf("grant bob post moderation: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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
	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	rangePayload, err := json.Marshal(proto.RedactPostRangePayload{
		Board:  "general",
		Posts:  []string{firstPostID, secondPostID, firstPostID},
		Reason: "range cleanup",
	})
	if err != nil {
		t.Fatalf("marshal redact range payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-range-redact-denied",
		Command:    proto.CmdRedactPostRange,
		Payload:    rangePayload,
		EnqueuedAt: 3333,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("range redact denied reply = %+v, want terminal forbidden", denied)
	}
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
	redactRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-range-redact",
		Command:    proto.CmdRedactPostRange,
		Payload:    rangePayload,
		EnqueuedAt: 3333,
	})
	if err != nil {
		t.Fatalf("produce range redact command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain range redact once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, boardPartition); err != nil || got != redactRecord.Offset {
		t.Fatalf("range redact committed offset = %d, %v; want %d, nil", got, err, redactRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay range redact events: %v", err)
	}
	if len(events) != 2 || events[0].Kind != proto.EvtPostRedacted || events[1].Kind != proto.EvtPostRedacted {
		t.Fatalf("range redact events = %+v, want two post.redacted events", events)
	}
	for i, event := range events {
		payload, ok := event.Payload.(*proto.PostRedactedPayload)
		if !ok {
			t.Fatalf("range redact event %d payload = %T, want PostRedactedPayload", i, event.Payload)
		}
		if payload.By != bob.ID || payload.Reason != "range cleanup" || payload.DeletionKind != "recycle" || payload.TS != 3333 {
			t.Fatalf("range redact payload %d = %+v, want moderator recycle redaction", i, payload)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-range-moderation-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize range redact events: %v", err)
	}
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

	restorePayload, err := json.Marshal(proto.RestorePostRangePayload{
		Board: "general",
		Posts: []string{firstPostID},
	})
	if err != nil {
		t.Fatalf("marshal restore range payload: %v", err)
	}
	restoreRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    bob.ID,
		CID:        "cid-native-range-restore",
		Command:    proto.CmdRestorePostRange,
		Payload:    restorePayload,
		EnqueuedAt: 4444,
	})
	if err != nil {
		t.Fatalf("produce range restore command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain range restore once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, boardPartition); err != nil || got != restoreRecord.Offset {
		t.Fatalf("range restore committed offset = %d, %v; want %d, nil", got, err, restoreRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay range restore events: %v", err)
	}
	if len(events) != 3 || events[2].Kind != proto.EvtPostRestored {
		t.Fatalf("range events after restore = %+v, want post.restored", events)
	}
	restoreEvent, ok := events[2].Payload.(*proto.PostRestoredPayload)
	if !ok {
		t.Fatalf("range restore payload = %T, want PostRestoredPayload", events[2].Payload)
	}
	if restoreEvent.ID != firstPostID || restoreEvent.By != bob.ID || restoreEvent.TS != 4444 {
		t.Fatalf("range restore event = %+v, want first post restored by bob", restoreEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-range-moderation-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize range restore event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	go c.Run(ctx)
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}
	canModeratePosts := true
	if err := setBoardMember(c.DB, "general", alice.ID, true, BoardMemberPatch{CanModeratePosts: &canModeratePosts}); err != nil {
		t.Fatalf("grant alice post moderation: %v", err)
	}

	createJunkPost := func(cid, title string) string {
		t.Helper()
		createPayload, err := json.Marshal(proto.CreateThreadPayload{
			Board: "general",
			Title: title,
			Body:  title + " body",
		})
		if err != nil {
			t.Fatalf("marshal create junk post payload: %v", err)
		}
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
		redactPayload, err := json.Marshal(proto.RedactPostPayload{
			Post:   posts[0].ID,
			Reason: "author cleanup",
		})
		if err != nil {
			t.Fatalf("marshal redact junk post payload: %v", err)
		}
		redactReply := c.ExecCmd(ctx, bob, proto.CmdRedactPost, redactPayload, cid+"-redact")
		if redactReply.Err != nil {
			t.Fatalf("redact junk post reply = %+v", redactReply)
		}
		return posts[0].ID
	}
	firstPostID := createJunkPost("cid-sql-junk-one", "Junk one")
	secondPostID := createJunkPost("cid-sql-junk-two", "Junk two")

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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
	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	clearPayload, err := json.Marshal(proto.ClearBoardJunkPayload{
		Board: "general",
		Posts: []string{firstPostID, firstPostID},
	})
	if err != nil {
		t.Fatalf("marshal selected junk clear payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  boardPartition,
		Offset:     1,
		ActorID:    carol.ID,
		CID:        "cid-native-clear-junk-denied",
		Command:    proto.CmdClearBoardJunk,
		Payload:    clearPayload,
		EnqueuedAt: 5555,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("clear junk denied reply = %+v, want terminal forbidden", denied)
	}
	clearRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-clear-junk-one",
		Command:    proto.CmdClearBoardJunk,
		Payload:    clearPayload,
		EnqueuedAt: 5555,
	})
	if err != nil {
		t.Fatalf("produce selected junk clear command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain selected junk clear once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, boardPartition); err != nil || got != clearRecord.Offset {
		t.Fatalf("selected junk clear committed offset = %d, %v; want %d, nil", got, err, clearRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay selected junk clear event: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtPostDeletionCleared {
		t.Fatalf("selected junk clear events = %+v, want post.deletion_cleared", events)
	}
	clearEvent, ok := events[0].Payload.(*proto.PostDeletionClearedPayload)
	if !ok {
		t.Fatalf("selected junk clear payload = %T, want PostDeletionClearedPayload", events[0].Payload)
	}
	if clearEvent.ID != firstPostID || clearEvent.Board != "general" || clearEvent.Kind != "junk" || clearEvent.By != alice.ID || clearEvent.TS != 5555 {
		t.Fatalf("selected junk clear event = %+v, want first junk post cleared by alice", clearEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-clear-junk-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize selected junk clear event: %v", err)
	}
	junk, err := c.ListBoardDeletedPosts("general", "junk", 10, 0)
	if err != nil {
		t.Fatalf("list junk after selected clear: %v", err)
	}
	if len(junk) != 1 || junk[0].PostID != secondPostID {
		t.Fatalf("junk after selected clear = %+v, want only second post", junk)
	}

	clearAllPayload, err := json.Marshal(proto.ClearBoardJunkPayload{Board: "general"})
	if err != nil {
		t.Fatalf("marshal all junk clear payload: %v", err)
	}
	clearAllRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-clear-junk-all",
		Command:    proto.CmdClearBoardJunk,
		Payload:    clearAllPayload,
		EnqueuedAt: 6666,
	})
	if err != nil {
		t.Fatalf("produce all junk clear command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain all junk clear once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, boardPartition); err != nil || got != clearAllRecord.Offset {
		t.Fatalf("all junk clear committed offset = %d, %v; want %d, nil", got, err, clearAllRecord.Offset)
	}
	events, err = eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay all junk clear event: %v", err)
	}
	if len(events) != 2 || events[1].Kind != proto.EvtPostDeletionCleared {
		t.Fatalf("events after all junk clear = %+v, want second post.deletion_cleared", events)
	}
	clearAllEvent, ok := events[1].Payload.(*proto.PostDeletionClearedPayload)
	if !ok {
		t.Fatalf("all junk clear payload = %T, want PostDeletionClearedPayload", events[1].Payload)
	}
	if clearAllEvent.ID != secondPostID || clearAllEvent.By != alice.ID || clearAllEvent.TS != 6666 {
		t.Fatalf("all junk clear event = %+v, want second junk post cleared by alice", clearAllEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-clear-junk-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize all junk clear event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	go c.Run(ctx)
	admin, err := c.RegisterUser("admin", "pw")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Native purge target",
		Body:  "purgeprivacytoken body that must leave local fts",
	})
	if err != nil {
		t.Fatalf("marshal purge target payload: %v", err)
	}
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

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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
	postPartition := LogPartition{Kind: partitionPost, Key: postID}
	purgePayload, err := json.Marshal(proto.PurgePostPayload{
		Post:   postID,
		Reason: "privacy request",
	})
	if err != nil {
		t.Fatalf("marshal purge payload: %v", err)
	}
	denied := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  postPartition,
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-purge-denied",
		Command:    proto.CmdPurgePost,
		Payload:    purgePayload,
		EnqueuedAt: 7777,
	})
	if denied.Err == nil || denied.Err.Code != proto.ErrForbidden || denied.Err.Retryable {
		t.Fatalf("purge denied reply = %+v, want terminal forbidden", denied)
	}
	purgeRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  postPartition,
		ActorID:    admin.ID,
		CID:        "cid-native-purge-post",
		Command:    proto.CmdPurgePost,
		Payload:    purgePayload,
		EnqueuedAt: 7777,
	})
	if err != nil {
		t.Fatalf("produce purge command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain purge once: %v", err)
	}
	if got, err := commandLog.CommittedOffset(ctx, postPartition); err != nil || got != purgeRecord.Offset {
		t.Fatalf("purge committed offset = %d, %v; want %d, nil", got, err, purgeRecord.Offset)
	}
	events, err := eventStore.ReplayPartition(ctx, partitionBoard, "general", 0, 10)
	if err != nil {
		t.Fatalf("replay purge event: %v", err)
	}
	if len(events) != 1 || events[0].Kind != proto.EvtPostPurged {
		t.Fatalf("purge events = %+v, want post.purged", events)
	}
	purgeEvent, ok := events[0].Payload.(*proto.PostPurgedPayload)
	if !ok {
		t.Fatalf("purge event payload = %T, want PostPurgedPayload", events[0].Payload)
	}
	if purgeEvent.ID != postID || purgeEvent.Thread != createReply.Result.ID || purgeEvent.By != admin.ID || purgeEvent.Reason != "privacy request" || purgeEvent.TS != 7777 {
		t.Fatalf("purge event = %+v, want deterministic admin purge", purgeEvent)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-purge-test",
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize purge event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	if err := setNativeDecisionTestTrustLevel(c, alice.ID, 2); err != nil {
		t.Fatalf("seed alice trust level: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	blockedPollPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Blocked native poll",
		Body:  "[poll]\nQuestion?\nOption A\nOption B\n[/poll]",
	})
	if err != nil {
		t.Fatalf("marshal blocked poll payload: %v", err)
	}
	blocked := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-poll-low-trust",
		Command:    proto.CmdCreateThread,
		Payload:    blockedPollPayload,
		EnqueuedAt: 1111,
	})
	if blocked.Err == nil || blocked.Err.Retryable || blocked.Err.Code != proto.ErrForbidden {
		t.Fatalf("blocked low-trust poll reply = %+v, want terminal forbidden", blocked)
	}

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createBody := "intro\n[poll]\nQuestion?\nOption A\nOption B\n[/poll]\noutro"
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native poll",
		Body:  createBody,
	})
	if err != nil {
		t.Fatalf("marshal create poll payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-poll",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create poll command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create poll once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-poll-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create poll events: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create poll events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("create poll events = %+v, want thread.new and post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootPostPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
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

	editExistingPollPayload, err := json.Marshal(proto.EditPostPayload{
		Post: rootPostPayload.ID,
		Body: "edited poll body",
	})
	if err != nil {
		t.Fatalf("marshal edit existing poll payload: %v", err)
	}
	editExistingPollReply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionPost, Key: rootPostPayload.ID},
		Offset:     1,
		ActorID:    alice.ID,
		CID:        "cid-native-edit-existing-poll-post",
		Command:    proto.CmdEditPost,
		Payload:    editExistingPollPayload,
		EnqueuedAt: 1734,
	})
	if editExistingPollReply.Err == nil || editExistingPollReply.Err.Retryable || editExistingPollReply.Err.Code != proto.ErrValidationFailed {
		t.Fatalf("edit existing poll reply = %+v, want terminal validation failure", editExistingPollReply)
	}
	if !strings.Contains(editExistingPollReply.Err.Message, "contain a poll") {
		t.Fatalf("edit existing poll error = %q, want poll-bearing post validation", editExistingPollReply.Err.Message)
	}

	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "[poll]\nReply question?\nYes\nNo\n[/poll]",
	})
	if err != nil {
		t.Fatalf("marshal append poll payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-poll",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	}); err != nil {
		t.Fatalf("produce append poll command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain append poll once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-poll-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize append poll event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	carol, err := c.RegisterUser("carol", "pw")
	if err != nil {
		t.Fatalf("register carol: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native quoted reply",
		Body:  "root mentions @bob\nsecond line",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-quote",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-quote-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	if processed := drainOutboxJobsForTest(t, c); processed != 1 {
		t.Fatalf("processed outbox jobs after create = %d, want root post job", processed)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootPostPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
	bobNotifications, err := c.ListNotifications(bob.ID, 10, 0, false)
	if err != nil {
		t.Fatalf("ListNotifications(bob) after create: %v", err)
	}
	if len(bobNotifications) != 1 || bobNotifications[0].Kind != "mention" || bobNotifications[0].PostID != rootPostPayload.ID {
		t.Fatalf("bob notifications after create = %+v, want one root mention", bobNotifications)
	}

	replyBody := "quoted reply without a mention"
	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread:    threadPayload.ID,
		ReplyTo:   rootPostPayload.ID,
		QuotePost: true,
		Body:      replyBody,
	})
	if err != nil {
		t.Fatalf("marshal quoted reply payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    carol.ID,
		CID:        "cid-native-append-post-quote",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	}); err != nil {
		t.Fatalf("produce quoted reply command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain quoted reply once: %v", err)
	}
	quoteEvents, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 2, 10)
	if err != nil {
		t.Fatalf("replay quoted reply event: %v", err)
	}
	if len(quoteEvents) != 1 || quoteEvents[0].Kind != proto.EvtPostAppended {
		t.Fatalf("quote events = %+v, want one post.appended", quoteEvents)
	}
	quotePostPayload, ok := quoteEvents[0].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("quote post payload = %T, want PostAppendedPayload", quoteEvents[0].Payload)
	}
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
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-quote-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize quoted reply event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
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

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	partition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Broker-native random signature",
		Body:  "root post",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	createRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  partition,
		ActorID:    alice.ID,
		CID:        "cid-native-create-thread-random-signature",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	if err != nil {
		t.Fatalf("produce create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain create once: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay create events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread event payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootPostPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
	expectedRootSignature, err := nativePostSignature(c.DB, alice.ID, createRecord)
	if err != nil {
		t.Fatalf("expected root native signature: %v", err)
	}
	if rootPostPayload.Signature != expectedRootSignature || !nativeSignatureIn(rootPostPayload.Signature, "signature one", "signature two") {
		t.Fatalf("root signature = %q, want deterministic active random signature %q", rootPostPayload.Signature, expectedRootSignature)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-random-signature-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize create events: %v", err)
	}
	posts, err := c.ListPosts(threadPayload.ID, 10, 0)
	if err != nil {
		t.Fatalf("list posts after create: %v", err)
	}
	if len(posts) != 1 || posts[0].Signature != rootPostPayload.Signature {
		t.Fatalf("posts after create = %+v, want broker-projected random signature %q", posts, rootPostPayload.Signature)
	}

	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread: threadPayload.ID,
		Body:   "reply post",
	})
	if err != nil {
		t.Fatalf("marshal append payload: %v", err)
	}
	appendRecord, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-append-post-random-signature",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	})
	if err != nil {
		t.Fatalf("produce append command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain append once: %v", err)
	}
	appendEvents, err := eventStore.ReplayPartition(ctx, partition.Kind, partition.Key, 2, 10)
	if err != nil {
		t.Fatalf("replay append event: %v", err)
	}
	if len(appendEvents) != 1 {
		t.Fatalf("append events = %+v, want one post.appended", appendEvents)
	}
	replyPostPayload, ok := appendEvents[0].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("reply post payload = %T, want PostAppendedPayload", appendEvents[0].Payload)
	}
	expectedReplySignature, err := nativePostSignature(c.DB, alice.ID, appendRecord)
	if err != nil {
		t.Fatalf("expected reply native signature: %v", err)
	}
	if replyPostPayload.Signature != expectedReplySignature || !nativeSignatureIn(replyPostPayload.Signature, "signature one", "signature two") {
		t.Fatalf("reply signature = %q, want deterministic active random signature %q", replyPostPayload.Signature, expectedReplySignature)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-random-signature-test",
		Partition: partition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize append event: %v", err)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	alice, err := c.RegisterUser("alice", "pw")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	if err := c.UpdateUserProfile(alice.ID, "Alice", "", "bio", "", "private-ish signature", "", ""); err != nil {
		t.Fatalf("update alice profile signature: %v", err)
	}
	anonymousAllowed := true
	if err := setBoardSettings(c.DB, "general", BoardSettingsPatch{AnonymousAllowed: &anonymousAllowed}); err != nil {
		t.Fatalf("enable anonymous posting: %v", err)
	}

	commandClient := NewMemoryBrokerCommandLogClient()
	eventClient := NewMemoryBrokerEventLogClient()
	commandLog := NewBrokerCommandLog(commandClient)
	eventStore := NewBrokerEventStore(eventClient)
	transactionStore := NewBrokerCommandEventTransactionStore(
		NewMemoryBrokerCommandEventTransactionClient(commandClient, eventClient),
	)
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

	boardPartition := LogPartition{Kind: partitionBoard, Key: "general"}
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board:     "general",
		Title:     "Broker-native anonymous post",
		Body:      "anonymous root post",
		Anonymous: true,
	})
	if err != nil {
		t.Fatalf("marshal anonymous create payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  boardPartition,
		ActorID:    alice.ID,
		CID:        "cid-native-anonymous-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	}); err != nil {
		t.Fatalf("produce anonymous create command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain anonymous create once: %v", err)
	}
	events, err := eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay anonymous create events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("anonymous create events = %+v, want thread.new and post.appended", events)
	}
	threadPayload, ok := events[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("thread payload = %T, want ThreadNewPayload", events[0].Payload)
	}
	rootPostPayload, ok := events[1].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("root post payload = %T, want PostAppendedPayload", events[1].Payload)
	}
	if threadPayload.Author != "Anonymous" || threadPayload.AuthorID != "" ||
		rootPostPayload.Author != "Anonymous" || rootPostPayload.AuthorID != "" ||
		rootPostPayload.Signature != "" || rootPostPayload.PostCommitActorID != alice.ID ||
		rootPostPayload.PostCommitActorName != "Anonymous" {
		t.Fatalf("anonymous create payloads = thread %+v post %+v, want public anonymous identity with hidden commit actor", threadPayload, rootPostPayload)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-anonymous-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize anonymous create events: %v", err)
	}
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
	if err := setThreadPref(c.DB, bob.ID, threadPayload.ID, watchLevel); err != nil {
		t.Fatalf("watch anonymous thread: %v", err)
	}
	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread:    threadPayload.ID,
		Anonymous: true,
		Body:      "anonymous reply body",
	})
	if err != nil {
		t.Fatalf("marshal anonymous reply payload: %v", err)
	}
	if _, err := commandLog.Produce(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		ActorID:    alice.ID,
		CID:        "cid-native-anonymous-reply",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 2234,
	}); err != nil {
		t.Fatalf("produce anonymous reply command: %v", err)
	}
	if _, err := worker.DrainOnce(ctx); err != nil {
		t.Fatalf("drain anonymous reply once: %v", err)
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-anonymous-test",
		Partition: boardPartition,
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize anonymous reply event: %v", err)
	}
	events, err = eventStore.ReplayPartition(ctx, boardPartition.Kind, boardPartition.Key, 0, 10)
	if err != nil {
		t.Fatalf("replay anonymous reply events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("anonymous events after reply = %+v, want three board events", events)
	}
	replyPostPayload, ok := events[2].Payload.(*proto.PostAppendedPayload)
	if !ok {
		t.Fatalf("reply post payload = %T, want PostAppendedPayload", events[2].Payload)
	}
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
	c, err := New(t.TempDir() + "/budgie.db")
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()
	if _, err := c.RegisterUser("alice", "pw"); err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := c.RegisterUser("bob", "pw")
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}
	executor := NewCommandLogNativeDecisionExecutor(c)
	createPayload, err := json.Marshal(proto.CreateThreadPayload{
		Board:     "general",
		Title:     "Anonymous disabled",
		Body:      "anonymous should need board policy",
		Anonymous: true,
	})
	if err != nil {
		t.Fatalf("marshal anonymous create payload: %v", err)
	}
	reply := executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionBoard, Key: "general"},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-anonymous-disabled-create",
		Command:    proto.CmdCreateThread,
		Payload:    createPayload,
		EnqueuedAt: 1234,
	})
	if reply.Err == nil || reply.Err.Retryable || reply.Err.Code != proto.ErrForbidden {
		t.Fatalf("anonymous create reply = %+v, want terminal forbidden when board policy disables anonymity", reply)
	}

	basePayload, err := json.Marshal(proto.CreateThreadPayload{
		Board: "general",
		Title: "Native anonymous disabled base",
		Body:  "root post",
	})
	if err != nil {
		t.Fatalf("marshal base payload: %v", err)
	}
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
	threadPayload, ok := baseEvents[0].Payload.(*proto.ThreadNewPayload)
	if !ok {
		t.Fatalf("base thread payload = %T, want ThreadNewPayload", baseEvents[0].Payload)
	}
	eventStore := NewBrokerEventStore(NewMemoryBrokerEventLogClient())
	for _, event := range baseEvents {
		if _, err := eventStore.Append(ctx, event); err != nil {
			t.Fatalf("append base native event %s: %v", event.ID, err)
		}
	}
	if _, err := c.MaterializeEventStorePartition(ctx, eventStore, EventStorePartitionMaterializationConfig{
		Source:    "native-anonymous-disabled-test",
		Partition: LogPartition{Kind: partitionBoard, Key: "general"},
		Limit:     10,
	}); err != nil {
		t.Fatalf("materialize base native events: %v", err)
	}
	appendPayload, err := json.Marshal(proto.AppendPostPayload{
		Thread:    threadPayload.ID,
		Anonymous: true,
		Body:      "anonymous reply should need board policy",
	})
	if err != nil {
		t.Fatalf("marshal anonymous reply payload: %v", err)
	}
	reply = executor.ExecuteCommandLogRecord(ctx, CommandLogRecord{
		Partition:  LogPartition{Kind: partitionThread, Key: threadPayload.ID},
		Offset:     1,
		ActorID:    bob.ID,
		CID:        "cid-native-anonymous-disabled-reply",
		Command:    proto.CmdAppendPost,
		Payload:    appendPayload,
		EnqueuedAt: 3234,
	})
	if reply.Err == nil || reply.Err.Retryable || reply.Err.Code != proto.ErrForbidden {
		t.Fatalf("anonymous reply = %+v, want terminal forbidden when board policy disables anonymity", reply)
	}
}
