package core

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/accountmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/accountstore"
	"github.com/juncoflockleader/budgie-bbs/internal/policy"
)

// SaveRegistrationIntake persists the private signup fields and, when accepted,
// stamps the privacy-policy acceptance time and version. It upserts only the
// intake-owned columns, leaving registration_email to the verification flow.
func (c *Core) SaveRegistrationIntake(userID string, in accountmodel.RegistrationIntake) error {
	now := nowMS()
	intake := accountmodel.NormalizeRegistrationIntake(in, now)
	return accountstore.SaveRegistrationIntake(c.DB, userID, intake, now)
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
