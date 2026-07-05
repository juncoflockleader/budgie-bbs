package accountmodel

import "testing"

func TestAIBotNameRules(t *testing.T) {
	if got := BoardAIBotName("general"); got != "general-ai" {
		t.Fatalf("BoardAIBotName() = %q, want general-ai", got)
	}
	if !IsReservedAIBotName(" General-AI ") {
		t.Fatalf("IsReservedAIBotName() rejected trimmed case-insensitive suffix")
	}
	if IsReservedAIBotName("maintainer") {
		t.Fatalf("IsReservedAIBotName() accepted an ordinary account name")
	}
}
