package statsplan

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func TestSnapshotPlanPayload(t *testing.T) {
	plan := &SnapshotPlan{Snapshot: projections.CommunityStatHistory{
		Day:                 "2026-06-04",
		SnapshotAt:          1234,
		TotalUsers:          1,
		TotalBoards:         2,
		TotalThreads:        3,
		TotalPosts:          4,
		TotalReactions:      5,
		TotalMail:           6,
		TotalDirectMessages: 7,
		TotalLogins:         8,
		TotalLogouts:        9,
		TotalWebLogins:      10,
		TotalWebLogouts:     11,
		TotalGuestLogins:    12,
		TotalGuestLogouts:   13,
		TotalOnlineSeconds:  14,
		OnlineUsers:         15,
		OnlineGuests:        16,
		MaxOnlineUsers:      17,
		MaxOnlineAt:         18,
		MaxOnlineGuests:     19,
		MaxOnlineGuestsAt:   20,
		HeadSeq:             21,
	}}
	payload := plan.SnapshotPayload()
	if payload.Day != "2026-06-04" || payload.SnapshotAt != 1234 ||
		payload.TotalUsers != 1 || payload.TotalBoards != 2 || payload.TotalThreads != 3 ||
		payload.TotalPosts != 4 || payload.TotalReactions != 5 || payload.TotalMail != 6 ||
		payload.TotalDirectMessages != 7 || payload.TotalLogins != 8 || payload.TotalLogouts != 9 ||
		payload.TotalWebLogins != 10 || payload.TotalWebLogouts != 11 ||
		payload.TotalGuestLogins != 12 || payload.TotalGuestLogouts != 13 ||
		payload.TotalOnlineSeconds != 14 || payload.OnlineUsers != 15 || payload.OnlineGuests != 16 ||
		payload.MaxOnlineUsers != 17 || payload.MaxOnlineAt != 18 ||
		payload.MaxOnlineGuests != 19 || payload.MaxOnlineGuestsAt != 20 || payload.HeadSeq != 21 {
		t.Fatalf("SnapshotPayload = %+v", payload)
	}
}
