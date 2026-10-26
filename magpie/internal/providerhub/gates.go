// The platform's own gates: which providers the user turned off, and which
// provider currently fills an engine slot. The search channels reuse the
// engine's DisabledChannels so there is one truth, not two.
package providerhub

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yetone/magpie/internal/searchdet"
)

// gateFile holds the non-search gates and the active provider per slot.
type gateFile struct {
	Disabled []string          `json:"disabled"`
	Slots    map[string]string `json:"slots"`
}

// gatePath is where the platform's gates live.
func gatePath() string { return filepath.Join(searchdet.DataDir(), "providerhub.json") }

// loadGates reads the gate file. A missing file is an empty state; a file
// that exists but cannot be read is an error the page shows, never silently
// treated as "everything on".
func loadGates() (gateFile, error) {
	g := gateFile{Disabled: []string{}, Slots: map[string]string{}}
	raw, err := os.ReadFile(gatePath())
	if errors.Is(err, os.ErrNotExist) {
		return g, nil
	}
	if err != nil {
		return g, err
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return gateFile{Disabled: []string{}, Slots: map[string]string{}}, fmt.Errorf("%s: %w", gatePath(), err)
	}
	if g.Disabled == nil {
		g.Disabled = []string{}
	}
	if g.Slots == nil {
		g.Slots = map[string]string{}
	}
	return g, nil
}

// saveGates writes through a temp file and rename.
func saveGates(g gateFile) error {
	if g.Disabled == nil {
		g.Disabled = []string{}
	}
	if g.Slots == nil {
		g.Slots = map[string]string{}
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	path := gatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".providerhub-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// disabledNonSearch reports whether id is turned off in the platform's file.
func disabledNonSearch(id string) bool {
	g, err := loadGates()
	if err != nil {
		// An unreadable file means "unknown"; the page shows the error and
		// the provider stays off rather than being assumed on.
		return true
	}
	for _, d := range g.Disabled {
		if d == id {
			return true
		}
	}
	return false
}

// searchDisabled reports whether the engine has the provider's channel off.
func searchDisabled(p Provider) bool {
	if p.Channel == "" {
		return false
	}
	s, err := searchdet.LoadSettings()
	if err != nil {
		return false
	}
	return s.Disabled(p.Channel)
}

// Enabled reports whether the user lets MolaSearch use this provider.
func Enabled(p Provider) bool {
	if p.Category == CatSearch {
		return !searchDisabled(p)
	}
	return !disabledNonSearch(p.ID)
}

// SetEnabled turns one provider on or off, in the place its category already
// keeps that decision.
func SetEnabled(p Provider, on bool) error {
	if p.Category == CatSearch && p.Channel != "" {
		s, err := searchdet.LoadSettings()
		if err != nil {
			return err
		}
		next := make([]string, 0, len(s.DisabledChannels)+1)
		for _, id := range s.DisabledChannels {
			if id != p.Channel {
				next = append(next, id)
			}
		}
		if !on {
			next = append(next, p.Channel)
		}
		s.DisabledChannels = next
		return searchdet.SaveSettings(s)
	}
	g, err := loadGates()
	if err != nil {
		return err
	}
	next := make([]string, 0, len(g.Disabled)+1)
	for _, id := range g.Disabled {
		if id != p.ID {
			next = append(next, id)
		}
	}
	if !on {
		next = append(next, p.ID)
	}
	g.Disabled = next
	return saveGates(g)
}

// ActiveInSlot names the provider whose keys fill an engine slot, from the
// gate file. An empty slot string means nothing was chosen through the
// platform (keys may still have been set by hand in .env).
func ActiveInSlot(slot string) string {
	g, err := loadGates()
	if err != nil {
		return ""
	}
	return g.Slots[slot]
}

// setActiveInSlot records which provider filled a slot. Empty clears it.
func setActiveInSlot(slot, id string) error {
	g, err := loadGates()
	if err != nil {
		return err
	}
	if id == "" {
		delete(g.Slots, slot)
	} else {
		g.Slots[slot] = id
	}
	return saveGates(g)
}
