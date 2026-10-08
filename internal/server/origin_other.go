//go:build !windows && !darwin

package server

import "golang.org/x/sys/unix"

// markDownloaded records where a saved file came from in the attribute the
// freedesktop.org conventions name, which file managers show. Linux has no
// quarantine to put it under.
func markDownloaded(path, referrer, _ string) error {
	if referrer == "" {
		return nil
	}
	return unix.Setxattr(path, "user.xdg.origin.url", []byte(referrer), 0)
}
