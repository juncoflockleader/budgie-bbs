package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const maxAttachmentUploadBytes int64 = 20 << 20

// POST /api/v1/posts/{post}/attachments
func (s *Server) handleUploadPostAttachment(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	postID := r.PathValue("post")
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(maxAttachmentUploadBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "validation_failed", "attachment upload is too large", false)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "file is required", false)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxAttachmentUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if int64(len(data)) > maxAttachmentUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "validation_failed", "attachment upload is too large", false)
		return
	}
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" && len(data) > 0 {
		contentType = http.DetectContentType(data)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	cid := r.Header.Get("X-Command-Id")
	p := proto.AttachPostPayload{
		Post:        postID,
		Filename:    header.Filename,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
	}
	if s.core.AuthoritativeCommandLogEnabled() {
		actorID := ""
		if actor != nil {
			actorID = actor.ID
		}
		attachmentSeed := cid
		if attachmentSeed != "" && actorID != "" {
			attachmentSeed = actorID + "\x00" + attachmentSeed
		}
		attachmentID := core.NewPostAttachmentID(attachmentSeed)
		p.ID = attachmentID
		p.StagedBlobID = attachmentID
		if err := s.core.StagePostAttachmentBlob(attachmentID, actorID, data, contentType); err != nil {
			if core.IsStagedAttachmentBlobConflict(err) {
				writeError(w, http.StatusConflict, proto.ErrConflict, "staged attachment bytes conflict with this command id", false)
				return
			}
			writeError(w, http.StatusConflict, proto.ErrBlobStagingRequired, err.Error(), true)
			return
		}
	}
	raw, _ := json.Marshal(p)
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdAttachPost, raw, cid)
	if reply.Err != nil && p.StagedBlobID != "" {
		_ = s.core.DiscardStagedPostAttachmentBlob(p.StagedBlobID)
	}
	if reply.Err != nil || isPendingAckReply(reply) || reply.Result == nil || reply.Result.ID == "" {
		writeAck(w, cid, reply)
		return
	}
	if err := s.core.StoreAttachmentBlob(reply.Result.ID, data, contentType); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeAck(w, cid, reply)
}

// POST /api/v1/mail/{mail}/attachments
func (s *Server) handleUploadMailAttachment(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	mailID := r.PathValue("mail")
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(maxAttachmentUploadBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "validation_failed", "attachment upload is too large", false)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "file is required", false)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxAttachmentUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if int64(len(data)) > maxAttachmentUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "validation_failed", "attachment upload is too large", false)
		return
	}
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" && len(data) > 0 {
		contentType = http.DetectContentType(data)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	cid := r.Header.Get("X-Command-Id")
	p := proto.AttachMailPayload{
		Mail:        mailID,
		Filename:    header.Filename,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
	}
	if s.core.AuthoritativeCommandLogEnabled() {
		actorID := ""
		if actor != nil {
			actorID = actor.ID
		}
		attachmentSeed := cid
		if attachmentSeed != "" && actorID != "" {
			attachmentSeed = actorID + "\x00" + attachmentSeed
		}
		attachmentID := core.NewMailAttachmentID(attachmentSeed)
		p.ID = attachmentID
		p.StagedBlobID = attachmentID
		if err := s.core.StageMailAttachmentBlob(attachmentID, actorID, data, contentType); err != nil {
			if core.IsStagedAttachmentBlobConflict(err) {
				writeError(w, http.StatusConflict, proto.ErrConflict, "staged mail attachment bytes conflict with this command id", false)
				return
			}
			writeError(w, http.StatusConflict, proto.ErrBlobStagingRequired, err.Error(), true)
			return
		}
	}
	raw, _ := json.Marshal(p)
	reply := s.core.ExecCmd(r.Context(), actor, proto.CmdAttachMail, raw, cid)
	if reply.Err != nil && p.StagedBlobID != "" {
		_ = s.core.DiscardStagedMailAttachmentBlob(p.StagedBlobID)
	}
	if reply.Err != nil || isPendingAckReply(reply) || reply.Result == nil || reply.Result.ID == "" {
		writeAck(w, cid, reply)
		return
	}
	if err := s.core.StoreMailAttachmentBlob(reply.Result.ID, data, contentType); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeAck(w, cid, reply)
}

// GET /api/v1/attachments/{attachment}
func (s *Server) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	attachmentID := r.PathValue("attachment")
	att, err := s.core.GetPostAttachment(attachmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if att == nil {
		writeError(w, http.StatusNotFound, "not_found", "attachment not found", false)
		return
	}
	post, err := s.core.GetPost(att.PostID)
	if err != nil || post == nil {
		writeError(w, http.StatusNotFound, "not_found", "post not found", false)
		return
	}
	thread, err := s.core.GetThread(post.Thread)
	if err != nil || thread == nil {
		writeError(w, http.StatusNotFound, "not_found", "thread not found", false)
		return
	}
	if ok, err := s.actorCanReadBoard(actor, thread.Board); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	} else if !ok {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	data, contentType, err := s.core.GetAttachmentBlob(attachmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if data == nil {
		writeError(w, http.StatusNotFound, "not_found", "attachment data not found", false)
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": att.Filename}))
	http.ServeContent(w, r, att.Filename, time.UnixMilli(att.CreatedAt), bytes.NewReader(data))
}

// GET /api/v1/mail/attachments/{attachment}
func (s *Server) handleDownloadMailAttachment(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	attachmentID := r.PathValue("attachment")
	att, err := s.core.GetMailAttachment(attachmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if att == nil {
		writeError(w, http.StatusNotFound, "not_found", "attachment not found", false)
		return
	}
	mail, err := s.core.GetMail(actor.ID, att.MailID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if mail == nil && att.CreatedBy != actor.ID {
		writeError(w, http.StatusNotFound, "not_found", "attachment not found", false)
		return
	}
	data, contentType, err := s.core.GetMailAttachmentBlob(attachmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if data == nil {
		writeError(w, http.StatusNotFound, "not_found", "attachment data not found", false)
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": att.Filename}))
	http.ServeContent(w, r, att.Filename, time.UnixMilli(att.CreatedAt), bytes.NewReader(data))
}

// GET /api/v1/digest/{entry}/download
func (s *Server) handleDownloadDigestEntry(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	entryID := r.PathValue("entry")
	export, err := s.core.GetDigestExport(entryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	if export == nil {
		writeError(w, http.StatusNotFound, "not_found", "digest entry not found", false)
		return
	}
	if ok, err := s.actorCanReadBoard(actor, export.Entry.BoardID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	} else if !ok {
		writeError(w, http.StatusForbidden, proto.ErrForbidden, "board members only", false)
		return
	}
	filename := digestExportFilename(export)
	data := []byte(core.FormatDigestExportText(export))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	http.ServeContent(w, r, filename, time.UnixMilli(export.Entry.UpdatedAt), bytes.NewReader(data))
}

func digestExportFilename(export *core.DigestExport) string {
	name := strings.TrimSpace(export.Entry.Title)
	if name == "" {
		name = export.Entry.ID
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	name = strings.Trim(b.String(), "._-")
	if name == "" {
		name = export.Entry.ID
	}
	if !strings.HasSuffix(strings.ToLower(name), ".txt") {
		name += ".txt"
	}
	return name
}
