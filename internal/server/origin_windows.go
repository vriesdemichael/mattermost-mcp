package server

import (
	"fmt"
	"os"
)

// markDownloaded gives a saved file the Mark of the Web, the Zone.Identifier
// stream a browser writes, so Windows treats it as from the internet:
// SmartScreen checks a program before it runs, and Office opens a document in
// Protected View, as for a file the person downloaded themselves.
func markDownloaded(path, referrer, host string) error {
	zone := fmt.Sprintf("[ZoneTransfer]\r\nZoneId=3\r\nReferrerUrl=%s\r\nHostUrl=%s\r\n", referrer, host)
	return os.WriteFile(path+":Zone.Identifier", []byte(zone), 0o600) //nolint:gosec // the stream of a file save_file just wrote
}
