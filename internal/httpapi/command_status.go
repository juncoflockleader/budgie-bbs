package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func (s *Server) handleGetCommandStatus(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	commandID := strings.TrimSpace(r.PathValue("command"))
	partitionKind := firstQueryValue(r, "commandPartitionKind", "partitionKind")
	partitionKey := firstQueryValue(r, "commandPartitionKey", "partitionKey")
	offsetText := firstQueryValue(r, "commandOffset", "offset")
	if commandID == "" || partitionKind == "" || partitionKey == "" || offsetText == "" {
		writeError(w, http.StatusUnprocessableEntity, proto.ErrValidationFailed, "command, partition kind, partition key, and offset are required", false)
		return
	}
	offset, err := strconv.ParseInt(offsetText, 10, 64)
	if err != nil || offset <= 0 {
		writeError(w, http.StatusUnprocessableEntity, proto.ErrValidationFailed, "command offset must be a positive integer", false)
		return
	}
	status, err := s.core.CommandStatus(r.Context(), actor, commandID, core.LogPartition{
		Kind: partitionKind,
		Key:  partitionKey,
	}, offset)
	if errors.Is(err, core.ErrCommandStatusNotFound) {
		writeError(w, http.StatusNotFound, proto.ErrNotFound, "command status not found", false)
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, proto.ErrCommandLogUnavailable, err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func firstQueryValue(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
			return value
		}
	}
	return ""
}
