package hoyoverse

import (
	"path/filepath"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestLastPlayedFiles_Hoyoverse(t *testing.T) {
	p := &Provider{}
	cases := map[core.GameID]string{
		"hoyoverse/genshin":  filepath.Join("miHoYo", "Genshin Impact"),
		"hoyoverse/starrail": filepath.Join("Cognosphere", "Star Rail"),
		"hoyoverse/zzz":      filepath.Join("miHoYo", "ZenlessZoneZero"),
	}
	for gid, sub := range cases {
		files := p.LastPlayedFiles(gid, "")
		if len(files) != 2 {
			t.Fatalf("%s: want 2 candidates, got %d: %v", gid, len(files), files)
		}
		wantLog := filepath.Join("AppData", "LocalLow", sub, "output_log.txt")
		wantPlayer := filepath.Join("AppData", "LocalLow", sub, "Player.log")
		if !strings.HasSuffix(files[0], wantLog) {
			t.Errorf("%s: candidate[0]=%q want suffix %q", gid, files[0], wantLog)
		}
		if !strings.HasSuffix(files[1], wantPlayer) {
			t.Errorf("%s: candidate[1]=%q want suffix %q", gid, files[1], wantPlayer)
		}
	}
}

func TestLastPlayedFiles_Hoyoverse_UnknownGID(t *testing.T) {
	p := &Provider{}
	if got := p.LastPlayedFiles("hoyoverse/unknown", ""); got != nil {
		t.Errorf("unknown gid: want nil, got %v", got)
	}
}
