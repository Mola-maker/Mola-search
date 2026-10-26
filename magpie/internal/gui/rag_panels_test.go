package gui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/ragstore"
)

// ragTestServer isolates the package-level manager on a throwaway data
// directory, so the panel endpoints read a corpus this test wrote.
func ragTestServer(t *testing.T) *http.ServeMux {
	t.Helper()
	t.Setenv("MOLASEARCH_DATA", t.TempDir())
	ragMu.Lock()
	oldManager, oldErr := ragManager, ragInitErr
	ragManager, ragInitErr = nil, nil
	ragMu.Unlock()
	t.Cleanup(func() {
		ragMu.Lock()
		if ragManager != nil {
			_ = ragManager.Close()
		}
		ragManager, ragInitErr = oldManager, oldErr
		ragMu.Unlock()
	})
	mux := http.NewServeMux()
	ragStoreRoutes(mux)
	return mux
}

func TestEventStreamsAreNotCompressed(t *testing.T) {
	// A live feed is flushed frame by frame; the web server's gzip pass must
	// leave it alone so an operation appears when it happens.
	if compressible("text/event-stream") || compressible("text/event-stream; charset=utf-8") {
		t.Error("text/event-stream would be gzipped")
	}
	if !compressible("application/json") {
		t.Error("JSON should still be compressed")
	}
}

func TestRagEventsStreamsHelloThenOperations(t *testing.T) {
	mux := ragTestServer(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/rag/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	reader := bufio.NewReader(resp.Body)
	event, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if event != "event: hello\n" {
		t.Fatalf("first frame = %q", event)
	}
	data, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(data, "data: ") {
		t.Fatalf("hello data = %q", data)
	}
	var hello struct {
		Operations []ragstore.Operation `json:"operations"`
		Agents     []ragAgent           `json:"agents"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(data), "data: ")), &hello); err != nil {
		t.Fatalf("hello JSON: %v (%q)", err, data)
	}
	if len(hello.Agents) == 0 {
		t.Error("the hello frame carries no agent registry")
	}
	if len(hello.Operations) != 0 {
		t.Errorf("a fresh stream invented %d operations", len(hello.Operations))
	}
}

func TestWriteSSEFrame(t *testing.T) {
	var buf bytes.Buffer
	op := ragstore.Operation{ID: 7, Actor: "magpie", Backend: "sqlite_hybrid", Op: "read", Detail: "search", Count: 2}
	if err := writeSSE(&buf, "op", op); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.HasPrefix(body, "event: op\ndata: ") || !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("frame = %q", body)
	}
	var got ragstore.Operation
	line := strings.TrimSuffix(strings.TrimPrefix(body, "event: op\ndata: "), "\n\n")
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("data is not JSON: %v (%q)", err, line)
	}
	if got.ID != 7 || got.Backend != "sqlite_hybrid" {
		t.Errorf("decoded = %+v", got)
	}
}

func TestRagPanelsEndpointReadsTheCorpusItWasGiven(t *testing.T) {
	mux := ragTestServer(t)
	post := func(path, actor, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if actor != "" {
			req.Header.Set("X-Magpie-Actor", actor)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/api/rag/records", "MCP/test-client 1.0",
		`{"topic":"battery chemistry","claim":"LFP lasts","evidenceText":"a fleet study","verdict":"VERIFIED","sourceDomain":"example.org","authorityLevel":3}`); rec.Code != http.StatusOK {
		t.Fatalf("write record: %d %s", rec.Code, rec.Body)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/rag/panels", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("panels: %d %s", rec.Code, rec.Body)
	}
	var data ragstore.PanelsData
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("panels JSON: %v", err)
	}
	if len(data.Records) != 1 || data.Records[0].Topic != "battery chemistry" {
		t.Fatalf("records = %+v", data.Records)
	}
	if !data.Graph.Available || len(data.Graph.Nodes) != 1 {
		t.Errorf("graph = %+v", data.Graph)
	}
	if data.Vectors.Available {
		t.Errorf("one vector reported a projection: %+v", data.Vectors)
	}
	if !data.Documents.Available {
		t.Errorf("documents = %+v", data.Documents)
	}
	if !data.Relational.Available {
		t.Fatalf("relational = %+v", data.Relational)
	}
	var recordRows int
	for _, table := range data.Relational.Tables {
		if table.Name == "memory_records" {
			recordRows = table.Rows
		}
	}
	if recordRows != 1 {
		t.Errorf("memory_records rows = %d, want 1", recordRows)
	}

	// The write is a real operation, attributed to the client that said it
	// was calling. The registry comes back with it, for the icon.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/rag/operations", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("operations: %d %s", rec.Code, rec.Body)
	}
	var ops struct {
		Operations []ragstore.Operation `json:"operations"`
		Agents     []ragAgent           `json:"agents"`
		Latest     int64                `json:"latest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ops); err != nil {
		t.Fatalf("operations JSON: %v", err)
	}
	var sawWrite bool
	for _, op := range ops.Operations {
		if op.Op == "write" && op.Detail == "memory" {
			sawWrite = true
			if op.Actor != "MCP/test-client 1.0" {
				t.Errorf("write actor = %q", op.Actor)
			}
			if op.Count != 1 {
				t.Errorf("write count = %d", op.Count)
			}
		}
	}
	if !sawWrite {
		t.Errorf("no write operation in %+v", ops.Operations)
	}
	if len(ops.Agents) == 0 {
		t.Fatal("no agent registry rows")
	}
	for _, a := range ops.Agents {
		if a.IconURL == "" {
			t.Errorf("%s has no icon URL", a.ID)
		}
	}
}
