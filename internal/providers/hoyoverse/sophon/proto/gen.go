package proto

// Regenerate the committed *.pb.go from the *.proto schemas. Requires protoc
// (system binary) plus protoc-gen-go on PATH:
//
//   cd internal/providers/hoyoverse/sophon/proto
//   go install google.golang.org/protobuf/cmd/protoc-gen-go
//   go generate ./...
//
//go:generate protoc --go_out=. --go_opt=paths=source_relative sophon_manifest.proto sophon_patch.proto
