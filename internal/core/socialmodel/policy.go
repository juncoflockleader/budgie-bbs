package socialmodel

type OtherUserFailure string

const (
	OtherUserOK   OtherUserFailure = ""
	OtherUserSelf OtherUserFailure = "self"
)

type LoginWatchFailure string

const (
	LoginWatchOK             LoginWatchFailure = ""
	LoginWatchFriendRequired LoginWatchFailure = "friend_required"
)

type BlessingFailure string

const (
	BlessingOK             BlessingFailure = ""
	BlessingTargetIgnores  BlessingFailure = "target_ignores"
	BlessingAlreadyBlessed BlessingFailure = "already_blessed"
)

type DirectMessageRecipientFailure string

const (
	DirectMessageRecipientOK          DirectMessageRecipientFailure = ""
	DirectMessageRecipientIgnored     DirectMessageRecipientFailure = "ignored"
	DirectMessageRecipientFriendsOnly DirectMessageRecipientFailure = "friends_only"
)

func OtherUserFailureFor(sameUser bool) OtherUserFailure {
	if sameUser {
		return OtherUserSelf
	}
	return OtherUserOK
}

func LoginWatchStartFailure(active, friend bool) LoginWatchFailure {
	if active && !friend {
		return LoginWatchFriendRequired
	}
	return LoginWatchOK
}

func BlessingFailureFor(ignored, alreadyBlessed bool) BlessingFailure {
	if ignored {
		return BlessingTargetIgnores
	}
	if alreadyBlessed {
		return BlessingAlreadyBlessed
	}
	return BlessingOK
}

func DirectMessageRecipientFailureFor(ignored, allowed bool) DirectMessageRecipientFailure {
	if ignored {
		return DirectMessageRecipientIgnored
	}
	if !allowed {
		return DirectMessageRecipientFriendsOnly
	}
	return DirectMessageRecipientOK
}
