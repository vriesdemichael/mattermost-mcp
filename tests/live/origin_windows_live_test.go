//go:build live && windows

package live

import "os"

// downloadMark is the Zone.Identifier stream of a saved file.
func downloadMark(path string) (string, error) {
	mark, err := os.ReadFile(path + ":Zone.Identifier")
	return string(mark), err
}
