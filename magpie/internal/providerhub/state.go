// The catalog the platform page draws: every provider with its declared
// fields, whether those fields are set (masked), whether the user lets it
// run, and whether it is the provider filling an engine slot.
package providerhub

import (
	"github.com/yetone/magpie/internal/searchdet"
)

// FieldState is one declared field's current value as the page may show it:
// a secret masked, a base URL or model in full.
type FieldState struct {
	Env    string `json:"env"`
	Set    bool   `json:"set"`
	Masked string `json:"masked,omitempty"`
	Value  string `json:"value,omitempty"`
}

// State is one provider with its live standing.
type State struct {
	Provider
	Configured bool         `json:"configured"`
	Enabled    bool         `json:"enabled"`
	Active     bool         `json:"active"`
	Values     []FieldState `json:"values"`
}

// StateOf reads one provider's standing.
func StateOf(p Provider) State {
	cfg := Config(p)
	st := State{Provider: p, Configured: Configured(p, cfg), Enabled: Enabled(p)}
	active := ""
	if p.Slot != "" {
		active = ActiveInSlot(p.Slot)
		// A slot holds one provider's keys under the engine's shared
		// MOLA_<SLOT>_* names. Another provider on the same shelf would read
		// those names as its own, so it reads unconfigured with an empty
		// form until it is chosen; otherwise four embedding providers would
		// all claim the one saved key.
		if active != "" && active != p.ID {
			st.Configured = false
			for _, f := range p.Fields {
				st.Values = append(st.Values, FieldState{Env: f.Env})
			}
			return st
		}
		st.Active = st.Configured && active == p.ID
	}
	for _, f := range p.Fields {
		v := cfg[f.Env]
		fs := FieldState{Env: f.Env, Set: v != ""}
		if f.Secret {
			fs.Masked = Mask(v)
		} else {
			fs.Value = v
		}
		st.Values = append(st.Values, fs)
	}
	return st
}

// Shelf is one category with its providers.
type Shelf struct {
	ID        Category `json:"id"`
	Label     string   `json:"label"`
	Providers []State  `json:"providers"`
}

// Catalog is the whole page: every shelf, the file keys go to, and the
// counts the header shows.
type Catalog struct {
	Shelves     []Shelf `json:"shelves"`
	EnvFile     string  `json:"envFile"`
	Configured  int     `json:"configured"`
	Enabled     int     `json:"enabled"`
	Total       int     `json:"total"`
	Error       string  `json:"error,omitempty"`
	SearchGates string  `json:"searchGates"`
}

// CatalogNow builds the page's whole state.
func CatalogNow() Catalog {
	cat := Catalog{EnvFile: searchdet.EnvTarget(), SearchGates: searchdet.SettingsFile()}
	if _, err := loadGates(); err != nil {
		cat.Error = err.Error()
	}
	byCat := map[Category][]State{}
	for _, p := range All() {
		s := StateOf(p)
		byCat[p.Category] = append(byCat[p.Category], s)
		cat.Total++
		if s.Configured {
			cat.Configured++
		}
		if s.Enabled {
			cat.Enabled++
		}
	}
	for _, c := range Categories {
		ps := byCat[c]
		if ps == nil {
			ps = []State{}
		}
		cat.Shelves = append(cat.Shelves, Shelf{ID: c, Label: CategoryLabel(c), Providers: ps})
	}
	return cat
}
