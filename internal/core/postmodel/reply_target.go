package postmodel

type ReplyTarget struct {
	ID       string
	ThreadID string
	ReplyTo  string
	Redacted bool
	NoReply  bool
	MailBack bool
}

type ReplyTargetPlan struct {
	EffectiveReplyTo  string
	QuoteParent       bool
	MailBackParent    bool
	NotifyParent      bool
	NeedsRootMailBack bool
}

type ReplyTargetFailure string

const (
	ReplyTargetOK              ReplyTargetFailure = ""
	ReplyTargetMissingForQuote ReplyTargetFailure = "missing_for_quote"
	ReplyTargetMissing         ReplyTargetFailure = "missing"
	ReplyTargetWrongThread     ReplyTargetFailure = "wrong_thread"
	ReplyTargetRedactedQuote   ReplyTargetFailure = "redacted_quote"
	ReplyTargetNoReply         ReplyTargetFailure = "no_reply"
)

func PlanReplyTarget(replyTo string, parent *ReplyTarget, threadID string, quotePost, canModerateThread bool) (ReplyTargetPlan, ReplyTargetFailure) {
	if replyTo == "" {
		if quotePost {
			return ReplyTargetPlan{}, ReplyTargetMissingForQuote
		}
		return ReplyTargetPlan{NeedsRootMailBack: true}, ReplyTargetOK
	}
	if parent == nil {
		return ReplyTargetPlan{}, ReplyTargetMissing
	}
	if parent.ThreadID != threadID {
		return ReplyTargetPlan{}, ReplyTargetWrongThread
	}
	plan := ReplyTargetPlan{
		EffectiveReplyTo: parent.ID,
		NotifyParent:     true,
	}
	if quotePost {
		if parent.Redacted {
			return ReplyTargetPlan{}, ReplyTargetRedactedQuote
		}
		plan.QuoteParent = true
	}
	if parent.NoReply && !canModerateThread {
		return ReplyTargetPlan{}, ReplyTargetNoReply
	}
	if parent.MailBack {
		plan.MailBackParent = true
	}
	if parent.ReplyTo != "" {
		plan.EffectiveReplyTo = parent.ReplyTo
	}
	return plan, ReplyTargetOK
}
