package favoritemodel

type FolderParentFailure string

const (
	FolderParentOK         FolderParentFailure = ""
	FolderParentSelf       FolderParentFailure = "self"
	FolderParentDescendant FolderParentFailure = "descendant"
)

func BoardZapAllowed(zapped, zapAllowed bool) bool {
	return !zapped || zapAllowed
}

func FolderSelfParentFailure(folderID, parentID string) FolderParentFailure {
	if folderID != "" && parentID == folderID {
		return FolderParentSelf
	}
	return FolderParentOK
}

func FolderDescendantParentFailure(containsParent bool) FolderParentFailure {
	if containsParent {
		return FolderParentDescendant
	}
	return FolderParentOK
}
