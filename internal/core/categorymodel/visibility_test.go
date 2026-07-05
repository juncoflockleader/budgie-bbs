package categorymodel

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func TestNormalizeVisibility(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		err  bool
	}{
		{name: "blank", raw: " ", want: VisibilityPublic},
		{name: "public", raw: " PUBLIC ", want: VisibilityPublic},
		{name: "staff", raw: "staff", want: VisibilityStaff},
		{name: "hidden", raw: "hidden", want: VisibilityHidden},
		{name: "invalid", raw: "private", err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeVisibility(tt.raw)
			if tt.err {
				if err == nil {
					t.Fatalf("NormalizeVisibility(%q) err=nil, want error", tt.raw)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("NormalizeVisibility(%q) = %q, %v; want %q, nil", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestFilterVisible(t *testing.T) {
	categories := []projections.Category{
		{ID: "public", Visibility: ""},
		{ID: "staff", Visibility: "staff"},
		{ID: "hidden", Visibility: "hidden"},
		{ID: "invalid", Visibility: "private"},
	}
	if got := visibleIDs(FilterVisible(categories, nil)); len(got) != 1 || got[0] != "public" {
		t.Fatalf("guest visible categories = %v, want [public]", got)
	}
	mod := &projections.User{ID: "usr_mod", Role: "moderator"}
	if got := visibleIDs(FilterVisible(categories, mod)); len(got) != 2 || got[0] != "public" || got[1] != "staff" {
		t.Fatalf("moderator visible categories = %v, want [public staff]", got)
	}
	admin := &projections.User{ID: "usr_admin", Role: "admin"}
	if got := visibleIDs(FilterVisible(categories, admin)); len(got) != len(categories) {
		t.Fatalf("admin visible categories = %v, want all categories", got)
	}
}

func visibleIDs(categories []projections.Category) []string {
	ids := make([]string, 0, len(categories))
	for _, category := range categories {
		ids = append(ids, category.ID)
	}
	return ids
}
