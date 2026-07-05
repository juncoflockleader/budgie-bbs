package favoritemodel

import "testing"

func TestBoardZapAllowed(t *testing.T) {
	tests := []struct {
		name       string
		zapped     bool
		zapAllowed bool
		want       bool
	}{
		{name: "not zapping is allowed", zapped: false, zapAllowed: false, want: true},
		{name: "zapping allowed board succeeds", zapped: true, zapAllowed: true, want: true},
		{name: "zapping disabled board fails", zapped: true, zapAllowed: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BoardZapAllowed(tt.zapped, tt.zapAllowed); got != tt.want {
				t.Fatalf("BoardZapAllowed(%v, %v) = %v, want %v", tt.zapped, tt.zapAllowed, got, tt.want)
			}
		})
	}
}

func TestFolderSelfParentFailure(t *testing.T) {
	tests := []struct {
		name     string
		folderID string
		parentID string
		want     FolderParentFailure
	}{
		{name: "same folder", folderID: "work", parentID: "work", want: FolderParentSelf},
		{name: "valid parent", folderID: "work", parentID: "root", want: FolderParentOK},
		{name: "blank folder is not self parent", folderID: "", parentID: "", want: FolderParentOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FolderSelfParentFailure(tt.folderID, tt.parentID); got != tt.want {
				t.Fatalf("FolderSelfParentFailure(%q, %q) = %q, want %q", tt.folderID, tt.parentID, got, tt.want)
			}
		})
	}
}

func TestFolderDescendantParentFailure(t *testing.T) {
	if got := FolderDescendantParentFailure(true); got != FolderParentDescendant {
		t.Fatalf("FolderDescendantParentFailure(true) = %q, want %q", got, FolderParentDescendant)
	}
	if got := FolderDescendantParentFailure(false); got != FolderParentOK {
		t.Fatalf("FolderDescendantParentFailure(false) = %q, want %q", got, FolderParentOK)
	}
}
