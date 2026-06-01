package sophon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHDiffApply_PatchBranchInvokesRun(t *testing.T) {
	dir := t.TempDir()
	outTmp := filepath.Join(dir, "assembled", "file.dat.tmp")
	var gotOld, gotDiff, gotOut string
	opts := HDiffOpts{
		Ctx:       context.Background(),
		Method:    MethodPatch,
		OldFile:   "/game/old.dat",
		DiffInput: "/staging/diff.bin",
		OutTmp:    outTmp,
		Run: func(ctx context.Context, oldFile, diffFile, newFile string) error {
			gotOld, gotDiff, gotOut = oldFile, diffFile, newFile
			// hpatchz would write newFile; emulate so the dir-creation contract is observable.
			return os.WriteFile(newFile, []byte("patched"), 0o644)
		},
	}
	if err := HDiffApply(opts); err != nil {
		t.Fatalf("HDiffApply: %v", err)
	}
	if gotOld != "/game/old.dat" || gotDiff != "/staging/diff.bin" || gotOut != outTmp {
		t.Fatalf("Run received (%q,%q,%q)", gotOld, gotDiff, gotOut)
	}
	if _, err := os.Stat(filepath.Dir(outTmp)); err != nil {
		t.Fatalf("OutTmp dir should have been created: %v", err)
	}
}

func TestHDiffApply_CopyOverRenamesBlobSlice(t *testing.T) {
	dir := t.TempDir()
	blob := filepath.Join(dir, "slice.bin")
	if err := os.WriteFile(blob, []byte("full file via copyover"), 0o644); err != nil {
		t.Fatal(err)
	}
	outTmp := filepath.Join(dir, "assembled", "file.dat.tmp")
	opts := HDiffOpts{Method: MethodCopyOver, BlobSlice: blob, OutTmp: outTmp}
	if err := HDiffApply(opts); err != nil {
		t.Fatalf("HDiffApply: %v", err)
	}
	got, _ := os.ReadFile(outTmp)
	if string(got) != "full file via copyover" {
		t.Fatalf("copy_over content mismatch: %q", got)
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Fatalf("blob slice should be gone after rename")
	}
}

func TestHDiffApply_UnknownMethod(t *testing.T) {
	err := HDiffApply(HDiffOpts{Method: "bogus", OutTmp: filepath.Join(t.TempDir(), "x.tmp")})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown-method error mentioning the method, got %v", err)
	}
}
