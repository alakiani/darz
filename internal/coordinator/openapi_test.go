package coordinator

import (
	"bytes"
	"flag"
	"log/slog"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite openapi.json")

// TestOpenAPISpecIsCurrent keeps the checked-in contract in step with the
// routes and types. Run with -update after changing either.
func TestOpenAPISpecIsCurrent(t *testing.T) {
	c := New(nil, nil, slog.New(slog.DiscardHandler))
	got, err := c.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("openapi.json", got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatalf("%v (run go test ./internal/coordinator -run OpenAPI -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("openapi.json is stale: run go test ./internal/coordinator -run OpenAPI -update")
	}
}
