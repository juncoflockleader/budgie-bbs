package core_test

import "testing"

func TestAuthorizedScopes(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	admin := registerAndGetUser(t, c, "boss", "password123")  // first user → admin
	alice := registerAndGetUser(t, c, "alice", "password123") // regular user

	requested := []string{
		"chat:lobby",                 // public pass-through
		"presence:global",            // public pass-through
		"moderation:global",          // staff only
		"account:" + alice.ID,        // owner only
		"account:" + admin.ID,        // owner only
		"board:does-not-exist-12345", // unreadable/missing board
	}

	has := func(scopes []string, want string) bool {
		for _, s := range scopes {
			if s == want {
				return true
			}
		}
		return false
	}

	// Regular user: keeps public + own account; drops moderation, others'
	// account, and an unreadable board.
	got := c.AuthorizedScopes(alice, requested)
	for _, want := range []string{"chat:lobby", "presence:global", "account:" + alice.ID} {
		if !has(got, want) {
			t.Errorf("alice should keep %q; got %v", want, got)
		}
	}
	for _, deny := range []string{"moderation:global", "account:" + admin.ID, "board:does-not-exist-12345"} {
		if has(got, deny) {
			t.Errorf("alice must not keep %q; got %v", deny, got)
		}
	}

	// Admin keeps moderation, but still not another user's account scope.
	gotAdmin := c.AuthorizedScopes(admin, requested)
	if !has(gotAdmin, "moderation:global") {
		t.Errorf("admin should keep moderation:global; got %v", gotAdmin)
	}
	if has(gotAdmin, "account:"+alice.ID) {
		t.Errorf("admin must not subscribe to another user's account scope; got %v", gotAdmin)
	}

	// The result is never nil (so replay/subscribe never degrade to "all").
	if c.AuthorizedScopes(alice, []string{"board:nope"}) == nil {
		t.Fatal("AuthorizedScopes must return a non-nil slice")
	}
}
