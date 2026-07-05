package boardmodel

type PostPolicySettings struct {
	ReadOnly       bool
	NoReply        bool
	MailInAllowed  bool
	MemberReadMode bool
	MemberPostMode bool
}

func (s *PostPolicySettings) AllowsMailIn(canModerateBoard bool) bool {
	return s == nil || s.MailInAllowed || canModerateBoard
}

func (s *PostPolicySettings) BlocksThreadCreation(canModerateBoard bool) bool {
	return s != nil && s.ReadOnly && !canModerateBoard
}

func (s *PostPolicySettings) BlocksReply(canModerateBoard bool) bool {
	return s != nil && (s.ReadOnly || s.NoReply) && !canModerateBoard
}

func (s *PostPolicySettings) RequiresPostingMembership() bool {
	return s != nil && (s.MemberReadMode || s.MemberPostMode)
}

func (s *PostPolicySettings) RequiresReadMembership() bool {
	return s != nil && s.MemberReadMode
}
