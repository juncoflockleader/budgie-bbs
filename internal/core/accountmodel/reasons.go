package accountmodel

import "strings"

const MaxAccountClosureReasonLength = 500

func NormalizeAccountClosureReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > MaxAccountClosureReasonLength {
		reason = reason[:MaxAccountClosureReasonLength]
	}
	return reason
}
