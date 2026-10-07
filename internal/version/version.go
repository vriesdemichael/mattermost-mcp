// Package version holds the release mm-mcp reports.
package version

// Version is the release this build is. The release build sets it with
// -ldflags "-X github.com/vriesdemichael/mattermost-mcp/internal/version.Version=vX.Y.Z"
// (ADR-013); any other build reports "dev".
var Version = "dev"
