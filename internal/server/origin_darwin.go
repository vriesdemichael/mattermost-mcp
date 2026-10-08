package server

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// markDownloaded gives a saved file the quarantine attribute a browser sets,
// so Gatekeeper checks it before it opens, as for a file the person downloaded
// themselves.
func markDownloaded(path, referrer, _ string) error {
	value := fmt.Sprintf("0081;%x;mm-mcp;", time.Now().Unix())
	if err := unix.Setxattr(path, "com.apple.quarantine", []byte(value), 0); err != nil {
		return err
	}
	if referrer != "" {
		_ = unix.Setxattr(path, "com.apple.metadata:kMDItemWhereFroms", []byte(referrer), 0)
	}
	return nil
}
