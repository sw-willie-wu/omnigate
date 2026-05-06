package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReasonCode_Constants(t *testing.T) {
	cases := []struct {
		got, want ReasonCode
	}{
		{ReasonUnspecified, ""},
		{ReasonVersionChanged, "version_changed"},
		{ReasonAudioPackAdded, "audio_pack_added"},
		{ReasonVersionAndAudio, "version_and_audio"},
		{ReasonPredownload, "predownload"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("ReasonCode mismatch: got %q want %q", c.got, c.want)
		}
	}
}

func TestUpdatePlan_Reason_OmitemptyJSON(t *testing.T) {
	// Unspecified reason (zero value "") must not appear in JSON output.
	p := UpdatePlan{Version: "1.0.0", Kind: PlanUpdate}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	if strings.Contains(out, `"reason"`) {
		t.Errorf("zero-value Reason should be omitted; got %s", out)
	}
}

func TestUpdatePlan_Reason_PopulatedJSON(t *testing.T) {
	p := UpdatePlan{Version: "1.0.0", Kind: PlanUpdate, Reason: ReasonVersionChanged}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, `"reason":"version_changed"`) {
		t.Errorf("populated Reason should serialize; got %s", out)
	}
}
