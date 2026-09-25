package readmodel

import "github.com/juncoflockleader/budgie-bbs/internal/core/projections"

type ViewerScope struct {
	UserID         string
	IncludePrivate bool
}

func ViewerScopeForUser(user *projections.User) ViewerScope {
	if user == nil {
		return ViewerScope{}
	}
	return ViewerScope{
		UserID:         user.ID,
		IncludePrivate: user.IsMod(),
	}
}
