package categorymodel

import (
	"fmt"
	"strings"
)

const (
	VisibilityPublic = "public"
	VisibilityStaff  = "staff"
	VisibilityHidden = "hidden"
)

type Viewer struct {
	Role string
}

func (v Viewer) IsAdmin() bool {
	return v.Role == "admin"
}

func (v Viewer) IsMod() bool {
	return v.Role == "moderator" || v.Role == "admin"
}

func NormalizeVisibility(raw string) (string, error) {
	visibility := strings.TrimSpace(strings.ToLower(raw))
	if visibility == "" {
		return VisibilityPublic, nil
	}
	switch visibility {
	case VisibilityPublic, VisibilityStaff, VisibilityHidden:
		return visibility, nil
	default:
		return "", fmt.Errorf(`visibility must be "public", "staff", or "hidden"`)
	}
}

func VisibleToUser(category Category, viewer *Viewer) bool {
	if viewer != nil && viewer.IsAdmin() {
		return true
	}
	visibility, err := NormalizeVisibility(category.Visibility)
	if err != nil {
		return false
	}
	switch visibility {
	case VisibilityPublic:
		return true
	case VisibilityStaff:
		return viewer != nil && viewer.IsMod()
	default:
		return false
	}
}

func FilterVisible(categories []Category, viewer *Viewer) []Category {
	if viewer != nil && viewer.IsAdmin() {
		return categories
	}
	out := make([]Category, 0, len(categories))
	for _, category := range categories {
		if VisibleToUser(category, viewer) {
			out = append(out, category)
		}
	}
	return out
}
