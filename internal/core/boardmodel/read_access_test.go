package boardmodel

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func TestActorCanReadBoard(t *testing.T) {
	public := &projections.BoardInfo{}
	public.Settings.MemberReadMode = false

	private := &projections.BoardInfo{
		Moderators: []projections.BoardModerator{{UserID: "mod1"}},
		Members:    []projections.BoardMember{{UserID: "member1"}},
	}
	private.Settings.MemberReadMode = true

	admin := &projections.User{ID: "a", Role: "admin"}
	mod := &projections.User{ID: "mod1", Role: "user"}
	member := &projections.User{ID: "member1", Role: "user"}
	stranger := &projections.User{ID: "x", Role: "user"}

	// Public board: readable by anyone, including a nil/internal actor.
	for _, u := range []*projections.User{nil, admin, mod, member, stranger} {
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
	for _, u := range []*projections.User{admin, mod, member} {
		if !ActorCanReadBoard(u, private) {
			t.Fatalf("%s should be able to read the private board", u.ID)
		}
	}
}

func TestActorCanReadBoardGuestAccess(t *testing.T) {
	guest := &projections.User{Role: "guest"}
	private := &projections.BoardInfo{}
	private.Settings.MemberReadMode = true
	private.Settings.GuestAccess = "public"
	if !ActorCanReadBoard(guest, private) {
		t.Fatal("guest public override should grant access")
	}

	public := &projections.BoardInfo{}
	public.Settings.GuestAccess = "hidden"
	if ActorCanReadBoard(guest, public) {
		t.Fatal("guest hidden override should deny access")
	}
}

func TestBoardInfoPermissions(t *testing.T) {
	info := &projections.BoardInfo{
		Moderators: []projections.BoardModerator{{UserID: "mod1"}},
		Members: []projections.BoardMember{
			{UserID: "member1"},
			{UserID: "manager", CanManageMembers: true},
			{UserID: "postmod", CanModeratePosts: true},
		},
	}
	if !ActorModeratesBoard(&projections.User{ID: "mod1", Role: "user"}, info) {
		t.Fatal("board moderator should moderate board")
	}
	if !ActorCanManageBoardMembers(&projections.User{ID: "manager", Role: "user"}, info) {
		t.Fatal("member manager should manage board members")
	}
	if !ActorCanModerateBoardPosts(&projections.User{ID: "postmod", Role: "user"}, info) {
		t.Fatal("post moderator should moderate board posts")
	}
	if ActorCanManageBoardMembers(&projections.User{ID: "member1", Role: "user"}, info) {
		t.Fatal("plain member should not manage board members")
	}
}
