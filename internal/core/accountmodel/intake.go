package accountmodel

import "strings"

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

type NormalizedRegistrationIntake struct {
	RealName         string
	Affiliation      string
	Note             string
	PolicyAcceptedAt int64
	PolicyVersion    string
}

func NormalizeRegistrationIntake(in RegistrationIntake, acceptedAt int64) NormalizedRegistrationIntake {
	out := NormalizedRegistrationIntake{
		RealName:    strings.TrimSpace(in.RealName),
		Affiliation: strings.TrimSpace(in.Affiliation),
		Note:        strings.TrimSpace(in.Note),
	}
	if in.PolicyAccepted {
		out.PolicyAcceptedAt = acceptedAt
		out.PolicyVersion = strings.TrimSpace(in.PolicyVersion)
	}
	return out
}
