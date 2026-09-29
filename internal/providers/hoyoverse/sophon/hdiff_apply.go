package sophon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// hdiffMagic is the leading bytes of every hdiffpatch diff ("HDIFF13&…"
// single-file compressed, "HDIFF13" plain; also "HDIFF19" dir-diff which
// hpatchz cannot apply — real Sophon copy_over slices are 349/349 HDIFF13&).
// copy_over slices from the Sophon patch CDN are hdiffs against an EMPTY old
// file (oldSize==0), not raw files — detect by prefix, byte-identical to
// Collapse's IsChunkActuallyHDiff ("HDIFF"u8).
const hdiffMagic = "HDIFF"

// IsHDiff reports whether b starts with the hdiffpatch magic.
func IsHDiff(b []byte) bool { return bytes.HasPrefix(b, []byte(hdiffMagic)) }

// HDiffOpts carries everything HDiffApply needs to materialise one target file.
// Run is the hpatchz binary runner injected by the parent hoyoverse package
// (sophon must not import hoyoverse/hpatchz directly here — the parent wires it).
type HDiffOpts struct {
	Ctx       context.Context
	Run       func(ctx context.Context, oldFile, diffFile, newFile string) error
	Method    string // MethodPatch | MethodCopyOver
	OldFile   string // MethodPatch: source file (absolute); "" is allowed and means an empty old file (hpatchz semantics)
	DiffInput string // MethodPatch: extracted hdiff slice path
	BlobSlice string // MethodCopyOver: extracted blob slice path (becomes the file)
	OutTmp    string
}

// HDiffApply produces OutTmp from opts. MethodPatch runs the injected hpatchz
// against (OldFile, DiffInput) → OutTmp — OldFile is passed through verbatim, so
// "" reaches hpatchz as an empty argument and means "patch against an empty old
// file", which is what Sophon copy_over hdiff slices (oldSize==0) need.
// MethodCopyOver renames the already-extracted blob slice into OutTmp. It does
// NOT verify the resulting whole-file MD5 — the caller does (spec §6.4/§6.5).
func HDiffApply(opts HDiffOpts) error {
	if err := os.MkdirAll(filepath.Dir(opts.OutTmp), 0o755); err != nil {
		return err
	}
	switch opts.Method {
	case MethodPatch:
		if opts.Run == nil {
			return fmt.Errorf("sophon: HDiffApply MethodPatch requires opts.Run")
		}
		return opts.Run(opts.Ctx, opts.OldFile, opts.DiffInput, opts.OutTmp)
	case MethodCopyOver:
		return SafeAtomicRename(opts.BlobSlice, opts.OutTmp)
	default:
		return fmt.Errorf("sophon: HDiffApply unknown method %q", opts.Method)
	}
}
