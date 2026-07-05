package commandrules

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/automodmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

func AutomodReason(reason, ruleID string) string {
	return automodmodel.Reason(reason, ruleID)
}

func AutomodContentRuleMatches(matchType, pattern string, threshold int, text string) (matched bool, handled bool) {
	return automodmodel.ContentRuleMatches(matchType, pattern, threshold, text, proto.AutomodRegexWithinComplexityLimit)
}

func MaxAutomodConsecutiveRun(text string) int {
	return automodmodel.MaxConsecutiveRun(text)
}

func AutomodRegexInput(text string) string {
	return automodmodel.RegexInput(text)
}

func AutomodLinkCount(text string) int {
	return automodmodel.LinkCount(text)
}
