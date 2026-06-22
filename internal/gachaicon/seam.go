package gachaicon

import "omnigate/internal/core"

// Test seams (used by package app tests). Kept in a non-_test.go file so they are
// visible across packages while the underlying methods/fields stay unexported.
func (x *Index) PutForTest(gid core.GameID, name string, e Entry) { x.put(gid, name, e) }
func (m *Manager) SwapForTest(gid core.GameID, idx *Index)        { m.swap(gid, idx) }
func (m *Manager) SetURLFnForTest(fn func(core.GameID, string, string) string) {
	m.urlFn = fn
}
