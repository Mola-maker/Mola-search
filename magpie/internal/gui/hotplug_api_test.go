package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/pluginrt"
)

// postHotplug asks the hot-plug API for one action.
func postHotplug(h http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/hotplug", strings.NewReader(body)))
	return rec
}

func readHotplug(t *testing.T, h http.Handler) hotplugState {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/hotplug", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/hotplug: %d %s", rec.Code, rec.Body)
	}
	var out hotplugState
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func hotOf(s hotplugState, id string) hotplugPlugin {
	for _, p := range s.Plugins {
		if p.ID == id {
			return p
		}
	}
	return hotplugPlugin{}
}

// TestHotplugLoadsUnloadsAndReloads: the API is the runtime's own state —
// an unload really disposes the plugin (its parts are gone), a reload while
// off is refused with the state, and loading it again applies it and raises
// its revision.
func TestHotplugLoadsUnloadsAndReloads(t *testing.T) {
	h := builtinHome(t)
	if _, err := pluginrt.Settle(); err != nil {
		t.Fatal(err)
	}
	// a new install has every one on
	st := readHotplug(t, h)
	p := hotOf(st, "routes")
	if p.State != pluginrt.Active || !p.On || len(p.Client) == 0 || p.ClientVersion == "" {
		t.Fatalf("the routes plugin before any action: %+v", p)
	}
	before := p.Revision

	// unload: disposed, and its parts leave the runtime
	rec := postHotplug(h, `{"id":"routes","action":"unload"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unload: %d %s", rec.Code, rec.Body)
	}
	st = readHotplug(t, h)
	p = hotOf(st, "routes")
	if p.On || p.State == pluginrt.Active || len(p.Parts) != 0 {
		t.Fatalf("after unload: %+v", p)
	}

	// reload while off is refused, and says so
	if rec := postHotplug(h, `{"id":"routes","action":"reload"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("reload of an off plugin: %d %s", rec.Code, rec.Body)
	}

	// load applies it again
	if rec := postHotplug(h, `{"id":"routes","action":"load"}`); rec.Code != http.StatusOK {
		t.Fatalf("load: %d %s", rec.Code, rec.Body)
	}
	st = readHotplug(t, h)
	p = hotOf(st, "routes")
	if !p.On || p.State != pluginrt.Active || p.Revision <= before {
		t.Fatalf("after load: %+v", p)
	}
	parts := len(p.Parts)
	if parts == 0 {
		t.Fatal("the loaded plugin registered no part")
	}

	// reload: the same parts, one dispose and one apply later
	rec = postHotplug(h, `{"id":"routes","action":"reload"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reload: %d %s", rec.Code, rec.Body)
	}
	st = readHotplug(t, h)
	p = hotOf(st, "routes")
	if p.State != pluginrt.Active || len(p.Parts) != parts || p.Revision <= before+1 {
		t.Fatalf("after reload: %+v", p)
	}

	// an unknown plugin or action is refused
	if rec := postHotplug(h, `{"id":"nope","action":"load"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown plugin: %d", rec.Code)
	}
	if rec := postHotplug(h, `{"id":"routes","action":"restart"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown action: %d", rec.Code)
	}
}

// TestHotplugRefusesInUseOff: a plugin something of the user's is made with
// stays on, as the plug-in switch already holds.
func TestHotplugRefusesInUseOff(t *testing.T) {
	h := builtinHome(t)
	if _, err := pluginrt.Settle(); err != nil {
		t.Fatal(err)
	}
	// the library is in use in the base state only when it holds something;
	// this switch's own guard is exercised by the builtins route, so this
	// checks the same refusal from the hot-plug route when it applies.
	for _, p := range readHotplug(t, h).Plugins {
		if p.InUse == 0 {
			continue
		}
		rec := postHotplug(h, `{"id":"`+p.ID+`","action":"unload"}`)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s in use: %d %s", p.ID, rec.Code, rec.Body)
		}
		return
	}
	t.Log("no plugin is in use in the base state")
}

// TestHotplugAssetSourceIsSaid: the page is told whether a reload reads
// files on disk or the binary's copy, since only the first can change.
func TestHotplugAssetSourceIsSaid(t *testing.T) {
	h := builtinHome(t)
	st := readHotplug(t, h)
	if st.AssetsFromDisk != assetsFromDisk() {
		t.Errorf("assetsFromDisk %v, asset source %v", st.AssetsFromDisk, assetsFromDisk())
	}
}
