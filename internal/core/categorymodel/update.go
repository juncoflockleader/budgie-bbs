package categorymodel

import (
	"fmt"
	"strings"
)

type Category struct {
	ID          string
	Name        string
	Description string
	ParentID    string
	Position    int
	Visibility  string
}

type UpdatePatch struct {
	Name        *string
	Description *string
	ParentID    *string
	Position    *int
	Visibility  *string
}

type UpdatePlan struct {
	Name          string
	Description   string
	ParentID      string
	Position      int
	Visibility    string
	ParentChanged bool
}

func PlanUpdate(category Category, patch UpdatePatch) (UpdatePlan, error) {
	plan := UpdatePlan{
		Name:        category.Name,
		Description: category.Description,
		ParentID:    category.ParentID,
		Position:    category.Position,
		Visibility:  category.Visibility,
	}
	if patch.Name != nil {
		plan.Name = strings.TrimSpace(*patch.Name)
		if plan.Name == "" {
			return UpdatePlan{}, fmt.Errorf("category name required")
		}
	}
	if patch.Description != nil {
		plan.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.ParentID != nil {
		plan.ParentID = strings.TrimSpace(*patch.ParentID)
		plan.ParentChanged = plan.ParentID != category.ParentID
		if plan.ParentID == category.ID {
			return UpdatePlan{}, fmt.Errorf("category cannot be its own parent")
		}
	}
	if patch.Position != nil {
		if *patch.Position < 0 {
			return UpdatePlan{}, fmt.Errorf("position cannot be negative")
		}
		plan.Position = *patch.Position
	}
	if patch.Visibility != nil {
		visibility, err := NormalizeVisibility(*patch.Visibility)
		if err != nil {
			return UpdatePlan{}, err
		}
		plan.Visibility = visibility
	}
	return plan, nil
}
