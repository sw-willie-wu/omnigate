package hypergryph

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLastPlayedFiles_Hypergryph(t *testing.T) {
	p := &Provider{}
	files := p.LastPlayedFiles("hypergryph/endfield", "")
	if len(files) != 2 {
		t.Fatalf("want 2 candidates, got %d: %v", len(files), files)
	}
	wantPlayer := filepath.Join("AppData", "LocalLow", "Gryphline", "Endfield", "Player.log")
	wantLog := filepath.Join("AppData", "LocalLow", "Gryphline", "Endfield", "output_log.txt")
	if !strings.HasSuffix(files[0], wantPlayer) {
		t.Errorf("candidate[0]=%q want suffix %q", files[0], wantPlayer)
	}
	if !strings.HasSuffix(files[1], wantLog) {
		t.Errorf("candidate[1]=%q want suffix %q", files[1], wantLog)
	}
}

func TestLastPlayedFiles_Hypergryph_UnknownGID(t *testing.T) {
	p := &Provider{}
	if got := p.LastPlayedFiles("hypergryph/unknown", ""); got != nil {
		t.Errorf("unknown gid: want nil, got %v", got)
	}
}
