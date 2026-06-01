# hpatchz binary

This directory ships a pre-built `hpatchz.exe` from [sisong/HDiffPatch](https://github.com/sisong/HDiffPatch), used to apply HoYoverse delta patches in Omnigate's M3.B HoYoverse updater.

## Provenance

- **Upstream:** https://github.com/sisong/HDiffPatch
- **Release tag:** `v4.12.2`
- **Release zip name:** `hdiffpatch_v4.12.2_bin_windows64.zip`
- **Release zip URL:** `https://github.com/sisong/HDiffPatch/releases/download/v4.12.2/hdiffpatch_v4.12.2_bin_windows64.zip`
- **Release zip SHA-256:** `0cc6aef5c39b058cd568f621bce0783166bdd9ff4b5f1480237bbb2770f5cd9b`
- **`hpatchz.exe` SHA-256:** `d4ba353cea752216d6c2dc32669681b55bd50dee24a58b2cc41e9933d81e91ff`
- **`hpatchz.exe` size:** `481280` bytes (~470 KB)

## License

MIT License. See `LICENSE`.

## How Omnigate uses this binary

`internal/providers/hoyoverse/hpatchz.go` embeds this file via `go:embed`. At first use, Omnigate writes the binary to `<TEMP>/omnigate/hpatchz-<sha8>.exe` (where `<sha8>` is the first 8 hex chars of `sha256.Sum256(embeddedBytes)`) and invokes it via `exec.CommandContext`. The cache is shared across all backends and persists across runs; OS temp cleanup eventually removes orphans.

## Updating

To bump the binary version: replace `hpatchz.exe`, update this README's tag/SHA-256/size, run `go test ./internal/providers/hoyoverse/...` (the SHA-keyed cache filename auto-derives from the new bytes), and commit.
