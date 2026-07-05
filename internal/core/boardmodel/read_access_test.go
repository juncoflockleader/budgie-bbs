package boardmodel

import "testing"

func TestActorCanReadBoard(t *testing.T) {
	public := &AccessInfo{}
	public.Settings.MemberReadMode = false

	private := &AccessInfo{
		Moderators: []AccessModerator{{UserID: "mod1"}},
		Members:    []AccessMember{{UserID: "member1"}},
	}
	private.Settings.MemberReadMode = true

	admin := &AccessActor{ID: "a", Role: "admin"}
	mod := &AccessActor{ID: "mod1", Role: "user"}
	member := &AccessActor{ID: "member1", Role: "user"}
	stranger := &AccessActor{ID: "x", Role: "user"}

	// Public board: readable by anyone, including a nil/internal actor.
	for _, u := range []*AccessActor{nil, admin, mod, member, stranger} {
		if !ActorCanReadBoard(u, public) {
			t.Fatalf("public board should be readable by %v", u)
		}
	}

	// Member-read-mode board: only site mod/admin, board moderators, members.
	if ActorCanReadBoard(stranger, private) {
		t.Fatal("a stranger must not read a member-read-mode board")
	}
	if ActorCanReadBoard(nil, private) {
		t.Fatal("a nil/internal actor must not read a member-read-mode board")
	}
	for _, u := range []*AccessActor{admin, mod, member} {
		if !ActorCanReadBoard(u, private) {
			t.Fatalf("%s should be able to read the private board", u.ID)
		}
	}
}

func TestActorCanReadBoardGuestAccess(t *testing.T) {
	guest := &AccessActor{Role: "guest"}
	private := &AccessInfo{}
	private.Settings.MemberReadMode = true
	private.Settings.GuestAccess = "public"
	if !ActorCanReadBoard(guest, private) {
		t.Fatal("guest public override should grant access")
	}

	public := &AccessInfo{}
	public.Settings.GuestAccess = "hidden"
	if ActorCanReadBoard(guest, public) {
		t.Fatal("guest hidden override should deny access")
	}
}

func TestBoardInfoPermissions(t *testing.T) {
	info := &AccessInfo{
		Moderators: []AccessModerator{{UserID: "mod1"}},
		Members: []AccessMember{
			{UserID: "member1"},
			{UserID: "manager", CanManageMembers: true},
			{UserID: "postmod", CanModeratePosts: true},
		},
	}
	if !ActorModeratesBoard(&AccessActor{ID: "mod1", Role: "user"}, info) {
		t.Fatal("board moderator should moderate board")
	}
	if !ActorCanManageBoardMembers(&AccessActor{ID: "manager", Role: "user"}, info) {
		t.Fatal("member manager should manage board members")
	}
	if !ActorCanModerateBoardPosts(&AccessActor{ID: "postmod", Role: "user"}, info) {
		t.Fatal("post moderator should moderate board posts")
	}
	if ActorCanManageBoardMembers(&AccessActor{ID: "member1", Role: "user"}, info) {
		t.Fatal("plain member should not manage board members")
	}
}
