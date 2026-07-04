package commandrules

import (
	"database/sql"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const maxMailRecipientsPerSend = 50

func ResolveMailGroupMutation(queryable Queryable, ownerID string, p proto.SetMailGroupPayload, idForNewGroup func() string) (string, []string, *proto.ErrorDetail) {
	groupID, errDetail := ResolveMailGroupID(queryable, ownerID, p.Group, p.Name, idForNewGroup)
	if errDetail != nil {
		return "", nil, errDetail
	}
	conflictID, err := projections.MailGroupIDByName(queryable, ownerID, p.Name)
	if err != nil {
		return "", nil, internalErr(err)
	}
	if conflictID != "" && conflictID != groupID {
		return "", nil, newErrDetail(proto.ErrValidationFailed, "mail group name already exists", false)
	}
	memberIDs, errDetail := ResolveUniqueMailGroupMembers(queryable, p.Members, ownerID)
	if errDetail != nil {
		return "", nil, errDetail
	}
	return groupID, memberIDs, nil
}

func ResolveMailGroupID(queryable Queryable, ownerID, groupRef, name string, idForNewGroup func() string) (string, *proto.ErrorDetail) {
	if groupRef != "" {
		groupID, err := projections.GetMailGroupID(queryable, ownerID, groupRef)
		if err != nil {
			return "", internalErr(err)
		}
		if groupID == "" {
			return "", newErrDetail(proto.ErrNotFound, "mail group not found", false)
		}
		return groupID, nil
	}
	groupID, err := projections.MailGroupIDByName(queryable, ownerID, name)
	if err != nil {
		return "", internalErr(err)
	}
	if groupID != "" {
		return groupID, nil
	}
	return idForNewGroup(), nil
}

func ValidateMailAttachmentMutation(queryable Queryable, actor *projections.User, mailID string) (map[string]int, *proto.ErrorDetail) {
	fromUserID, found, err := projections.MailSenderID(queryable, mailID)
	if err != nil {
		return nil, internalErr(err)
	}
	if !found {
		return nil, newErrDetail(proto.ErrNotFound, "mail not found", false)
	}
	if fromUserID != actor.ID {
		return nil, newErrDetail(proto.ErrForbidden, "only the sender can attach files to this mail", false)
	}
	count, err := projections.MailAttachmentCount(queryable, mailID)
	if err != nil {
		return nil, internalErr(err)
	}
	if msg := proto.ValidateMailAttachmentCount(count + 1); msg != "" {
		return nil, newErrDetail(proto.ErrValidationFailed, msg, false)
	}
	copyCounts, err := projections.ActiveMailCopyCounts(queryable, mailID)
	if err != nil {
		return nil, internalErr(err)
	}
	return copyCounts, nil
}

func NormalizeMailAttachments(input []proto.AttachmentPayload, idFor func(int) string) ([]proto.AttachmentPayload, *proto.ErrorDetail) {
	if len(input) == 0 {
		return nil, nil
	}
	attachments, msg := proto.NormalizeMailAttachments(input)
	if msg != "" {
		return nil, newErrDetail(proto.ErrValidationFailed, msg, false)
	}
	return proto.WithAttachmentIDs(attachments, idFor), nil
}

func MailCopyCounts(recipients []*projections.User, senderID string, saveSent bool) map[string]int {
	copyCounts := map[string]int{}
	for _, recipient := range recipients {
		if recipient == nil {
			continue
		}
		copyCounts[recipient.ID]++
	}
	if saveSent {
		copyCounts[senderID]++
	}
	return copyCounts
}

func ResolveMailRecipients(queryable Queryable, actor *projections.User, refs []string, mailAll bool) ([]*projections.User, *proto.ErrorDetail) {
	if actor == nil {
		return nil, newErrDetail(proto.ErrForbidden, "authentication required", false)
	}
	recipients := []*projections.User{}
	seen := map[string]bool{}
	for _, ref := range refs {
		target, err := projections.FindUserRef(queryable, ref)
		if err != nil {
			return nil, internalErr(err)
		}
		if target == nil {
			return nil, newErrDetail(proto.ErrNotFound, "recipient not found: "+strings.TrimSpace(ref), false)
		}
		if target.ID != actor.ID && !mailAll {
			ignored, err := projections.UserRelationshipExists(queryable, target.ID, actor.ID, "ignore")
			if err != nil {
				return nil, internalErr(err)
			}
			if ignored {
				return nil, newErrDetail(proto.ErrForbidden, "recipient does not accept mail from this user", false)
			}
		}
		if !seen[target.ID] {
			seen[target.ID] = true
			recipients = append(recipients, target)
		}
	}
	if len(recipients) == 0 {
		return nil, newErrDetail(proto.ErrValidationFailed, "at least one recipient is required", false)
	}
	return recipients, nil
}

func EnsureMailQuota(queryable Queryable, copyCounts map[string]int, addedPerCopy int64) *proto.ErrorDetail {
	if addedPerCopy <= 0 {
		return nil
	}
	for userID, copies := range copyCounts {
		if strings.TrimSpace(userID) == "" || copies <= 0 {
			continue
		}
		added := addedPerCopy * int64(copies)
		ok, err := projections.MailQuotaAllows(queryable, userID, added)
		if err != nil {
			return internalErr(err)
		}
		if !ok {
			return newErrDetail(proto.ErrValidationFailed, "mail quota exceeded for user "+userID, false)
		}
	}
	return nil
}

func ExpandMailRecipients(db *sql.DB, actor *projections.User, p proto.SendMailPayload) ([]string, *proto.ErrorDetail) {
	if actor == nil {
		return nil, newErrDetail(proto.ErrForbidden, "authentication required", false)
	}
	ownerID := actor.ID
	refs := []string{}
	if p.ToAll {
		if !actor.IsAdmin() {
			return nil, newErrDetail(proto.ErrForbidden, "admin role required for mail-all", false)
		}
		allUserIDs, err := projections.ListMailAllRecipientIDs(db, actor.ID)
		if err != nil {
			return nil, internalErr(err)
		}
		refs = append(refs, allUserIDs...)
	}
	for _, ref := range p.To {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if groupRef, ok := strings.CutPrefix(ref, "group:"); ok {
			p.ToGroups = append(p.ToGroups, strings.TrimSpace(groupRef))
			continue
		}
		refs = append(refs, ref)
	}
	for _, groupRef := range p.ToGroups {
		groupRef = strings.TrimSpace(groupRef)
		if groupRef == "" {
			continue
		}
		if proto.IsFriendMailGroupRef(groupRef) {
			friendIDs, err := projections.ListFriendUserIDs(db, ownerID)
			if err != nil {
				return nil, internalErr(err)
			}
			refs = append(refs, friendIDs...)
			continue
		}
		groupID, err := projections.GetMailGroupID(db, ownerID, groupRef)
		if err != nil {
			return nil, internalErr(err)
		}
		if groupID == "" {
			return nil, newErrDetail(proto.ErrNotFound, "mail group not found: "+groupRef, false)
		}
		members, err := projections.ListMailGroupMembers(db, ownerID, groupID)
		if err != nil {
			return nil, internalErr(err)
		}
		for _, member := range members {
			refs = append(refs, member.UserID)
		}
	}
	if p.ToFriends {
		friendIDs, err := projections.ListFriendUserIDs(db, ownerID)
		if err != nil {
			return nil, internalErr(err)
		}
		refs = append(refs, friendIDs...)
	}
	if len(refs) == 0 {
		return nil, newErrDetail(proto.ErrValidationFailed, "at least one recipient is required", false)
	}
	if !p.ToAll && len(refs) > maxMailRecipientsPerSend {
		return nil, newErrDetail(proto.ErrValidationFailed, "too many recipients in one message", false)
	}
	return refs, nil
}

func ResolveUniqueMailGroupMembers(queryable Queryable, refs []string, ownerID string) ([]string, *proto.ErrorDetail) {
	ids, missingRef, includesOwner, err := projections.ResolveMailGroupMemberIDs(queryable, refs, ownerID)
	if err != nil {
		return nil, internalErr(err)
	}
	if missingRef != "" {
		return nil, newErrDetail(proto.ErrNotFound, "user not found: "+missingRef, false)
	}
	if includesOwner {
		return nil, newErrDetail(proto.ErrValidationFailed, "mail group cannot include yourself", false)
	}
	return ids, nil
}
