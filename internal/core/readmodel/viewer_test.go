package readmodel

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func TestViewerScopeForUser(t *testing.T) {
	tests := []struct {
		name            string
		user            *projections.User
		wantUserID      string
		wantIncludePriv bool
	}{
		{name: "anonymous"},
		{name: "regular", user: &projections.User{ID: "usr_regular", Role: "user"}, wantUserID: "usr_regular"},
		{name: "moderator", user: &projections.User{ID: "usr_mod", Role: "moderator"}, wantUserID: "usr_mod", wantIncludePriv: true},
		{name: "admin", user: &projections.User{ID: "usr_admin", Role: "admin"}, wantUserID: "usr_admin", wantIncludePriv: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ViewerScopeForUser(tt.user)
			if got.UserID != tt.wantUserID || got.IncludePrivate != tt.wantIncludePriv {
				t.Fatalf("ViewerScopeForUser() = %+v, want userID=%q includePrivate=%v", got, tt.wantUserID, tt.wantIncludePriv)
			}
		})
	}
}
