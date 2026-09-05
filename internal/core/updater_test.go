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

func TestUpdatePlan_PatchFieldsRoundTrip(t *testing.T) {
	// Test JSON round-trip with patch fields and ephemeral task
	original := UpdatePlan{
		GameID:       "wuwa",
		Kind:         PlanUpdate,
		ManifestETag: "abc123",
		Version:      "2.0.0",
		Reason:       ReasonVersionChanged,
		Files: []FileTask{
			{
				Path:      "game/data.pak",
				Hash:      "def456",
				Size:      1000000,
				URL:       "https://cdn.example.com/data.pak",
				Ephemeral: true,
			},
		},
		TotalBytes: 1000000,
		PatchGroups: []PatchGroup{
			{
				DiffPath: "patches/game_data.diff",
				Src: PatchFile{
					Path: "game/old_data.pak",
					Hash: "oldabcd",
					Size: 900000,
				},
				Dst: PatchFile{
					Path: "game/new_data.pak",
					Hash: "newabcd",
					Size: 950000,
				},
			},
		},
		DeleteFiles:   []string{"game/legacy.dat", "game/temp.tmp"},
		PeakTempBytes: 2000000,
	}

	// Marshal
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Unmarshal
	var restored UpdatePlan
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify all fields match
	if restored.GameID != original.GameID {
		t.Errorf("GameID mismatch: got %q, want %q", restored.GameID, original.GameID)
	}
	if restored.Kind != original.Kind {
		t.Errorf("Kind mismatch: got %q, want %q", restored.Kind, original.Kind)
	}
	if restored.Version != original.Version {
		t.Errorf("Version mismatch: got %q, want %q", restored.Version, original.Version)
	}
	if restored.Reason != original.Reason {
		t.Errorf("Reason mismatch: got %q, want %q", restored.Reason, original.Reason)
	}
	if restored.TotalBytes != original.TotalBytes {
		t.Errorf("TotalBytes mismatch: got %d, want %d", restored.TotalBytes, original.TotalBytes)
	}
	if len(restored.Files) != len(original.Files) {
		t.Errorf("Files length mismatch: got %d, want %d", len(restored.Files), len(original.Files))
	} else if len(restored.Files) > 0 {
		if restored.Files[0].Ephemeral != original.Files[0].Ephemeral {
			t.Errorf("Files[0].Ephemeral mismatch: got %v, want %v", restored.Files[0].Ephemeral, original.Files[0].Ephemeral)
		}
	}
	if restored.PeakTempBytes != original.PeakTempBytes {
		t.Errorf("PeakTempBytes mismatch: got %d, want %d", restored.PeakTempBytes, original.PeakTempBytes)
	}
	if len(restored.DeleteFiles) != len(original.DeleteFiles) {
		t.Errorf("DeleteFiles length mismatch: got %d, want %d", len(restored.DeleteFiles), len(original.DeleteFiles))
	} else {
		for i, v := range restored.DeleteFiles {
			if v != original.DeleteFiles[i] {
				t.Errorf("DeleteFiles[%d] mismatch: got %q, want %q", i, v, original.DeleteFiles[i])
			}
		}
	}
	if len(restored.PatchGroups) != len(original.PatchGroups) {
		t.Errorf("PatchGroups length mismatch: got %d, want %d", len(restored.PatchGroups), len(original.PatchGroups))
	} else if len(restored.PatchGroups) > 0 {
		pg := restored.PatchGroups[0]
		orig := original.PatchGroups[0]
		if pg.DiffPath != orig.DiffPath {
			t.Errorf("PatchGroups[0].DiffPath mismatch: got %q, want %q", pg.DiffPath, orig.DiffPath)
		}
		if pg.Src.Path != orig.Src.Path || pg.Src.Hash != orig.Src.Hash || pg.Src.Size != orig.Src.Size {
			t.Errorf("PatchGroups[0].Src mismatch: got %+v, want %+v", pg.Src, orig.Src)
		}
		if pg.Dst.Path != orig.Dst.Path || pg.Dst.Hash != orig.Dst.Hash || pg.Dst.Size != orig.Dst.Size {
			t.Errorf("PatchGroups[0].Dst mismatch: got %+v, want %+v", pg.Dst, orig.Dst)
		}
	}

	// Verify zero-value plan omits patch fields
	zeroplan := UpdatePlan{Version: "1.0.0", Kind: PlanUpdate}
	b2, err := json.Marshal(zeroplan)
	if err != nil {
		t.Fatalf("marshal zero-value: %v", err)
	}
	out := string(b2)
	if strings.Contains(out, `"patch_groups"`) {
		t.Errorf("zero-value patch_groups should be omitted by omitempty; got %s", out)
	}
	if strings.Contains(out, `"delete_files"`) {
		t.Errorf("zero-value delete_files should be omitted by omitempty; got %s", out)
	}
	if strings.Contains(out, `"peak_temp_bytes"`) {
		t.Errorf("zero-value peak_temp_bytes should be omitted by omitempty; got %s", out)
	}
	if strings.Contains(out, `"ephemeral"`) {
		t.Errorf("zero-value ephemeral should be omitted by omitempty; got %s", out)
	}
}
