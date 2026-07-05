package core

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/core/accountstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/categorymodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/categorystore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func (c *Core) ListCategoriesForUser(viewer *projections.User) ([]projections.Category, error) {
	categories, err := c.ListCategories()
	if err != nil {
		return nil, err
	}
	if viewer != nil && viewer.IsAdmin() {
		return categories, nil
	}
	out := make([]projections.Category, 0, len(categories))
	for _, category := range categories {
		if categorymodel.VisibleToUser(categoryModelCategory(category), categoryModelViewer(viewer)) {
			out = append(out, category)
		}
	}
	return out, nil
}

func categoryModelViewer(viewer *projections.User) *categorymodel.Viewer {
	if viewer == nil {
		return nil
	}
	return &categorymodel.Viewer{Role: viewer.Role}
}

func categoryModelCategory(category projections.Category) categorymodel.Category {
	return categorymodel.Category{
		ID:          category.ID,
		Name:        category.Name,
		Description: category.Description,
		ParentID:    category.ParentID,
		Position:    category.Position,
		Visibility:  category.Visibility,
	}
}

func (c *Core) UpdateCategory(actorID, categoryID string, patch categorymodel.UpdatePatch) (*projections.Category, error) {
	actorID = strings.TrimSpace(actorID)
	categoryID = strings.TrimSpace(categoryID)
	if actorID == "" || categoryID == "" {
		return nil, sql.ErrNoRows
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	actor, err := accountstore.UserByIDTx(tx, actorID)
	if err != nil {
		return nil, err
	}
	if actor == nil || !actor.IsAdmin() {
		return nil, fmt.Errorf("%w: admin role required", ErrAccountDeleteForbidden)
	}
	category, err := categorystore.GetTx(tx, categoryID)
	if err != nil {
		return nil, err
	}
	if category == nil {
		return nil, sql.ErrNoRows
	}

	plan, err := categorymodel.PlanUpdate(categoryModelCategory(*category), patch)
	if err != nil {
		return nil, err
	}
	if plan.ParentChanged {
		if err := categorystore.ValidateParentTx(tx, categoryID, plan.ParentID); err != nil {
			return nil, err
		}
	}
	if patch.Position == nil && plan.ParentChanged {
		plan.Position, err = projections.NextCategoryPosition(tx, plan.ParentID)
		if err != nil {
			return nil, err
		}
	}

	ts := nowMS()
	if err := categorystore.UpdateTx(tx, categoryID, plan, ts); err != nil {
		return nil, err
	}
	updated, err := categorystore.GetTx(tx, categoryID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}
