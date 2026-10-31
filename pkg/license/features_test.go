package license

import "testing"

func TestPlansAndFeatures(t *testing.T) {
	pro := PlanFeatures("pro")
	host := PlanFeatures("hosting")
	if len(pro) == 0 || len(host) != len(pro)+1 {
		t.Fatalf("pro=%v hosting=%v", pro, host)
	}
	if PlanFeatures("free") != nil || KnownPlan("free") || KnownPlan("") {
		t.Error("Free is not a licensed plan")
	}
	for _, f := range pro {
		if f == FeatureMultiNode {
			t.Error("multi-node belongs to Hosting only")
		}
	}
	// the returned slice is a copy: changing it must not change the plan
	got := PlanFeatures("pro")
	got[0] = "tampered"
	if PlanFeatures("pro")[0] == "tampered" {
		t.Error("PlanFeatures returned the internal slice")
	}
	for _, f := range host {
		if !ValidFeature(f) {
			t.Errorf("%s should be valid", f)
		}
	}
	if ValidFeature("") || ValidFeature("s3") || ValidFeature("PRO") {
		t.Error("unknown names must not be valid")
	}
	c := Claims{Features: []string{FeatureS3Backup}}
	if !c.Has(FeatureS3Backup) || c.Has(FeatureAPI) || (Claims{}).Has(FeatureAPI) {
		t.Error("Has")
	}
}
