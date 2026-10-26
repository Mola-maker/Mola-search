// The provider platform's API: every provider with its standing, a form that
// writes keys into the .env the engine reads, a gate per provider, and a
// real connectivity check. A check with no key, or on a provider the user
// turned off, is refused rather than answered with a made-up result.
package gui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/providerhub"
)

// providerHubRoutes registers the platform's endpoints on the window's mux.
func providerHubRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/providerhub/catalog", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, providerhub.CatalogNow())
	})

	mux.HandleFunc("POST /api/providerhub/save", func(rw http.ResponseWriter, r *http.Request) {
		var in providerhub.SaveInput
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		st, err := providerhub.Save(in)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"provider": st, "catalog": providerhub.CatalogNow()})
	})

	mux.HandleFunc("POST /api/providerhub/test", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		res, err := providerhub.RunProbe(ctx, in.ID)
		switch {
		case errors.Is(err, providerhub.ErrNotConfigured):
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusConflict)
			writeJSON(rw, map[string]string{"error": err.Error(), "code": "not-configured"})
		case errors.Is(err, providerhub.ErrDisabled):
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusConflict)
			writeJSON(rw, map[string]string{"error": err.Error(), "code": "disabled"})
		case err != nil:
			fail(rw, err)
		default:
			writeJSON(rw, res)
		}
	})
}
