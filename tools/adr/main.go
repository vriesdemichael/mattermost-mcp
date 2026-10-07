// Command adr validates the decision records and generates their index.
//
//	go run ./tools/adr validate
//	go run ./tools/adr index          # rewrite docs/site/adr/index.md
//	go run ./tools/adr index -check   # fail when the committed index is stale
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vriesdemichael/mm-mcp/internal/adr"
)

var dir = filepath.Join("docs", "site", "adr")

func main() {
	if len(os.Args) < 2 || (os.Args[1] != "validate" && os.Args[1] != "index") {
		fail("usage: adr validate | adr index [-check]")
	}
	flags := flag.NewFlagSet("adr "+os.Args[1], flag.ExitOnError)
	check := flags.Bool("check", false, "fail when the committed index is stale")
	_ = flags.Parse(os.Args[2:])

	records, err := adr.LoadAll(dir)
	if err != nil {
		fail(err.Error())
	}
	if os.Args[1] == "validate" {
		fmt.Printf("%d decision records are well formed.\n", len(records))
		return
	}
	index := filepath.Join(dir, adr.IndexName)
	rendered := adr.RenderIndex(records)
	if *check {
		current, _ := os.ReadFile(index) //nolint:gosec // a fixed path in the repository
		if string(current) != rendered {
			fail(index + " is stale; run `task docs:adr-index`")
		}
		fmt.Println("The decision record index is current.")
		return
	}
	if err := os.WriteFile(index, []byte(rendered), 0o600); err != nil {
		fail(err.Error())
	}
	fmt.Printf("Wrote %s with %d records.\n", index, len(records))
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
