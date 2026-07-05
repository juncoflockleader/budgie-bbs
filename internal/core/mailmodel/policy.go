package mailmodel

import "strings"

const MaxRecipientsPerSend = 50

func AttachmentMutationAllowed(actorID, fromUserID string) bool {
	return fromUserID == actorID
}

func RecipientIgnoreApplies(senderID, recipientID string, mailAll bool) bool {
	return recipientID != senderID && !mailAll
}

func MailAllAllowed(toAll, actorIsAdmin bool) bool {
	return !toAll || actorIsAdmin
}

func RecipientRefsEmpty(count int) bool {
	return count == 0
}

func RecipientRefsTooMany(toAll bool, count int) bool {
	return !toAll && count > MaxRecipientsPerSend
}

func MailGroupIncludesOwner(includesOwner bool) bool {
	return includesOwner
}

func PostAuthorRecipient(authorID, authorName string) (string, bool) {
	recipient := strings.TrimSpace(authorID)
	if recipient == "" {
		recipient = strings.TrimSpace(authorName)
	}
	if recipient == "" || strings.EqualFold(strings.TrimSpace(authorName), "anonymous") {
		return "", false
	}
	return recipient, true
}
