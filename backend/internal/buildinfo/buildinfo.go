// Package buildinfo carries the version stamped into the binary at link time.
//
// It exists as its own package so the handful of consumers (the health
// endpoint, the MCP server identity, the startup log) can read it directly
// rather than having it threaded through main -> server.New -> every handler.
package buildinfo

// Version is set at link time with
//
//	-ldflags "-X github.com/shawn-bluce/renderbin/backend/internal/buildinfo.Version=v1.2.3"
//
// Release builds pass the Git tag; `make build` passes `git describe`. It
// stays "dev" for a plain `go build`, which is the honest answer.
//
// We can't use runtime/debug.ReadBuildInfo's VCS stamping instead: that needs
// .git in the build context, and .dockerignore excludes it on purpose.
var Version = "dev"
