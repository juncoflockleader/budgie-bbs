package mailmodel

type Recipient struct {
	ID string
}

func CopyCounts(recipients []Recipient, senderID string, saveSent bool) map[string]int {
	copyCounts := map[string]int{}
	for _, recipient := range recipients {
		copyCounts[recipient.ID]++
	}
	if saveSent {
		copyCounts[senderID]++
	}
	return copyCounts
}
