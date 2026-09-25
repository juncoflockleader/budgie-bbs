package boardmodel

type MembershipApplicationFailure string

const (
	MembershipApplicationOK              MembershipApplicationFailure = ""
	MembershipApplicationAlreadyMember   MembershipApplicationFailure = "already_member"
	MembershipApplicationAlreadyPending  MembershipApplicationFailure = "already_pending"
	MembershipApplicationBlocked         MembershipApplicationFailure = "blocked"
	MembershipApplicationAlreadyReviewed MembershipApplicationFailure = "already_reviewed"
)

const membershipApplicationPendingStatus = "pending"

func MembershipApplicantFailure(isMember bool) MembershipApplicationFailure {
	if isMember {
		return MembershipApplicationAlreadyMember
	}
	return MembershipApplicationOK
}

func MembershipApplicationStartFailure(latestStatus string) MembershipApplicationFailure {
	switch latestStatus {
	case membershipApplicationPendingStatus:
		return MembershipApplicationAlreadyPending
	case "blacklisted":
		return MembershipApplicationBlocked
	default:
		return MembershipApplicationOK
	}
}

func MembershipApplicationReviewFailure(status string) MembershipApplicationFailure {
	if status != membershipApplicationPendingStatus {
		return MembershipApplicationAlreadyReviewed
	}
	return MembershipApplicationOK
}
