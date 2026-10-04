package app

import (
	"log/slog"
	"testing"

	"omnigate/internal/providers/kurogames"
)

func newTestAppWithStore(t *testing.T) *App {
	t.Helper()
	tmp := t.TempDir()
	orig := osTempDir
	osTempDir = func() string { return tmp }
	t.Cleanup(func() { osTempDir = orig })
	a := New(t.TempDir(), slog.Default())
	t.Cleanup(a.Close)
	return a
}

func TestConstructProviders_InjectsStoreKV(t *testing.T) {
	a := newTestAppWithStore(t)
	var kuro *kurogames.Provider
	for _, p := range a.providers {
		if k, ok := p.(*kurogames.Provider); ok {
			kuro = k
		}
	}
	if kuro == nil || !kuro.HasExternalKV() {
		t.Fatal("kurogames provider did not receive store KV")
	}
}
