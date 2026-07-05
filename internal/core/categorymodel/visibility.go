package categorymodel

import (
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

const (
	VisibilityPublic = "public"
	VisibilityStaff  = "staff"
	VisibilityHidden = "hidden"
)

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

func VisibleToUser(category projections.Category, viewer *projections.User) bool {
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

func FilterVisible(categories []projections.Category, viewer *projections.User) []projections.Category {
	if viewer != nil && viewer.IsAdmin() {
		return categories
	}
	out := make([]projections.Category, 0, len(categories))
	for _, category := range categories {
		if VisibleToUser(category, viewer) {
			out = append(out, category)
		}
	}
	return out
}
