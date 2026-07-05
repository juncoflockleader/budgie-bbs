package pollmodel

func CreationTrustAllowed(isModerator bool, trustLevel, minLevel int) bool {
	return isModerator || trustLevel >= minLevel
}

func ResultPublisherAllowed(canManagePolls, isPostAuthor, isThreadAuthor bool) bool {
	return canManagePolls || isPostAuthor || isThreadAuthor
}

func PublicResultAllowed(emitPublicSystemPost bool) bool {
	return emitPublicSystemPost
}
