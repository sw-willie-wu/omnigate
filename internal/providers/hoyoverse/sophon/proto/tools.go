//go:build tools
// +build tools

// Package proto's tools.go pins protoc-gen-go so `go install` (run from this
// dir) picks the right version for regenerating the committed *.pb.go files.
// Never built by application code (the `tools` build tag is never set).
package proto

import (
	_ "google.golang.org/protobuf/cmd/protoc-gen-go"
)
