package license

// Features are the paid capabilities a licence can unlock. The open core never
// checks a server count: a feature is either built into the official Pro
// module and unlocked by a signed token, or it is not there at all.
const (
	FeatureS3Backup   = "s3-backup"
	FeatureRollback   = "update-rollback"
	FeatureMultiAdmin = "multi-admin"
	FeatureStatusPage = "status-page"
	FeatureMods       = "mod-manager"
	FeatureDiscord    = "discord"
	FeatureAPI        = "api"
	FeatureMultiNode  = "multi-node"
)

var pro = []string{FeatureS3Backup, FeatureRollback, FeatureMultiAdmin, FeatureStatusPage, FeatureMods, FeatureDiscord, FeatureAPI}

// Plans and what they include. The server picks the features when it issues a
// licence, so adding a feature to a plan later needs no new panel release for
// features the Pro module already contains.
var plans = map[string][]string{
	"pro":     pro,
	"hosting": append(append([]string{}, pro...), FeatureMultiNode),
}

// PlanFeatures returns a copy of the features of a plan, or nil if unknown.
func PlanFeatures(plan string) []string {
	f, ok := plans[plan]
	if !ok {
		return nil
	}
	return append([]string(nil), f...)
}

func KnownPlan(plan string) bool { _, ok := plans[plan]; return ok }

// ValidFeature reports whether a name is one this version knows. Panels ignore
// unknown names in a token, so a newer server can issue newer features.
func ValidFeature(f string) bool {
	for _, p := range plans["hosting"] {
		if p == f {
			return true
		}
	}
	return false
}

// Has reports whether the claims include a feature.
func (c Claims) Has(feature string) bool {
	for _, f := range c.Features {
		if f == feature {
			return true
		}
	}
	return false
}
