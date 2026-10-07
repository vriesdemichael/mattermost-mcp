package main

import (
	"slices"
	"testing"
)

func TestTheSourcesAreReadInTheOrderTheMakefileConcatenatesThem(t *testing.T) {
	t.Parallel()
	makefile := "build-v4:\n\t@cat $(V4_SRC)/users.yaml >> $(V4_YAML)\n\t@cat $(V4_SRC)/posts.yaml >> $(V4_YAML)\n"
	files, err := SourceOrder(makefile)
	if err != nil || !slices.Equal(files, []string{"introduction.yaml", "users.yaml", "posts.yaml"}) {
		t.Fatalf("got %v, %v", files, err)
	}
	if _, err := SourceOrder("build-v4:\n\techo nothing\n"); err == nil {
		t.Fatal("a Makefile that lists no sources was accepted")
	}
}

func TestAPathDefinedInTwoSourcesKeepsTheOperationsOfBoth(t *testing.T) {
	t.Parallel()
	source := `openapi: 3.0.0
info:
  title: t
paths:
  /api/v4/things:
    get:
      operationId: GetThings
      responses:
        200:
          description: ok
  /api/v4/other:
    get:
      operationId: GetOther
  /api/v4/things:
    post:
      operationId: CreateThing
`
	document, err := Decode(source, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	things := document["paths"].(map[string]any)["/api/v4/things"].(map[string]any)
	if things["get"] == nil || things["post"] == nil {
		t.Fatalf("lost an operation: %v", things)
	}
	responses := things["get"].(map[string]any)["responses"].(map[string]any)
	if _, ok := responses["200"]; !ok {
		t.Fatalf("a numeric response code did not become a string key: %v", responses)
	}
	if document["info"].(map[string]any)[releaseKey] != "1.2.3" {
		t.Fatalf("the release is not recorded: %v", document["info"])
	}
}

func TestADocumentWithoutPathsIsRefused(t *testing.T) {
	t.Parallel()
	if _, err := Decode("openapi: 3.0.0\ninfo: {}\n", "1.2.3"); err == nil {
		t.Fatal("a document without paths was accepted")
	}
}
