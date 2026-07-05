package accountstore

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/accountmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

func SaveRegistrationIntake(db *sql.DB, userID string, intake accountmodel.NormalizedRegistrationIntake, updatedAt int64) error {
	_, err := sqlstore.Exec(db,
		`INSERT INTO user_private_profiles (user_id, real_name, school, contact_note, policy_accepted_at, policy_version, updated_at)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   real_name=excluded.real_name,
		   school=excluded.school,
		   contact_note=excluded.contact_note,
		   policy_accepted_at=excluded.policy_accepted_at,
		   policy_version=excluded.policy_version,
		   updated_at=excluded.updated_at`,
		userID, intake.RealName, intake.Affiliation, intake.Note, intake.PolicyAcceptedAt, intake.PolicyVersion, updatedAt,
	)
	return err
}
