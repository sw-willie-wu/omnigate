//go:build ignore

package main

import (
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// gen_large_manifest.go: regenerate testdata/large_manifest.json.gz for spec §7.9
// load benchmarks. Run: `go run testdata/gen_large_manifest.go` from
// internal/providers/kurogames/. Schema mirrors the indexFileRaw shape (Resource
// array of manifestFileRaw with Dest/MD5/Size).
func main() {
	type fileRaw struct {
		Dest string `json:"dest"`
		MD5  string `json:"md5"`
		Size int64  `json:"size"`
	}
	type indexFileRaw struct {
		Resource []fileRaw `json:"resource"`
	}

	mf := indexFileRaw{}
	for i := 0; i < 1000; i++ {
		h := md5.Sum([]byte(fmt.Sprintf("entry-%d", i)))
		mf.Resource = append(mf.Resource, fileRaw{
			Dest: fmt.Sprintf("Engine/Game/Bin/foo_%04d.dll", i),
			MD5:  hex.EncodeToString(h[:]),
			Size: int64(1024 + (i*7919)%(1024*1024)),
		})
	}

	body, err := json.Marshal(&mf)
	if err != nil {
		panic(err)
	}
	out, err := os.Create("testdata/large_manifest.json.gz")
	if err != nil {
		panic(err)
	}
	defer out.Close()
	gw := gzip.NewWriter(out)
	if _, err := gw.Write(body); err != nil {
		panic(err)
	}
	if err := gw.Close(); err != nil {
		panic(err)
	}
	fmt.Printf("wrote testdata/large_manifest.json.gz with %d entries\n", len(mf.Resource))
}
