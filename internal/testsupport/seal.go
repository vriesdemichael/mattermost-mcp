// Package testsupport holds what unit tests share.
package testsupport

import (
	"os"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/config"
)

// Seal empties every variable mm-mcp reads and turns the network block on
// (ADR-006), so neither the developer's shell nor a CI runner decides what a
// unit test sees, and no test reaches beyond the machine. A test that needs a
// value passes it, through a Getenv it hands to the code, and never publishes
// it to the process.
func Seal() {
	for _, name := range config.EnvironmentVariables {
		_ = os.Setenv(name, "")
	}
	_ = os.Setenv(config.EnvBlockExternalNetwork, "1")
}

// SealedMain is the TestMain of every unit test package: seal, then run.
// TestEveryTestPackageIsSealed fails a package that has tests and lacks it.
func SealedMain(m *testing.M) {
	Seal()
	os.Exit(m.Run())
}

// Env is a Getenv over a fixed set of values, for code that reads its
// configuration through one.
func Env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
