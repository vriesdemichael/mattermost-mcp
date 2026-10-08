//go:build live && darwin

package live

import "golang.org/x/sys/unix"

// downloadMark is the quarantine attribute of a saved file.
func downloadMark(path string) (string, error) {
	buffer := make([]byte, 1024)
	n, err := unix.Getxattr(path, "com.apple.quarantine", buffer)
	if err != nil {
		return "", err
	}
	return string(buffer[:n]), nil
}
