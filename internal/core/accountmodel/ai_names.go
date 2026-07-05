package accountmodel

import "strings"

const ReservedAIBotNameSuffix = "-ai"

func BoardAIBotName(boardID string) string {
	return boardID + ReservedAIBotNameSuffix
}

func IsReservedAIBotName(name string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), ReservedAIBotNameSuffix)
}
