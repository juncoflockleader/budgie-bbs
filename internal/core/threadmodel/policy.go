package threadmodel

func ReplyAllowed(locked, canModerateBoard bool) bool {
	return !locked || canModerateBoard
}

func StarterAcceptsReplies(noReply, canModerateThread bool) bool {
	return !noReply || canModerateThread
}

func ModerationAllowed(canModerateThread bool) bool {
	return canModerateThread
}

type TitlePermissionFailure string

const (
	TitlePermissionOK         TitlePermissionFailure = ""
	TitlePermissionAuthor     TitlePermissionFailure = "author"
	TitlePermissionEditWindow TitlePermissionFailure = "edit_window"
)

func TitlePermissionFailureFor(canModerateThread, isAuthor, withinWindow bool) TitlePermissionFailure {
	if canModerateThread {
		return TitlePermissionOK
	}
	if !isAuthor {
		return TitlePermissionAuthor
	}
	if !withinWindow {
		return TitlePermissionEditWindow
	}
	return TitlePermissionOK
}
