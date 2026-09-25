package notificationmodel

func PreferredPostKind(existing, candidate string) string {
	if postKindPriority(existing) >= postKindPriority(candidate) {
		return existing
	}
	return candidate
}

func CanReceiveBoardPost(userPresent, memberReadMode, canUseMemberBoard bool) bool {
	if !userPresent {
		return false
	}
	if !memberReadMode {
		return true
	}
	return canUseMemberBoard
}

func postKindPriority(kind string) int {
	switch kind {
	case "mention":
		return 3
	case "reply":
		return 2
	case "watched":
		return 1
	default:
		return 0
	}
}
