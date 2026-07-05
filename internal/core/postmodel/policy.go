package postmodel

type Actor struct {
	ID   string
	Name string
}

func AuthoredBy(actor *Actor, authorID, authorName string) bool {
	if actor == nil {
		return false
	}
	if authorID != "" {
		return authorID == actor.ID
	}
	return authorName == actor.Name
}

func AuthoredByID(actor *Actor, authorID string) bool {
	return actor != nil && authorID != "" && authorID == actor.ID
}

func WithinAuthorEditWindow(nowMS, createdAt, windowMS int64) bool {
	return nowMS-createdAt < windowMS
}

func AuthorEditAllowed(canBypassAuthorWindow, isAuthor, withinWindow bool) bool {
	return canBypassAuthorWindow || (isAuthor && withinWindow)
}

type Flags struct {
	Marked      bool
	Recommended bool
	NoReply     bool
	TeX         bool
	MailBack    bool
}

type FlagPatch struct {
	Marked      *bool
	Recommended *bool
	NoReply     *bool
	TeX         *bool
	MailBack    *bool
}

type FlagPlan struct {
	Flags
	CuratorChange          bool
	ThreadModerationChange bool
	AuthorMetadataChange   bool
}

type FlagPermissionFailure string

const (
	FlagPermissionOK               FlagPermissionFailure = ""
	FlagPermissionCurator          FlagPermissionFailure = "curator"
	FlagPermissionThreadModeration FlagPermissionFailure = "thread_moderation"
	FlagPermissionAuthorMetadata   FlagPermissionFailure = "author_metadata"
)

func PlanFlagUpdate(current Flags, patch FlagPatch) FlagPlan {
	plan := FlagPlan{Flags: current}
	if patch.Marked != nil {
		plan.CuratorChange = plan.CuratorChange || *patch.Marked != current.Marked
		plan.Marked = *patch.Marked
	}
	if patch.Recommended != nil {
		plan.CuratorChange = plan.CuratorChange || *patch.Recommended != current.Recommended
		plan.Recommended = *patch.Recommended
	}
	if patch.NoReply != nil {
		plan.ThreadModerationChange = *patch.NoReply != current.NoReply
		plan.NoReply = *patch.NoReply
	}
	if patch.TeX != nil {
		plan.AuthorMetadataChange = plan.AuthorMetadataChange || *patch.TeX != current.TeX
		plan.TeX = *patch.TeX
	}
	if patch.MailBack != nil {
		plan.AuthorMetadataChange = plan.AuthorMetadataChange || *patch.MailBack != current.MailBack
		plan.MailBack = *patch.MailBack
	}
	return plan
}

func (plan FlagPlan) HasChanges() bool {
	return plan.CuratorChange || plan.ThreadModerationChange || plan.AuthorMetadataChange
}

func (plan FlagPlan) PermissionFailure(actor *Actor, authorID string, canCurate, canModerateThread bool) FlagPermissionFailure {
	if plan.CuratorChange && !canCurate {
		return FlagPermissionCurator
	}
	if plan.ThreadModerationChange && !canModerateThread {
		return FlagPermissionThreadModeration
	}
	if plan.AuthorMetadataChange && !canModerateThread && !AuthoredByID(actor, authorID) {
		return FlagPermissionAuthorMetadata
	}
	return FlagPermissionOK
}

func RedactionKind(canModeratePosts, isAuthor, withinWindow bool) (string, bool) {
	if !canModeratePosts && !(isAuthor && withinWindow) {
		return "", false
	}
	if !canModeratePosts {
		return "junk", true
	}
	return "recycle", true
}
