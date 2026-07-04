package statsplan

import "github.com/juncoflockleader/budgie-bbs/internal/core/projections"

const (
	SystemBoardID          = "BBSLists"
	SystemBoardName        = "BBSLists"
	SystemBoardDescription = "Generated community rankings and statistics"
)

type SnapshotPlan struct {
	MainThreadID    string
	MainExistingSeq int64
	Snapshot        projections.CommunityStatHistory
	Posts           []SystemPostPlan
}

type SystemPostPlan struct {
	ThreadID string
	PostID   string
	Title    string
	Body     string
}
