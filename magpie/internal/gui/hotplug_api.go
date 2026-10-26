package gui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	"github.com/yetone/magpie/internal/pluginrt"
)

// The hot-plug API: what the running plugin runtime really has, and the
// three things a page may ask of it — load, unload and reload. It is the
// runtime's own state (pluginrt), never a copy: a plugin's State, Why and
// Revision are the ones Reconcile and Reload produced.
//
// What it can and cannot do is said in the payload, not promised in the
// page: a built-in plugin's Go code is compiled into the binary, so a
// reload re-runs its Apply with the config plugins.yml holds now; its
// client files are re-fetched from the server, which serves them from disk
// in a dev build (assetsFromDisk) and from the binary otherwise.

// hotplugPlugin is one built-in plugin as the hot-plug page is told it.
type hotplugPlugin struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	State  pluginrt.State  `json:"state"`
	Why    string          `json:"why,omitempty"`
	On     bool            `json:"on"`
	InUse  int             `json:"inUse"`
	Use    string          `json:"use,omitempty"`
	Parts  []pluginrt.Part `json:"parts,omitempty"`
	Client []string        `json:"client,omitempty"`
	// Config is the plugin's settings as plugins.yml composes them now:
	// what its client code is given after a reload
	Config map[string]any `json:"config,omitempty"`
	// ClientVersion is a digest of the client files as the server would
	// serve them now. It changes when a dev build's files change, and only
	// with a new binary otherwise.
	ClientVersion string `json:"clientVersion,omitempty"`
	Revision      int    `json:"revision"`
	// WhyNot says why the page should not offer an action.
	WhyNot string `json:"whyNot,omitempty"`
}

// hotplugState is the answer to a read or an action.
type hotplugState struct {
	Plugins []hotplugPlugin `json:"plugins"`
	// AssetsFromDisk says whether a reload's fresh fetch reads files on
	// disk (a dev build) or the binary's copy (shipped).
	AssetsFromDisk bool   `json:"assetsFromDisk"`
	Action         string `json:"action,omitempty"`
	ID             string `json:"id,omitempty"`
}

// hotplugStates is every built-in plugin as it runs now.
func hotplugStates() hotplugState {
	list := builtinsState()
	inUse := map[string]int{}
	use := map[string]string{}
	config := map[string]map[string]any{}
	for _, b := range list {
		inUse[b.ID], use[b.ID], config[b.ID] = b.InUse, b.Use, b.Config
	}
	st := map[string]pluginrt.Status{}
	if rt := pluginrt.Running(); rt != nil {
		for _, s := range rt.Statuses() {
			st[s.ID] = s
		}
	}
	out := hotplugState{AssetsFromDisk: assetsFromDisk(), Plugins: []hotplugPlugin{}}
	for _, m := range pluginrt.Registered() {
		s := st[m.ID]
		p := hotplugPlugin{
			ID: m.ID, Name: m.Name, State: s.State, Why: s.Why, On: s.State == pluginrt.Active,
			InUse: inUse[m.ID], Use: use[m.ID], Parts: s.Parts, Client: m.Client, Config: config[m.ID],
			Revision: s.Revision, ClientVersion: pluginClientVersion(m.ID, m.Client),
		}
		switch {
		case s.State != pluginrt.Active:
			p.WhyNot = "not active: load it first"
		case inUse[m.ID] > 0:
			p.WhyNot = "in use, so it stays on"
		}
		out.Plugins = append(out.Plugins, p)
	}
	return out
}

// pluginClientVersion is a digest of the client files as they are served
// now, empty for a plugin with none.
func pluginClientVersion(id string, files []string) string {
	if len(files) == 0 {
		return ""
	}
	h := sha256.New()
	for _, f := range files {
		b, err := fs.ReadFile(staticFS(), "plugins/"+id+"/"+f)
		if err != nil {
			h.Write([]byte("missing:" + f + ";"))
			continue
		}
		h.Write([]byte(f))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// hotplugRoutes: GET /api/hotplug is the runtime's honest state; POST
// {"id", "action"} with action load, unload or reload does that one thing
// through the runtime and answers with the state after it.
func hotplugRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hotplug", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, hotplugStates())
	})
	mux.HandleFunc("POST /api/hotplug", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		known := false
		for _, m := range pluginrt.Registered() {
			if m.ID == in.ID {
				known = true
				break
			}
		}
		if !known {
			fail(rw, fmt.Errorf("no built-in plugin %q", in.ID))
			return
		}
		var err error
		switch in.Action {
		case "load":
			err = pluginrt.Set(in.ID, true)
			if err == nil {
				_, err = pluginrt.Refresh()
			}
		case "unload":
			for _, p := range hotplugStates().Plugins {
				if p.ID != in.ID {
					continue
				}
				if p.InUse > 0 {
					rw.Header().Set("Content-Type", "application/json")
					rw.WriteHeader(http.StatusConflict)
					writeJSON(rw, map[string]any{
						"error": fmt.Sprintf("%s is in use%s and stays on", p.Name, inUseSuffix(p.Use, p.InUse)),
						"code":  "in-use", "inUse": p.InUse, "use": p.Use,
					})
					return
				}
			}
			err = pluginrt.Set(in.ID, false)
			if err == nil {
				_, err = pluginrt.Refresh()
			}
		case "reload":
			_, err = pluginrt.Reload(in.ID)
		default:
			fail(rw, fmt.Errorf("no hot-plug action %q (load, unload or reload)", in.Action))
			return
		}
		if err != nil {
			fail(rw, err)
			return
		}
		out := hotplugStates()
		out.Action, out.ID = in.Action, in.ID
		writeJSON(rw, out)
	})
}

func inUseSuffix(use string, n int) string {
	if use == "" {
		return ""
	}
	return " (" + use + ")"
}
