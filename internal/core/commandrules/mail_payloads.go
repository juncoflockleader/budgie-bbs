package commandrules

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/mailmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func ForwardMailSendPayload(p proto.ForwardMailPayload, source *projections.MailItem) proto.SendMailPayload {
	return proto.SendMailPayload{
		To:        p.To,
		ToGroups:  p.ToGroups,
		ToFriends: p.ToFriends,
		ToAll:     p.ToAll,
		Subject:   proto.NormalizeForwardMailSubject(p.Subject, source.Subject),
		Body:      proto.FormatForwardMailBody(p.Note, source.FromName, source.ToNames, source.Subject, projections.MailAttachmentFilenames(source.Attachments), source.Body),
		SaveSent:  p.SaveSent,
	}
}

func PostMailToBoardContent(p proto.PostMailToBoardPayload, source *projections.MailItem) (title, body string) {
	return proto.PostMailToBoardTitle(p.Subject, source.Subject),
		proto.FormatMailBoardBody(p.Note, source.FromName, source.ToNames, source.Subject, projections.MailAttachmentFilenames(source.Attachments), source.Body)
}

func DigestEntryMailSendPayload(p proto.SendDigestEntryMailPayload, export *projections.DigestExport) proto.SendMailPayload {
	body := projections.FormatDigestExportText(export)
	if note := p.Note; note != "" {
		body = note + "\n\n" + body
	}
	return proto.SendMailPayload{
		To:        p.To,
		ToGroups:  p.ToGroups,
		ToFriends: p.ToFriends,
		ToAll:     p.ToAll,
		Subject:   proto.DigestEntryMailSubject(p.Subject, export.Entry.Title),
		Body:      body,
		SaveSent:  p.SaveSent,
	}
}

func MailPostAuthorSendPayload(actor *projections.User, p proto.MailPostAuthorPayload, thread *projections.Thread, post *projections.Post) (proto.SendMailPayload, *proto.ErrorDetail) {
	recipient, ok := mailmodel.PostAuthorRecipient(post.AuthorID, post.Author)
	if !ok {
		return proto.SendMailPayload{}, newErrDetail(proto.ErrValidationFailed, "anonymous article author cannot receive mail", false)
	}
	return proto.SendMailPayload{
		To:       []string{recipient},
		Subject:  proto.MailPostAuthorSubject(p.Subject, thread.Title),
		Body:     proto.FormatPostAuthorMailBody(thread.Board, thread.Title, post.CreatedSeq, post.ID, post.Author, actor.Name, p.Body, post.Body),
		SaveSent: p.SaveSent,
	}, nil
}
