# krpdiff test fixtures

These fixtures exercise the WuWa krpdiff (HDiffPatch directory-diff) format.
`krpdiff` is **not** a single-file HDiffPatch diff — it is produced by hdiffz's
*directory* diff mode. A dir diff over an old/new directory tree that each
contain exactly one file still emits the `HDIFF19` (directory-diff) magic,
not the `HDIFF13` single-file magic, and only dir diff supports the `-C`
checksum flag in hdiffz v5.1.3 (single-file diff rejects `-C` outright).

## Tool

- `hdiffz` v5.1.3 (HDiffPatch), Windows x64 build (`hdiffz.exe` / `hpatchz.exe`
  from the official HDiffPatch releases). Tools are NOT committed to this repo
  — download them yourself to regenerate fixtures.

## Layout

Each fixture (`a`, `b`) is a directory diff between an `oldDir` and a `newDir`
that each contain a single file at the same relative path (mimicking a real
WuWa pak layout). This repo's testdata only keeps the bare file contents
(`old_a.bin`, `new_a.bin`, etc.) plus the resulting `.krpdiff`, not the
directory trees themselves. To reconstruct an `oldDir`/`newDir` for a test
(e.g. to run `hpatchz` against `a.krpdiff`), place the `.bin` file at the
relative path below:

| Fixture | old file  | new file  | relative path inside oldDir/newDir |
|---------|-----------|-----------|-------------------------------------|
| a       | `old_a.bin` | `new_a.bin` | `Client/Content/Paks/chunk_a.pak` |
| b       | `old_b.bin` | `new_b.bin` | `Client/Content/Paks/chunk_b.pak` |

i.e. for fixture `a`:

```
oldDir/Client/Content/Paks/chunk_a.pak   (= old_a.bin)
newDir/Client/Content/Paks/chunk_a.pak   (= new_a.bin)
```

## Regeneration

The `.bin` contents are random binary (~64KB base, with a modified region and
an appended tail in the "new" version so hdiffz has both matching and
non-matching runs to diff). Regenerating produces byte-different but
format-identical fixtures — that's fine, only the format/magic matters to
the tests that consume these files.

Commands used (Git Bash / MSYS on Windows, `/dev/urandom` available):

```bash
# --- fixture a ---
mkdir -p a_old/Client/Content/Paks a_new/Client/Content/Paks
head -c 65536 /dev/urandom > a_old/Client/Content/Paks/chunk_a.pak
cp a_old/Client/Content/Paks/chunk_a.pak a_new/Client/Content/Paks/chunk_a.pak
# modify a 2000-byte region starting at offset 30000
dd if=/dev/urandom of=a_new/Client/Content/Paks/chunk_a.pak bs=1 seek=30000 count=2000 conv=notrunc status=none
# append 2000 bytes to the tail (old=65536B, new=67536B)
head -c 2000 /dev/urandom >> a_new/Client/Content/Paks/chunk_a.pak

# --- fixture b ---
mkdir -p b_old/Client/Content/Paks b_new/Client/Content/Paks
head -c 65536 /dev/urandom > b_old/Client/Content/Paks/chunk_b.pak
cp b_old/Client/Content/Paks/chunk_b.pak b_new/Client/Content/Paks/chunk_b.pak
# modify a 3000-byte region starting at offset 1000
dd if=/dev/urandom of=b_new/Client/Content/Paks/chunk_b.pak bs=1 seek=1000 count=3000 conv=notrunc status=none
# append 1500 bytes to the tail (old=65536B, new=67036B)
head -c 1500 /dev/urandom >> b_new/Client/Content/Paks/chunk_b.pak

# --- dir diff (hdiffz v5.1.3) ---
# NOTE: uppercase -C-fadler64; the long form "-checksum-fadler64" does NOT
# exist in v5.1.3.
hdiffz -c-zstd -C-fadler64 a_old a_new a.krpdiff
hdiffz -c-zstd -C-fadler64 b_old b_new b.krpdiff

# --- collect into testdata (this directory) ---
cp a_old/Client/Content/Paks/chunk_a.pak old_a.bin
cp a_new/Client/Content/Paks/chunk_a.pak new_a.bin
cp b_old/Client/Content/Paks/chunk_b.pak old_b.bin
cp b_new/Client/Content/Paks/chunk_b.pak new_b.bin
# a.krpdiff / b.krpdiff copied as-is
```

## Verification performed at generation time (not committed as a test)

`hpatchz` roundtrip sanity check, confirming `a.krpdiff`/`b.krpdiff` applied
to `old_a.bin`/`old_b.bin` (placed at the relative paths above, inside an
`oldDir`) reproduce `new_a.bin`/`new_b.bin` byte-for-byte:

```bash
hpatchz a_old a.krpdiff a_out   # "patch ok!"
diff -q a_new/Client/Content/Paks/chunk_a.pak a_out/Client/Content/Paks/chunk_a.pak   # MATCH

hpatchz b_old b.krpdiff b_out   # "patch ok!"
diff -q b_new/Client/Content/Paks/chunk_b.pak b_out/Client/Content/Paks/chunk_b.pak   # MATCH
```

## Format note (magic bytes)

Both `a.krpdiff` and `b.krpdiff` start with the literal byte sequence:

```
HDIFF19&zstd&fadler64
```

`update_krpdiff_fixture_test.go` asserts this prefix byte-for-byte to catch
format drift (e.g. if a future hdiffz version or different compress/checksum
flag combination changes the header).
