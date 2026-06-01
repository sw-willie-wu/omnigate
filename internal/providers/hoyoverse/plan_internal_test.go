package hoyoverse

import "testing"

func TestSophonFlavorStrings(t *testing.T) {
	cases := []struct {
		f    planFlavor
		want string
	}{
		{flavorSophonPatch, "sophon_patch"},
		{flavorSophonBuild, "sophon_build"},
		{flavorSophonFull, "sophon_full"},
		{flavorSophonPredlPatch, "sophon_predl_patch"},
		{flavorSophonPredlBuild, "sophon_predl_build"},
	}
	for _, c := range cases {
		if got := c.f.String(); got != c.want {
			t.Errorf("%d.String() = %q, want %q", int(c.f), got, c.want)
		}
	}
}

func TestSophonFlavorValues(t *testing.T) {
	// Must NOT renumber the v1 constants (§A.5).
	if flavorSophonPatch != 6 {
		t.Errorf("flavorSophonPatch = %d, want 6", int(flavorSophonPatch))
	}
	if flavorSophonPredlBuild != 10 {
		t.Errorf("flavorSophonPredlBuild = %d, want 10", int(flavorSophonPredlBuild))
	}
}

func TestPredlPlanCacheFieldsCompile(t *testing.T) {
	// Compile-only assertion that the predl cache + genshinPlan Sophon fields exist.
	var gp genshinPlan
	gp.sophonBuildID = "b"
	gp.predlConsume = true
	pc := &predlPlanCache{Flavor: flavorSophonPredlPatch, BuildID: "b"}
	gp.predlPlan = pc
	if gp.predlPlan.BuildID != "b" {
		t.Fatal("predlPlanCache not wired")
	}
	// OVERRIDE A: exercise the new sophonRawManifests map field
	gp.sophonRawManifests = map[string][]byte{"game": {1, 2, 3}}
	if len(gp.sophonRawManifests["game"]) != 3 {
		t.Errorf("sophonRawManifests not set correctly, got %d bytes, want 3", len(gp.sophonRawManifests["game"]))
	}
}

// OVERRIDE B: new test for planFlavorFromString
func TestPlanFlavorFromString(t *testing.T) {
	cases := map[string]planFlavor{
		"sophon_patch":       flavorSophonPatch,
		"sophon_build":       flavorSophonBuild,
		"sophon_full":        flavorSophonFull,
		"sophon_predl_patch": flavorSophonPredlPatch,
		"sophon_predl_build": flavorSophonPredlBuild,
		"garbage":            flavorNone,
		"":                   flavorNone,
	}
	for in, want := range cases {
		if got := planFlavorFromString(in); got != want {
			t.Errorf("planFlavorFromString(%q) = %v, want %v", in, got, want)
		}
	}
}
