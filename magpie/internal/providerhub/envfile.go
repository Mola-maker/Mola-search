// Writing keys from the platform: a merge into the .env file MolaSearch
// already reads, never a second source of truth. Keys are written 0600
// through a temp file and rename, other lines are kept as they are, and a
// name the provider did not declare is refused.
package providerhub

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/searchdet"
)

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// SaveInput is one provider's form as the page posts it.
type SaveInput struct {
	ID      string            `json:"id"`
	Values  map[string]string `json:"values"`
	Clear   []string          `json:"clear"`
	Enabled *bool             `json:"enabled"`
}

// Config returns every value the provider reads, process environment first,
// then the .env file the engine reads.
func Config(p Provider) map[string]string {
	out := make(map[string]string, len(p.Fields))
	for _, f := range p.Fields {
		v := os.Getenv(f.Env)
		if v == "" {
			v = searchdet.EnvValue(f.Env)
		}
		out[f.Env] = v
	}
	return out
}

// Configured reports whether every alternative key set is complete. A
// keyless provider is always configured; it needs nothing to be asked.
func Configured(p Provider, cfg map[string]string) bool {
	if p.Keyless && len(p.Fields) == 0 {
		return true
	}
	if len(p.Modes) > 0 {
		for _, mode := range p.Modes {
			ok := true
			for _, name := range mode {
				if strings.TrimSpace(cfg[name]) == "" {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
		return false
	}
	for _, f := range p.Fields {
		if f.Required && strings.TrimSpace(cfg[f.Env]) == "" {
			return false
		}
	}
	return true
}

// Mask shows a key without giving it away: four characters at each end when
// it is long enough, a dot otherwise.
func Mask(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return "…"
	}
	return v[:4] + "…" + v[len(v)-4:]
}

// Save writes the form: declared fields only, cleared names removed, the
// gate set when asked, and the engine's key cache reloaded so the next
// request sees it. It returns the provider's fresh state.
func Save(in SaveInput) (State, error) {
	p, ok := ByID(in.ID)
	if !ok {
		return State{}, fmt.Errorf("unknown provider %q", in.ID)
	}
	declared := map[string]bool{}
	for _, f := range p.Fields {
		declared[f.Env] = true
	}
	values := map[string]string{}
	for name, v := range in.Values {
		if !declared[name] {
			return State{}, fmt.Errorf("%s: %q is not one of this provider's fields", p.ID, name)
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return State{}, fmt.Errorf("%s: a value cannot span lines", name)
		}
		values[name] = strings.TrimSpace(v)
	}
	for _, name := range in.Clear {
		if !declared[name] {
			return State{}, fmt.Errorf("%s: %q is not one of this provider's fields", p.ID, name)
		}
	}
	// A slot's providers share the engine's MOLA_<SLOT>_* names, so writing
	// a partial set over another vendor's would leave the slot with a base
	// from one and a key from the other. Writing values into a slot needs
	// the whole set; a clear alone is allowed and drops the slot below.
	if len(values) > 0 && p.Slot != "" {
		merged := Config(p)
		for name, v := range values {
			merged[name] = v
		}
		for _, name := range in.Clear {
			merged[name] = ""
		}
		if !Configured(p, merged) {
			return State{}, fmt.Errorf("%s: this slot needs every field; nothing was written", p.ID)
		}
	}
	if in.Enabled != nil {
		if err := SetEnabled(p, *in.Enabled); err != nil {
			return State{}, err
		}
	}
	// Only a save that touches the provider's keys can change which provider
	// fills an engine slot. A gate-only save (the page's switch) must not
	// hand the slot to a provider whose keys were not saved here: every
	// provider of a slot reads the same MOLA_<SLOT>_* names, so its values
	// would look complete even though they belong to whoever filled it.
	keysChanged := len(values) > 0 || len(in.Clear) > 0
	if keysChanged {
		if err := writeEnv(searchdet.EnvTarget(), values, in.Clear); err != nil {
			return State{}, err
		}
		searchdet.ReloadEnv()
	}
	if p.Slot != "" && keysChanged {
		// The slot is filled by this provider only while its needed values
		// are all there. A clear leaves the choice in place (the form keeps
		// showing this provider's remaining values) but the slot is not
		// configured, so no provider claims it until the set is complete
		// again or another provider's complete set is saved.
		if Configured(p, Config(p)) {
			if err := setActiveInSlot(p.Slot, p.ID); err != nil {
				return State{}, err
			}
		}
	}
	return StateOf(p), nil
}

// slotPrefix is the env prefix the engine reads for a slot. Embedding is
// EMBED, not EMBEDDING, because that is the name searchdet.ResolveKeys reads.
func slotPrefix(slot string) string {
	switch slot {
	case "embedding":
		return "EMBED"
	case "rerank":
		return "RERANK"
	case "vision":
		return "VISION"
	case "stt":
		return "STT"
	case "tts":
		return "TTS"
	}
	return strings.ToUpper(slot)
}

func slotBase(p Provider) string  { return "MOLA_" + slotPrefix(p.Slot) + "_API_BASE" }
func slotKey(p Provider) string   { return "MOLA_" + slotPrefix(p.Slot) + "_API_KEY" }
func slotModel(p Provider) string { return "MOLA_" + slotPrefix(p.Slot) + "_MODEL" }

// writeEnv merges values into path and drops cleared names. Everything else
// in the file, comments included, stays where it was.
func writeEnv(path string, values map[string]string, clear []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var lines []string
	if raw, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	clearSet := map[string]bool{}
	for _, name := range clear {
		if !envName.MatchString(name) {
			return fmt.Errorf("%q is not an environment name", name)
		}
		clearSet[name] = true
	}
	for name := range values {
		if !envName.MatchString(name) {
			return fmt.Errorf("%q is not an environment name", name)
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(lines)+len(values))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out = append(out, line)
			continue
		}
		name, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			out = append(out, line)
			continue
		}
		name = strings.TrimSpace(name)
		if clearSet[name] {
			continue
		}
		if v, ok := values[name]; ok {
			out = append(out, name+"="+envQuote(v))
			seen[name] = true
			continue
		}
		out = append(out, line)
	}
	extra := make([]string, 0, len(values))
	for name := range values {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		out = append(out, name+"="+envQuote(values[name]))
	}
	data := strings.Join(out, "\n") + "\n"
	tmp, err := os.CreateTemp(filepath.Dir(path), ".env-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(data); err != nil {
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

// envQuote leaves a plain value as it is and quotes one that would otherwise
// be read as a comment or lose its edges.
func envQuote(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t#\"'\\") {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}
