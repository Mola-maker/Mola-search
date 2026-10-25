package ragstore

import (
	"os"
	"strings"
	"testing"
)

// TestStoreContractDoesNotDependOnTheSearchEngine pins the direction of the
// type dependency: the Store interface speaks the shared USMF record, not
// searchdet's copy of it. Without this guard, a future edit can quietly put
// the retrieval engine back in the memory layer's contract.
func TestStoreContractDoesNotDependOnTheSearchEngine(t *testing.T) {
	src, err := os.ReadFile("types.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "type Store interface")
	if start < 0 {
		t.Fatal("Store interface not found in types.go")
	}
	end := strings.Index(text[start:], "\n}")
	if end < 0 {
		t.Fatal("Store interface has no closing brace")
	}
	decl := text[start : start+end]
	if strings.Contains(decl, "searchdet.") {
		t.Fatalf("Store's contract imports the search engine's types:\n%s", decl)
	}
	if !strings.Contains(decl, "usmf.CanonicalMemoryRecord") {
		t.Fatalf("Store does not speak the shared USMF record:\n%s", decl)
	}
}
