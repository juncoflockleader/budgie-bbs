package accountmodel

import (
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func TestNewcomerLifecycleRecord(t *testing.T) {
	user := &projections.User{ID: "usr_alice", Name: "alice", Role: "user"}
	record := NewcomerLifecycleRecord(user)
	if record.BoardID != "newcomers" || record.ThreadID != "newcomer_thr_usr_alice" || record.PostID != "newcomer_pst_usr_alice" {
		t.Fatalf("newcomer record ids = %+v", record)
	}
	if record.AuthorID != user.ID || record.AuthorName != user.Name || record.Title != "New user: alice" {
		t.Fatalf("newcomer record author/title = %+v", record)
	}
	if !record.MarkBoardReadForAllUsers {
		t.Fatalf("newcomer record should mark the generated board read for all users")
	}
	if !strings.Contains(record.Body, "Status: registered") || !strings.Contains(record.Body, "Role: user") {
		t.Fatalf("newcomer body = %q, want public registration details", record.Body)
	}
}

func TestGoodbyeLifecycleRecord(t *testing.T) {
	user := &projections.User{ID: "usr_alice", Name: "alice", Role: "user"}
	record := GoodbyeLifecycleRecord(user)
	if record.BoardID != "Goodbye" || record.ThreadID != "goodbye_thr_usr_alice" || record.PostID != "goodbye_pst_usr_alice" {
		t.Fatalf("goodbye record ids = %+v", record)
	}
	if record.AuthorID != user.ID || record.AuthorName != user.Name || record.Title != "Goodbye: alice" {
		t.Fatalf("goodbye record author/title = %+v", record)
	}
	if record.MarkBoardReadForAllUsers {
		t.Fatalf("goodbye record should not mark the generated board read globally")
	}
	if !strings.Contains(record.Body, "Status: deactivated") || strings.Contains(record.Body, "private farewell note") {
		t.Fatalf("goodbye body = %q, want public deactivation notice only", record.Body)
	}
}
