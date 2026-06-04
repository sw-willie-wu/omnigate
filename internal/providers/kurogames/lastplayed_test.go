package kurogames

import (
	"path/filepath"
	"testing"
)

func TestLastPlayedFiles_Kurogames(t *testing.T) {
	p := &Provider{}
	dir := filepath.Join("C:\\", "Games", "Wuthering Waves Game")
	files := p.LastPlayedFiles("kurogames/wutheringwaves", dir)
	want := filepath.Join(dir, "Client", "Saved", "Logs", "Client.log")
	if len(files) != 1 || files[0] != want {
		t.Fatalf("got %v, want [%s]", files, want)
	}
}

func TestLastPlayedFiles_Kurogames_NoInstallDir(t *testing.T) {
	p := &Provider{}
	if got := p.LastPlayedFiles("kurogames/wutheringwaves", ""); got != nil {
		t.Errorf("empty installDir: want nil, got %v", got)
	}
}

func TestLastPlayedFiles_Kurogames_UnknownGID(t *testing.T) {
	p := &Provider{}
	if got := p.LastPlayedFiles("kurogames/unknown", `C:\Games\X`); got != nil {
		t.Errorf("unknown gid: want nil, got %v", got)
	}
}
