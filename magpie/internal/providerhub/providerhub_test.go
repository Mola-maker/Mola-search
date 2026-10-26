package providerhub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/searchdet"
)

// sandbox points the engine's data dir (and so the .env and gate files) at a
// fresh folder, and clears the keys the tests write.
func sandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MOLASEARCH_DATA", dir)
	t.Setenv("MOLASEARCH_ENV", "")
	t.Setenv("EXA_API_KEY", "")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("MOLA_EMBED_API_BASE", "")
	t.Setenv("MOLA_EMBED_API_KEY", "")
	t.Setenv("MOLA_EMBED_MODEL", "")
	searchdet.ReloadEnv()
	t.Cleanup(searchdet.ReloadEnv)
	return dir
}

func TestCatalogHasEveryShelfAndSoundProviders(t *testing.T) {
	sandbox(t)
	cat := CatalogNow()
	if len(cat.Shelves) != len(Categories) {
		t.Fatalf("%d shelves, want %d", len(cat.Shelves), len(Categories))
	}
	seen := map[string]bool{}
	for _, sh := range cat.Shelves {
		if len(sh.Providers) == 0 {
			t.Errorf("shelf %s is empty", sh.ID)
		}
		if sh.Label == "" {
			t.Errorf("shelf %s has no label", sh.ID)
		}
		for _, p := range sh.Providers {
			if p.ID == "" || p.Name == "" {
				t.Errorf("shelf %s has a provider without id or name", sh.ID)
			}
			if seen[p.ID] {
				t.Errorf("provider id %q is used twice", p.ID)
			}
			seen[p.ID] = true
			if p.Category != sh.ID {
				t.Errorf("%s sits on shelf %s, says %s", p.ID, sh.ID, p.Category)
			}
			if p.Docs == "" {
				t.Errorf("%s has no docs link", p.ID)
			}
			if p.Icon == "" || p.Tone == "" {
				t.Errorf("%s has no icon or tone", p.ID)
			}
			for _, f := range p.Fields {
				if !envName.MatchString(f.Env) {
					t.Errorf("%s declares %q, not an environment name", p.ID, f.Env)
				}
				if f.Key == "" {
					t.Errorf("%s field %s has no label", p.ID, f.Env)
				}
			}
			if p.Slot != "" {
				if len(p.Fields) != 3 {
					t.Errorf("%s fills slot %s but declares %d fields, want base/key/model", p.ID, p.Slot, len(p.Fields))
				}
				if p.Fields[0].Env != slotBase(p.Provider) || p.Fields[1].Env != slotKey(p.Provider) || p.Fields[2].Env != slotModel(p.Provider) {
					t.Errorf("%s's slot fields are %v", p.ID, p.Provider.RequiredEnv())
				}
			}
			if p.Channel != "" && !p.Wired {
				t.Errorf("%s maps channel %q but is not marked wired", p.ID, p.Channel)
			}
		}
	}
	want := []string{"tencent", "exa", "tavily", "brave", "firecrawl", "deepseek", "context7", "oh-my-claudecode", "superpowers"}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("the platform does not list %q", id)
		}
	}
}

func TestConfiguredFollowsEachKeySet(t *testing.T) {
	sandbox(t)
	tencent, _ := ByID("tencent")
	if Configured(tencent, map[string]string{}) {
		t.Fatal("tencent reads configured with no keys")
	}
	if !Configured(tencent, map[string]string{"TENCENT_SECRET_ID": "id", "TENCENT_SECRET_KEY": "key"}) {
		t.Fatal("tencent with the signed pair reads unconfigured")
	}
	if !Configured(tencent, map[string]string{"TENCENT_WSA_API_KEY": "wsa"}) {
		t.Fatal("tencent with the MCP key alone reads unconfigured")
	}
	if Configured(tencent, map[string]string{"TENCENT_SECRET_ID": "id"}) {
		t.Fatal("half the signed pair reads configured")
	}
	exa, _ := ByID("exa")
	if Configured(exa, map[string]string{"EXA_API_KEY": ""}) {
		t.Fatal("exa without its key reads configured")
	}
	if !Configured(exa, map[string]string{"EXA_API_KEY": "k"}) {
		t.Fatal("exa with its key reads unconfigured")
	}
	github, _ := ByID("github")
	if !Configured(github, map[string]string{}) {
		t.Fatal("a keyless provider reads unconfigured")
	}
	// a model on this computer takes no key; the base and the model are
	// what make its slot complete
	ollama, _ := ByID("ollama-embed")
	if !Configured(ollama, map[string]string{
		"MOLA_EMBED_API_BASE": "http://127.0.0.1:11434/v1",
		"MOLA_EMBED_MODEL":    "nomic-embed-text",
	}) {
		t.Fatal("a local model without a key reads unconfigured")
	}
	if Configured(ollama, map[string]string{"MOLA_EMBED_API_BASE": "http://127.0.0.1:11434/v1"}) {
		t.Fatal("a local model without its model name reads configured")
	}
}

