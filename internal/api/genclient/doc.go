// Package genclient holds the generated typed Go client for the Gas City
// API. It is produced by `cmd/gen-client` from the live OpenAPI 3.0
// downgrade of the server's spec, processed through oapi-codegen v2.6.0.
//
// See engdocs/architecture/api-control-plane.md §2 "The generated Go client" for the
// three legitimate in-tree consumers of this package
// (internal/api/client.go for CLI mutation coordination,
// cmd/gc/cmd_events.go for direct event read/stream access, and
// genclient_roundtrip_test.go as a Layer 2 conformance probe) and
// for why it is not promoted as a public Go SDK.
//
// Regeneration:
//
//	go generate ./internal/api/genclient
//
// CI runs the same regen and diffs against the committed file (see
// TestGeneratedClientInSync in this package). If the generated client
// differs from the committed file, CI fails — keep the spec, the
// generator, and the committed client in lock-step.
package genclient

// Invokes the wrapper script rather than a bare `go run ... > ...`
// because the shell redirect zeroes the target file BEFORE `go run`
// compiles, and the compile step reads this package — so
// `client_gen.go` is empty at read time and the build fails before
// producing any output. The script writes to a temp file and renames.
//
// Run through `bash` explicitly rather than relying on the shebang: go generate
// fork/execs the directive directly, and Windows has no shebang handling, so a
// bare script path fails with "%1 is not a valid Win32 application" and blocks
// the pre-commit regeneration.
//
// `bash`, not `sh`, so this keeps honoring the script's own `#!/usr/bin/env bash`
// shebang. On Debian and Ubuntu /bin/sh is dash, so running it as `sh` would
// quietly change the interpreter on exactly the platforms CI uses, and any
// bashism the script grows later would fail there instead of here.
//go:generate bash ../../../scripts/gen-client.sh
