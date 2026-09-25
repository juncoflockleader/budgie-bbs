package projections

import "testing"

func TestMailAttachmentFilenames(t *testing.T) {
	if got := MailAttachmentFilenames(nil); got != nil {
		t.Fatalf("MailAttachmentFilenames(nil) = %#v, want nil", got)
	}
	got := MailAttachmentFilenames([]MailAttachment{
		{Filename: "first.txt"},
		{Filename: " second.txt "},
	})
	want := []string{"first.txt", " second.txt "}
	if len(got) != len(want) {
		t.Fatalf("MailAttachmentFilenames len = %d, want %d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("MailAttachmentFilenames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
