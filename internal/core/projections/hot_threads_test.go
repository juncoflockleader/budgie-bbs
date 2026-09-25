package projections

import "testing"

func createHotThreadSplitTestTable(t *testing.T, db sqlLike) {
	t.Helper()
	if _, err := QExec(db, `CREATE TABLE hot_thread_splits (
		thread_id TEXT PRIMARY KEY,
		shards INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create hot_thread_splits: %v", err)
	}
}

func TestLoadHotThreadSplitsNormalizesRows(t *testing.T) {
	db := openSQLiteTestDB(t)
	createHotThreadSplitTestTable(t, db)
	execSQL(t, db, `INSERT INTO hot_thread_splits (thread_id, shards, updated_at) VALUES
		(' thr_hot ', 4, 1000),
		('thr_off', 1, 1000),
		('', 8, 1000)`)

	splits, err := LoadHotThreadSplits(db)
	requireNoError(t, "LoadHotThreadSplits", err)
	if len(splits) != 1 || splits["thr_hot"] != 4 {
		t.Fatalf("splits = %#v, want only normalized thr_hot=4", splits)
	}
}

func TestPersistHotThreadSplitUpsertsAndDeletesRows(t *testing.T) {
	db := openSQLiteTestDB(t)
	createHotThreadSplitTestTable(t, db)

	requireNoError(t, "PersistHotThreadSplit insert", PersistHotThreadSplit(db, " thr_hot ", 4))
	splits, err := LoadHotThreadSplits(db)
	requireNoError(t, "LoadHotThreadSplits after insert", err)
	if got := splits["thr_hot"]; got != 4 {
		t.Fatalf("inserted split = %d, want 4 in %#v", got, splits)
	}

	requireNoError(t, "PersistHotThreadSplit update", PersistHotThreadSplit(db, "thr_hot", 6))
	splits, err = LoadHotThreadSplits(db)
	requireNoError(t, "LoadHotThreadSplits after update", err)
	if got := splits["thr_hot"]; got != 6 {
		t.Fatalf("updated split = %d, want 6 in %#v", got, splits)
	}

	requireNoError(t, "PersistHotThreadSplit delete", PersistHotThreadSplit(db, "thr_hot", 1))
	splits, err = LoadHotThreadSplits(db)
	requireNoError(t, "LoadHotThreadSplits after delete", err)
	if len(splits) != 0 {
		t.Fatalf("deleted split still present: %#v", splits)
	}
}

func TestPersistHotThreadSplitRejectsEmptyThreadID(t *testing.T) {
	db := openSQLiteTestDB(t)
	createHotThreadSplitTestTable(t, db)
	if err := PersistHotThreadSplit(db, " ", 4); err == nil {
		t.Fatal("PersistHotThreadSplit empty thread id succeeded")
	}
}
