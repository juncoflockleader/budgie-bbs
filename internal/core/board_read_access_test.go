package core

import "testing"

func TestActorCanReadBoard(t *testing.T) {
	public := &BoardInfo{}
	public.Settings.MemberReadMode = false

	private := &BoardInfo{
		Moderators: []BoardModerator{{UserID: "mod1"}},
		Members:    []BoardMember{{UserID: "member1"}},
	}
	private.Settings.MemberReadMode = true

	admin := &User{ID: "a", Role: "admin"}
	mod := &User{ID: "mod1", Role: "user"}
	member := &User{ID: "member1", Role: "user"}
	stranger := &User{ID: "x", Role: "user"}

	// Public board: readable by anyone, including a nil/guest actor.
	for _, u := range []*User{nil, admin, mod, member, stranger} {
		if !ActorCanReadBoard(u, public) {
			t.Fatalf("public board should be readable by %v", u)
		}
	}

	// Member-read-mode board: only site mod/admin, board moderators, members.
	if ActorCanReadBoard(stranger, private) {
		t.Fatal("a stranger must not read a member-read-mode board")
	}
	if ActorCanReadBoard(nil, private) {
		t.Fatal("a guest must not read a member-read-mode board")
	}
	for _, u := range []*User{admin, mod, member} {
		if !ActorCanReadBoard(u, private) {
			t.Fatalf("%s should be able to read the private board", u.ID)
		}
	}
}
