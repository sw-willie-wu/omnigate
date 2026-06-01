package hoyoverse

import (
	"encoding/json"
	"testing"
)

// FuzzAppliedJSON_Serde: arbitrary JSON into appliedManifestSet (§A.6) must
// never panic on Unmarshal; valid round-trips must re-marshal cleanly.
func FuzzAppliedJSON_Serde(f *testing.F) {
	f.Add([]byte(`{"latest":{"build_id":"b","version":"6.6.0","applied_at":"2026-06-01T00:00:00Z","categories":{"game":"b"}},"previous":null}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"latest":{}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var s appliedManifestSet
		if err := json.Unmarshal(data, &s); err != nil {
			return // malformed JSON is fine; no panic is the invariant
		}
		if _, err := json.Marshal(&s); err != nil {
			t.Fatalf("re-marshal of accepted struct failed: %v", err)
		}
	})
}

// FuzzSophonApplyWAL_Roundtrip: random records marshal/unmarshal preserve shape.
func FuzzSophonApplyWAL_Roundtrip(f *testing.F) {
	f.Add([]byte(`{"game_id":"g","target_tag":"6.6.0","build_id":"b","flavor":"sophon_full","branch_kind":"main","records":[{"kind":"chunk_assemble","path":"data/x.bin","state":"pending"}]}`))
	f.Add([]byte(`{"records":[]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var w sophonApplyWAL
		if err := json.Unmarshal(data, &w); err != nil {
			return
		}
		b1, err := json.Marshal(&w)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var w2 sophonApplyWAL
		if err := json.Unmarshal(b1, &w2); err != nil {
			t.Fatalf("re-unmarshal: %v", err)
		}
		if len(w2.Records) != len(w.Records) {
			t.Fatalf("record count drift: %d -> %d", len(w.Records), len(w2.Records))
		}
	})
}
