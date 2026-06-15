package core

import (
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/policy"
)

// RegistrationIntake holds the optional private signup fields plus the
// privacy-policy acceptance recorded at account creation. All free-text fields
// are optional; they are stored in user_private_profiles, never in public state.
type RegistrationIntake struct {
	RealName       string
	Affiliation    string // stored in the `school` column
	Note           string // reason for joining / contact note
	PolicyAccepted bool
	PolicyVersion  string
}

// SaveRegistrationIntake persists the private signup fields and, when accepted,
// stamps the privacy-policy acceptance time and version. It upserts only the
// intake-owned columns, leaving registration_email to the verification flow.
func (c *Core) SaveRegistrationIntake(userID string, in RegistrationIntake) error {
	realName := strings.TrimSpace(in.RealName)
	affiliation := strings.TrimSpace(in.Affiliation)
	note := strings.TrimSpace(in.Note)
	now := nowMS()
	var acceptedAt int64
	version := ""
	if in.PolicyAccepted {
		acceptedAt = now
		version = strings.TrimSpace(in.PolicyVersion)
	}
	_, err := qExec(c.DB,
		`INSERT INTO user_private_profiles (user_id, real_name, school, contact_note, policy_accepted_at, policy_version, updated_at)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   real_name=excluded.real_name,
		   school=excluded.school,
		   contact_note=excluded.contact_note,
		   policy_accepted_at=excluded.policy_accepted_at,
		   policy_version=excluded.policy_version,
		   updated_at=excluded.updated_at`,
		userID, realName, affiliation, note, acceptedAt, version, now,
	)
	return err
}

// SetPrivacyPolicy enables or disables mandatory privacy-policy acceptance at
// signup. Off by default; budgied turns it on per the -require-policy-acceptance
// flag.
func (c *Core) SetPrivacyPolicy(required bool) {
	c.privacyPolicyRequired = required
}

// PrivacyPolicyRequired reports whether signup must record policy acceptance.
func (c *Core) PrivacyPolicyRequired() bool {
	return c != nil && c.privacyPolicyRequired
}

// PrivacyPolicy returns the bundled policy markdown and its version identifier.
func (c *Core) PrivacyPolicy() (markdown, version string) {
	return policy.DefaultPrivacyPolicy()
}
