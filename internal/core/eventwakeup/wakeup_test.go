package eventwakeup

import "testing"

func TestEncodePostReaction(t *testing.T) {
	got := EncodePostReaction("pst_1", "usr_1", "heart", 42)
	want := `{"post":"pst_1","user":"usr_1","emoji":"heart","ts":42}`
	if got != want {
		t.Fatalf("EncodePostReaction() = %s, want %s", got, want)
	}
}

func TestEncodePollVote(t *testing.T) {
	got := EncodePollVote("poll_1", "usr_1", 42)
	want := `{"poll":"poll_1","user":"usr_1","ts":42}`
	if got != want {
		t.Fatalf("EncodePollVote() = %s, want %s", got, want)
	}
}
