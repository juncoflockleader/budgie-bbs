package projections

import (
	"database/sql"
	"fmt"
	"strings"
)

type DerivedViewWatermark struct {
	View       string
	AppliedSeq int64
	UpdatedAt  int64
}

func LookupProjectionWatermarkAppliedSeq(queryable sqlLike, watermark string) (int64, bool, error) {
	var applied int64
	err := QQueryRow(queryable, `SELECT applied_seq FROM derived_view_watermarks WHERE view_name=?`, watermark).Scan(&applied)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return applied, true, nil
}

func ListDerivedViewWatermarks(queryable sqlLike) ([]DerivedViewWatermark, error) {
	rows, err := QQuery(queryable,
		`SELECT view_name, applied_seq, updated_at
		   FROM derived_view_watermarks
		  ORDER BY view_name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DerivedViewWatermark
	for rows.Next() {
		var mark DerivedViewWatermark
		if err := rows.Scan(&mark.View, &mark.AppliedSeq, &mark.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, mark)
	}
	return out, rows.Err()
}

func RecordDerivedViewApplied(execable sqlLike, view string, appliedSeq, updatedAt int64) error {
	view = NormalizeDerivedView(view)
	if view == "" {
		return fmt.Errorf("derived view name required")
	}
	return RecordProjectionWatermarkApplied(execable, view, appliedSeq, updatedAt)
}

func RecordProjectionWatermarkApplied(execable sqlLike, watermark string, appliedSeq, updatedAt int64) error {
	watermark = strings.TrimSpace(watermark)
	if watermark == "" {
		return fmt.Errorf("projection watermark name required")
	}
	if appliedSeq < 0 {
		appliedSeq = 0
	}
	_, err := QExec(execable,
		`INSERT INTO derived_view_watermarks (view_name, applied_seq, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(view_name) DO UPDATE
		       SET applied_seq=excluded.applied_seq,
		           updated_at=excluded.updated_at`,
		watermark, appliedSeq, updatedAt,
	)
	return err
}

func RecordProjectionWatermarkAppliedMax(execable sqlLike, watermark string, appliedSeq, updatedAt int64) error {
	watermark = strings.TrimSpace(watermark)
	if watermark == "" {
		return fmt.Errorf("projection watermark name required")
	}
	if appliedSeq < 0 {
		appliedSeq = 0
	}
	_, err := QExec(execable,
		`INSERT INTO derived_view_watermarks (view_name, applied_seq, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(view_name) DO UPDATE
		       SET applied_seq=CASE
		             WHEN derived_view_watermarks.applied_seq > excluded.applied_seq
		             THEN derived_view_watermarks.applied_seq
		             ELSE excluded.applied_seq
		           END,
		           updated_at=excluded.updated_at`,
		watermark, appliedSeq, updatedAt,
	)
	return err
}