func TestSaveWritesTheDeclaredFieldsAndMasksThem(t *testing.T) {
	dir := sandbox(t)
	st, err := Save(SaveInput{ID: "exa", Values: map[string]string{"EXA_API_KEY": "abc123def456"}})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Configured {
		t.Fatalf("after saving a key the state is %+v", st)
	}
	if len(st.Values) != 1 || !st.Values[0].Set {
		t.Fatalf("fields: %+v", st.Values)
	}
	if st.Values[0].Masked == "abc123def456" || !strings.Contains(st.Values[0].Masked, "…") {
		t.Fatalf("the key is shown as %q", st.Values[0].Masked)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "EXA_API_KEY=abc123def456") {
		t.Fatalf(".env holds:\n%s", raw)
	}
	info, err := os.Stat(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf(".env is %v, want no group or other access", info.Mode().Perm())
	}
	// the engine sees the key on its next call, without a restart
	if got := searchdet.ResolveKeys().Exa; got != "abc123def456" {
		t.Fatalf("the engine reads %q", got)
	}
}

func TestSaveKeepsOtherLinesAndClearsWhatItIsTold(t *testing.T) {
	dir := sandbox(t)
	path := filepath.Join(dir, ".env")
	before := "# kept by hand\nOTHER=one\nexa_api_key=old\nTAVILY_API_KEY=leave-me\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	searchdet.ReloadEnv()
	if _, err := Save(SaveInput{ID: "exa", Values: map[string]string{"EXA_API_KEY": "new value"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	s := string(raw)
	if !strings.Contains(s, "# kept by hand") || !strings.Contains(s, "OTHER=one") {
		t.Fatalf("other lines were lost:\n%s", s)
	}
	if !strings.Contains(s, "TAVILY_API_KEY=leave-me") {
		t.Fatalf("another provider's key was touched:\n%s", s)
	}
	if !strings.Contains(s, `EXA_API_KEY="new value"`) {
		t.Fatalf("a value with a space is not quoted:\n%s", s)
	}
	if _, err := Save(SaveInput{ID: "exa", Clear: []string{"EXA_API_KEY"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "EXA_API_KEY") {
		t.Fatalf("a cleared key stays:\n%s", raw)
	}
}

func TestSaveRefusesNamesTheProviderDidNotDeclare(t *testing.T) {
	sandbox(t)
	if _, err := Save(SaveInput{ID: "exa", Values: map[string]string{"OPENAI_API_KEY": "x"}}); err == nil {
		t.Fatal("a foreign key was written")
	}
	if _, err := Save(SaveInput{ID: "exa", Clear: []string{"PATH"}}); err == nil {
		t.Fatal("a foreign name was cleared")
	}
	if _, err := Save(SaveInput{ID: "exa", Values: map[string]string{"EXA_API_KEY": "a\nb"}}); err == nil {
		t.Fatal("a multi-line value was written")
	}
	if _, err := Save(SaveInput{ID: "nope"}); err == nil {
		t.Fatal("an unknown provider was saved")
	}
}

func TestGatesTurnProvidersOff(t *testing.T) {
	sandbox(t)
	exa, _ := ByID("exa")
	if !Enabled(exa) {
		t.Fatal("exa starts turned off")
	}
	if err := SetEnabled(exa, false); err != nil {
		t.Fatal(err)
	}
	if Enabled(exa) {
		t.Fatal("after turning it off exa still reads on")
	}
	// the engine's own gate is the one written for a search provider
	s, err := searchdet.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Disabled("exa") {
		t.Fatalf("the engine's disabled list is %v", s.DisabledChannels)
	}
	if err := SetEnabled(exa, true); err != nil {
		t.Fatal(err)
	}
	s, _ = searchdet.LoadSettings()
	if s.Disabled("exa") {
		t.Fatal("turning exa back on did not clear the engine gate")
	}
	// a non-search provider keeps its gate in the platform's own file
	jina, _ := ByID("jina-embed")
	if err := SetEnabled(jina, false); err != nil {
		t.Fatal(err)
	}
	if Enabled(jina) {
		t.Fatal("a turned-off embedding provider reads on")
	}
	raw, _ := os.ReadFile(gatePath())
	if !strings.Contains(string(raw), "jina-embed") {
		t.Fatalf("the gate file holds:\n%s", raw)
	}
}

// A gate-only save (the page's switch) must not change which provider fills
// an engine slot: every provider of a slot reads the same MOLA_<SLOT>_*
// names, so another provider's values would look like its own.
func TestGateSaveDoesNotStealTheSlot(t *testing.T) {
	sandbox(t)
	if _, err := Save(SaveInput{ID: "jina-embed", Values: map[string]string{
		"MOLA_EMBED_API_BASE": "https://api.jina.ai/v1",
		"MOLA_EMBED_API_KEY":  "abc123def456",
		"MOLA_EMBED_MODEL":    "jina-embeddings-v3",
	}}); err != nil {
		t.Fatal(err)
	}
	if got := ActiveInSlot("embedding"); got != "jina-embed" {
		t.Fatalf("after its keys were saved the slot is %q", got)
	}
	on := true
	if _, err := Save(SaveInput{ID: "openai-embed", Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	if got := ActiveInSlot("embedding"); got != "jina-embed" {
		t.Fatalf("a gate-only save moved the slot to %q", got)
	}
}

// One slot has one set of keys: the provider that saved them reads
// configured, and every other provider on the shelf reads blank rather than
// borrowing a vendor's key as its own.
func TestOneSlotHoldsOneProvidersKeys(t *testing.T) {
	sandbox(t)
	if _, err := Save(SaveInput{ID: "jina-embed", Values: map[string]string{
		"MOLA_EMBED_API_BASE": "https://api.jina.ai/v1",
		"MOLA_EMBED_API_KEY":  "abc123def456",
		"MOLA_EMBED_MODEL":    "jina-embeddings-v3",
	}}); err != nil {
		t.Fatal(err)
	}
	embedding := func() []State {
		for _, sh := range CatalogNow().Shelves {
			if sh.ID == CatEmbedding {
				return sh.Providers
			}
		}
		return nil
	}
	rows := embedding()
	if len(rows) != 4 {
		t.Fatalf("embedding providers: %d", len(rows))
	}
	configured := 0
	for _, st := range rows {
		if st.Configured {
			configured++
			if st.ID != "jina-embed" || !st.Active {
				t.Errorf("%s claims the slot jina-embed saved (active %v)", st.ID, st.Active)
			}
		}
		if st.ID == "jina-embed" {
			continue
		}
		for _, v := range st.Values {
			if v.Set || v.Masked != "" || v.Value != "" {
				t.Errorf("%s reads jina's values: %+v", st.ID, v)
			}
		}
	}
	if configured != 1 {
		t.Fatalf("%d providers claim the one saved slot", configured)
	}
	// Clearing the key keeps the choice and empties the slot: no provider
	// claims it, and the other three still read blank.
	if _, err := Save(SaveInput{ID: "jina-embed", Clear: []string{"MOLA_EMBED_API_KEY"}}); err != nil {
		t.Fatal(err)
	}
	if got := ActiveInSlot("embedding"); got != "jina-embed" {
		t.Fatalf("a clear moved the choice to %q", got)
	}
	rows = embedding()
	for _, st := range rows {
		if st.Configured {
			t.Errorf("%s reads configured with the slot's key cleared", st.ID)
		}
		if st.ID != "jina-embed" {
			for _, v := range st.Values {
				if v.Set || v.Masked != "" || v.Value != "" {
					t.Errorf("%s reads the emptied slot's values: %+v", st.ID, v)
				}
			}
		}
	}
}

// A slot's fields share the engine's names, so a partial write would pair
// one vendor's base with another's key. It is refused before anything is
// written; a set that is complete can switch the slot.
func TestSlotSaveNeedsEveryField(t *testing.T) {
	dir := sandbox(t)
	if _, err := Save(SaveInput{ID: "openai-embed", Values: map[string]string{
		"MOLA_EMBED_API_BASE": "https://api.openai.com/v1",
	}}); err == nil {
		t.Fatal("a partial slot save was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
		t.Fatal("a refused slot save wrote .env")
	}
	if got := ActiveInSlot("embedding"); got != "" {
		t.Fatalf("a refused save chose %q", got)
	}
	if _, err := Save(SaveInput{ID: "openai-embed", Values: map[string]string{
		"MOLA_EMBED_API_BASE": "https://api.openai.com/v1",
		"MOLA_EMBED_API_KEY":  "abc123def456",
		"MOLA_EMBED_MODEL":    "text-embedding-3-small",
	}}); err != nil {
		t.Fatal(err)
	}
	if got := ActiveInSlot("embedding"); got != "openai-embed" {
		t.Fatalf("a complete save chose %q", got)
	}
}

func TestRunProbeRefusesWhatItCannotCheck(t *testing.T) {
	sandbox(t)
	if _, err := RunProbe(context.Background(), "not-a-provider"); err == nil {
		t.Fatal("an unknown provider was probed")
	}
	if _, err := RunProbe(context.Background(), "exa"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("probing exa without a key: %v", err)
	}
	if _, err := Save(SaveInput{ID: "exa", Values: map[string]string{"EXA_API_KEY": "k"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(SaveInput{ID: "exa", Enabled: boolp(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunProbe(context.Background(), "exa"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("probing a turned-off provider: %v", err)
	}
}

func boolp(b bool) *bool { return &b }

func TestProbeOpenAIReportsWhatTheKeySees(t *testing.T) {
	var gotAuth, gotPath string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":[{"id":"text-embedding-3-small"},{"id":"text-embedding-3-large"}]}`))
	}))
	defer srv.Close()
	p, _ := ByID("openai-embed")
	detail, err := probe(context.Background(), p, map[string]string{"__base": srv.URL, "MOLA_EMBED_API_KEY": "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("the probe sent %q", gotAuth)
	}
	if gotPath != "/models" {
		t.Fatalf("the probe asked %q", gotPath)
	}
	if detail != "2 entries visible to this key" {
		t.Fatalf("detail %q", detail)
	}
}

func TestProbeShowsTheVendorsRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()
	p, _ := ByID("openai-embed")
	_, err := probe(context.Background(), p, map[string]string{"__base": srv.URL, "MOLA_EMBED_API_KEY": "sk-bad"})
	if err == nil {
		t.Fatal("a 401 was reported as a good connection")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("the refusal reads %q", err)
	}
}

func TestProbeMCPReadsServerInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer c7" {
			t.Errorf("Authorization: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"serverInfo\":{\"name\":\"context7\",\"version\":\"2.1\"}}}\n\n"))
	}))
	defer srv.Close()
	p, _ := ByID("context7")
	got, err := probe(context.Background(), p, map[string]string{
		"__base":           srv.URL,
		"CONTEXT7_MCP_URL": srv.URL,
		"CONTEXT7_API_KEY": "c7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "context7 2.1" {
		t.Fatalf("detail %q", got)
	}
}

func TestBrokenGateFileIsShownNotSwallowed(t *testing.T) {
	dir := sandbox(t)
	if err := os.WriteFile(filepath.Join(dir, "providerhub.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cat := CatalogNow()
	if cat.Error == "" {
		t.Fatal("a corrupt gate file is not reported")
	}
	if Enabled(mustProvider(t, "jina-embed")) {
		t.Fatal("with the gates unreadable a provider still reads on")
	}
}

func TestCatalogCountsWhatTheUserHas(t *testing.T) {
	sandbox(t)
	if _, err := Save(SaveInput{ID: "exa", Values: map[string]string{"EXA_API_KEY": "k"}}); err != nil {
		t.Fatal(err)
	}
	cat := CatalogNow()
	if cat.Total != len(All()) {
		t.Fatalf("total %d, want %d", cat.Total, len(All()))
	}
	if cat.Configured < 1 {
		t.Fatalf("configured %d after one key", cat.Configured)
	}
	raw, _ := json.Marshal(cat)
	if !strings.Contains(string(raw), `"exa"`) {
		t.Fatal("the catalog does not carry the provider id")
	}
}

func mustProvider(t *testing.T, id string) Provider {
	t.Helper()
	p, ok := ByID(id)
	if !ok {
		t.Fatalf("no provider %q", id)
	}
	return p
}
