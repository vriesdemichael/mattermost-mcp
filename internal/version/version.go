// Package version holds the release mm-mcp reports.
package version

// Version is the release this build is. The release build sets it with
// -ldflags "-X github.com/vriesdemichael/mm-mcp/internal/version.Version=vX.Y.Z"
// (ADR-013); any other build reports "dev".
var Version = "dev"

// UserAgent is how every request mm-mcp sends to Mattermost names it, so an
// administrator can tell them apart in their logs.
func UserAgent() string { return "mm-mcp/" + Version }
