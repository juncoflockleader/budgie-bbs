package categorystore

import (
	"database/sql"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core/categorymodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

func GetTx(tx *sql.Tx, categoryID string) (*projections.Category, error) {
	var category projections.Category
	err := sqlstore.QueryRow(tx,
		`SELECT id, name, description, parent_id, position, visibility, created_at, updated_at
		   FROM categories WHERE id=?`,
		categoryID,
	).Scan(&category.ID, &category.Name, &category.Description, &category.ParentID, &category.Position, &category.Visibility, &category.CreatedAt, &category.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &category, err
}

func ValidateParentTx(tx *sql.Tx, categoryID, parentID string) error {
	seen := map[string]bool{categoryID: true}
	for parentID != "" {
		if seen[parentID] {
			return fmt.Errorf("category parent would create a cycle")
		}
		seen[parentID] = true
		var next string
		err := sqlstore.QueryRow(tx, `SELECT parent_id FROM categories WHERE id=?`, parentID).Scan(&next)
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		if err != nil {
			return err
		}
		parentID = next
	}
	return nil
}

func UpdateTx(tx *sql.Tx, categoryID string, plan categorymodel.UpdatePlan, ts int64) error {
	if _, err := sqlstore.Exec(tx,
		`UPDATE categories
		    SET name=?, description=?, parent_id=?, position=?, visibility=?, updated_at=?
		  WHERE id=?`,
		plan.Name, plan.Description, plan.ParentID, plan.Position, plan.Visibility, ts, categoryID,
	); err != nil {
		return err
	}
	_, err := sqlstore.Exec(tx, `UPDATE boards SET name=?, description=? WHERE id=?`, plan.Name, plan.Description, categoryID)
	return err
}
