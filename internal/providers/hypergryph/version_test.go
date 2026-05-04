package hypergryph

import (
	"context"
	"testing"

	"launcher-collection-tmp/internal/core"
)

func TestFetchVersion_AlwaysEmpty_M2Limitation(t *testing.T) {
	v, err := fetchVersion(context.Background(), "any/path", core.GameID("hypergryph/endfield"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if v.Current != "" || v.Latest != "" {
		t.Errorf("got VersionInfo %+v, want empty (no clean version source for hypergryph in M2)", v)
	}
	if v.Predownload != nil {
		t.Errorf("Predownload = %v, want nil", v.Predownload)
	}
}
