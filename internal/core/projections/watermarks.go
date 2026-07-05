package projections

import (
	"database/sql"
	"fmt"
	"strings"
)

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
