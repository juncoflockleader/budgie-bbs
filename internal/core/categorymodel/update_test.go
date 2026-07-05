package categorymodel

import "testing"

func TestPlanUpdateNormalizesPatch(t *testing.T) {
	category := Category{
		ID:          "sports",
		Name:        "Sports",
		Description: "Sports",
		ParentID:    "general",
		Position:    1,
		Visibility:  VisibilityPublic,
	}
	name := " Athletics "
	description := " Sports desk "
	parentID := " clubs "
	visibility := " STAFF "
	plan, err := PlanUpdate(category, UpdatePatch{
		Name:        &name,
		Description: &description,
		ParentID:    &parentID,
		Visibility:  &visibility,
	})
	if err != nil {
		t.Fatalf("PlanUpdate: %v", err)
	}
	if plan.Name != "Athletics" || plan.Description != "Sports desk" || plan.ParentID != "clubs" || !plan.ParentChanged || plan.Position != 1 || plan.Visibility != VisibilityStaff {
		t.Fatalf("PlanUpdate() = %+v, want normalized update plan", plan)
	}
}

func TestPlanUpdateRejectsInvalidPatch(t *testing.T) {
	category := Category{ID: "sports", Name: "Sports", ParentID: "general"}
	blankName := " "
	if _, err := PlanUpdate(category, UpdatePatch{Name: &blankName}); err == nil {
		t.Fatalf("PlanUpdate accepted blank name")
	}
	selfParent := " sports "
	if _, err := PlanUpdate(category, UpdatePatch{ParentID: &selfParent}); err == nil {
		t.Fatalf("PlanUpdate accepted self parent")
	}
	negative := -1
	if _, err := PlanUpdate(category, UpdatePatch{Position: &negative}); err == nil {
		t.Fatalf("PlanUpdate accepted negative position")
	}
	invalidVisibility := "private"
	if _, err := PlanUpdate(category, UpdatePatch{Visibility: &invalidVisibility}); err == nil {
		t.Fatalf("PlanUpdate accepted invalid visibility")
	}
}
