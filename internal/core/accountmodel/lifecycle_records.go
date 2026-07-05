package accountmodel

import (
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type LifecycleRecord struct {
	BoardID                  string
	BoardName                string
	BoardDescription         string
	ThreadID                 string
	PostID                   string
	AuthorID                 string
	AuthorName               string
	Title                    string
	Body                     string
	MarkBoardReadForAllUsers bool
}

func NewcomerLifecycleRecord(user *projections.User) LifecycleRecord {
	title := "New user: " + user.Name
	body := fmt.Sprintf("# %s\n\n- User: %s\n- Role: %s\n- Status: registered\n\nThis generated newcomer record contains public account information only.\n",
		title, user.Name, user.Role)
	return LifecycleRecord{
		BoardID:                  "newcomers",
		BoardName:                "newcomers",
		BoardDescription:         "Generated new-user registration records",
		ThreadID:                 "newcomer_thr_" + user.ID,
		PostID:                   "newcomer_pst_" + user.ID,
		AuthorID:                 user.ID,
		AuthorName:               user.Name,
		Title:                    title,
		Body:                     body,
		MarkBoardReadForAllUsers: true,
	}
}

func GoodbyeLifecycleRecord(user *projections.User) LifecycleRecord {
	title := "Goodbye: " + user.Name
	body := fmt.Sprintf("# %s\n\n- User: %s\n- Status: deactivated\n\nThe account holder closed this account. Private deactivation notes are not published.\n",
		title, user.Name)
	return LifecycleRecord{
		BoardID:          "Goodbye",
		BoardName:        "Goodbye",
		BoardDescription: "Generated account deactivation notices",
		ThreadID:         "goodbye_thr_" + user.ID,
		PostID:           "goodbye_pst_" + user.ID,
		AuthorID:         user.ID,
		AuthorName:       user.Name,
		Title:            title,
		Body:             body,
	}
}
