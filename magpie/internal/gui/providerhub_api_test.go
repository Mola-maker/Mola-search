package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/providerhub"
)

// The platform's routes answer with the real catalog, write a key into the
// .env the engine reads, and refuse to check a provider that has no key.
func TestProviderHubCatalogSaveAndRefusals(t *testing.T) {
	t.Setenv("MOLASEARCH_DATA", t.TempDir())
	t.Setenv("MOLASEARCH_ENV", "")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("MOLA_EMBED_API_KEY", "")
	h := Handler(nil, nil)

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec
	}

	rec := get("/api/providerhub/catalog")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog: %d %s", rec.Code, rec.Body)
	}
	var cat providerhub.Catalog
	if err := json.Unmarshal(rec.Body.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	if cat.Total != len(providerhub.All()) || len(cat.Shelves) != len(providerhub.Categories) {
		t.Fatalf("catalog: %d providers on %d shelves", cat.Total, len(cat.Shelves))
	}
	if cat.EnvFile == "" {
		t.Fatal("catalog names no .env file")
	}

	rec = post("/api/providerhub/save", `{"id":"exa","values":{"EXA_API_KEY":"abc123def456"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	var saved struct {
		Provider providerhub.State `json:"provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Provider.Configured || !saved.Provider.Enabled {
		t.Fatalf("after save: %+v", saved.Provider)
	}

	rec = post("/api/providerhub/save", `{"id":"exa","values":{"PATH":"x"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a foreign field was accepted: %d %s", rec.Code, rec.Body)
	}

	// tavily has no key in this sandbox: a check is refused, not simulated
	rec = post("/api/providerhub/test", `{"id":"tavily"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not-configured") {
		t.Fatalf("testing an unconfigured provider: %d %s", rec.Code, rec.Body)
	}

	jina, ok := providerhub.ByID("jina-embed")
	if !ok {
		t.Fatal("no jina-embed provider")
	}
	if err := providerhub.SetEnabled(jina, false); err != nil {
		t.Fatal(err)
	}
	rec = post("/api/providerhub/test", `{"id":"jina-embed"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "disabled") {
		t.Fatalf("testing a turned-off provider: %d %s", rec.Code, rec.Body)
	}
}
