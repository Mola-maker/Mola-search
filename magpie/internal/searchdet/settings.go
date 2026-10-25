// Every gate and threshold an investigation uses, in one place the settings
// page can read and write. The defaults are the engine's old constants; a
// key the saved file leaves out keeps its default, and a key it names is
// applied as written, so 0 stays 0 for the knobs whose range allows it.
package searchdet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Settings are the knobs behind the gates. Ranges are enforced on save and
// on load: a value outside its range is refused, not clamped silently.
type Settings struct {
	// TopK is how many citations a verdict may see.
	TopK int `json:"topK"`
	// PerChannel is how many results one channel may return per query.
	PerChannel int `json:"perChannel"`
	// MergeCap is the deduplicated pool size before ranking.
	MergeCap int `json:"mergeCap"`
	// FoldGamma is how much a day of age discounts a result (0..1).
	FoldGamma float64 `json:"foldGamma"`
	// ChannelTimeoutMs is one channel's network deadline.
	ChannelTimeoutMs int `json:"channelTimeoutMs"`
	// FreshDays is the FreshOnly window; an undated result is kept.
	FreshDays int `json:"freshDays"`
	// AuthorityHigh is the level counted as authoritative.
	AuthorityHigh int `json:"authorityHigh"`
	// AuthorityLow is the level counted as low quality.
	AuthorityLow int `json:"authorityLow"`
	// VerdictMinSignals is how many refute/confirm signals a verdict needs.
	VerdictMinSignals int `json:"verdictMinSignals"`
	// SupportFloor is the minimum query-token coverage before a verdict.
	SupportFloor float64 `json:"supportFloor"`
	// BreakerFails opens a channel's breaker after this many consecutive errors.
	BreakerFails int `json:"breakerFails"`
	// BreakerCooldownSec is how long an open breaker stays open.
	BreakerCooldownSec int `json:"breakerCooldownSec"`
	// GapRoundsCap caps a mode's AREX gap rounds.
	GapRoundsCap int `json:"gapRoundsCap"`
	// NoveltyRoundsCap caps RAAC's expansion rounds.
	NoveltyRoundsCap int `json:"noveltyRoundsCap"`
	// DisabledChannels are provider ids the user turned off.
	DisabledChannels []string `json:"disabledChannels"`
	// AnswerEnabled asks the answer model to state the answer held by the
	// evidence after a run. Off keeps the evidence-only envelope.
	AnswerEnabled bool `json:"answerEnabled"`
	// AnswerModel is the chat model that synthesis uses.
	AnswerModel string `json:"answerModel"`
}

// DefaultSettings is the engine's behavior before anyone changes a knob.
func DefaultSettings() Settings {
	return Settings{
		TopK:               20,
		PerChannel:         8,
		MergeCap:           40,
		FoldGamma:          0.35,
		ChannelTimeoutMs:   12000,
		FreshDays:          30,
		AuthorityHigh:      4,
		AuthorityLow:       1,
		VerdictMinSignals:  2,
		SupportFloor:       0.08,
		BreakerFails:       3,
		BreakerCooldownSec: 600,
		GapRoundsCap:       3,
		NoveltyRoundsCap:   3,
		DisabledChannels:   []string{},
		AnswerEnabled:      true,
		AnswerModel:        "deepseek-chat",
	}
}

// SettingsFile is where the knobs live.
func SettingsFile() string { return filepath.Join(DataDir(), "search_settings.json") }

// LoadSettings reads the file, filling fields the file left out with their
// default. A file that exists but cannot be read or parsed is an error: the
// caller decides whether to run on defaults, and the page shows why.
func LoadSettings() (Settings, error) {
	s := DefaultSettings()
	raw, err := os.ReadFile(SettingsFile())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var saved Settings
	if err := json.Unmarshal(raw, &saved); err != nil {
		return s, fmt.Errorf("%s: %w", SettingsFile(), err)
	}
	// Unmarshal alone cannot tell a saved 0 from a key the file never named,
	// and 0 is a real setting for foldGamma, supportFloor and the round caps.
	var named map[string]json.RawMessage
	if err := json.Unmarshal(raw, &named); err != nil {
		return s, fmt.Errorf("%s: %w", SettingsFile(), err)
	}
	mergeSettings(&s, saved, named)
	if err := s.Validate(); err != nil {
		return DefaultSettings(), fmt.Errorf("%s: %w", SettingsFile(), err)
	}
	return s, nil
}

// SaveSettings validates and writes through a temp file and rename.
func SaveSettings(s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	path := SettingsFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".search_settings-*.tmp")
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

// mergeSettings applies the keys the saved file actually names. Presence,
// not a non-zero value, decides: 0 is how a user turns folding off or stops
// every gap and novelty round, so it must survive a reload.
func mergeSettings(dst *Settings, src Settings, named map[string]json.RawMessage) {
	has := func(key string) bool {
		_, ok := named[key]
		return ok
	}
	if has("topK") {
		dst.TopK = src.TopK
	}
	if has("perChannel") {
		dst.PerChannel = src.PerChannel
	}
	if has("mergeCap") {
		dst.MergeCap = src.MergeCap
	}
	if has("foldGamma") {
		dst.FoldGamma = src.FoldGamma
	}
	if has("channelTimeoutMs") {
		dst.ChannelTimeoutMs = src.ChannelTimeoutMs
	}
	if has("freshDays") {
		dst.FreshDays = src.FreshDays
	}
	if has("authorityHigh") {
		dst.AuthorityHigh = src.AuthorityHigh
	}
	if has("authorityLow") {
		dst.AuthorityLow = src.AuthorityLow
	}
	if has("verdictMinSignals") {
		dst.VerdictMinSignals = src.VerdictMinSignals
	}
	if has("supportFloor") {
		dst.SupportFloor = src.SupportFloor
	}
	if has("breakerFails") {
		dst.BreakerFails = src.BreakerFails
	}
	if has("breakerCooldownSec") {
		dst.BreakerCooldownSec = src.BreakerCooldownSec
	}
	if has("gapRoundsCap") {
		dst.GapRoundsCap = src.GapRoundsCap
	}
	if has("noveltyRoundsCap") {
		dst.NoveltyRoundsCap = src.NoveltyRoundsCap
	}
	if has("answerEnabled") {
		dst.AnswerEnabled = src.AnswerEnabled
	}
	if has("answerModel") {
		dst.AnswerModel = src.AnswerModel
	}
	if has("disabledChannels") {
		dst.DisabledChannels = src.DisabledChannels
		if dst.DisabledChannels == nil {
			dst.DisabledChannels = []string{}
		}
	}
}

// Validate refuses a knob outside the range the engine can honor.
func (s Settings) Validate() error {
	check := func(ok bool, format string, args ...any) error {
		if !ok {
			return fmt.Errorf(format, args...)
		}
		return nil
	}
	if err := check(s.TopK >= 1 && s.TopK <= 100, "topK must be 1..100, got %d", s.TopK); err != nil {
		return err
	}
	if err := check(s.PerChannel >= 1 && s.PerChannel <= 20, "perChannel must be 1..20, got %d", s.PerChannel); err != nil {
		return err
	}
	if err := check(s.MergeCap >= s.TopK && s.MergeCap <= 500, "mergeCap must be >= topK and <= 500, got %d", s.MergeCap); err != nil {
		return err
	}
	if err := check(s.FoldGamma >= 0 && s.FoldGamma <= 1, "foldGamma must be 0..1, got %v", s.FoldGamma); err != nil {
		return err
	}
	if err := check(s.ChannelTimeoutMs >= 1000 && s.ChannelTimeoutMs <= 120000, "channelTimeoutMs must be 1000..120000, got %d", s.ChannelTimeoutMs); err != nil {
		return err
	}
	if err := check(s.FreshDays >= 1 && s.FreshDays <= 3650, "freshDays must be 1..3650, got %d", s.FreshDays); err != nil {
		return err
	}
	if err := check(s.AuthorityHigh >= 1 && s.AuthorityHigh <= 5, "authorityHigh must be 1..5, got %d", s.AuthorityHigh); err != nil {
		return err
	}
	if err := check(s.AuthorityLow >= 1 && s.AuthorityLow <= s.AuthorityHigh, "authorityLow must be 1..authorityHigh, got %d", s.AuthorityLow); err != nil {
		return err
	}
	if err := check(s.VerdictMinSignals >= 1 && s.VerdictMinSignals <= 10, "verdictMinSignals must be 1..10, got %d", s.VerdictMinSignals); err != nil {
		return err
	}
	if err := check(s.SupportFloor >= 0 && s.SupportFloor <= 1, "supportFloor must be 0..1, got %v", s.SupportFloor); err != nil {
		return err
	}
	if err := check(s.BreakerFails >= 1 && s.BreakerFails <= 20, "breakerFails must be 1..20, got %d", s.BreakerFails); err != nil {
		return err
	}
	if err := check(s.BreakerCooldownSec >= 10 && s.BreakerCooldownSec <= 3600, "breakerCooldownSec must be 10..3600, got %d", s.BreakerCooldownSec); err != nil {
		return err
	}
	if err := check(s.GapRoundsCap >= 0 && s.GapRoundsCap <= 5, "gapRoundsCap must be 0..5, got %d", s.GapRoundsCap); err != nil {
		return err
	}
	if err := check(s.NoveltyRoundsCap >= 0 && s.NoveltyRoundsCap <= 5, "noveltyRoundsCap must be 0..5, got %d", s.NoveltyRoundsCap); err != nil {
		return err
	}
	for _, id := range s.DisabledChannels {
		if channelByID(id) == nil {
			return fmt.Errorf("disabledChannels: unknown provider %q", id)
		}
	}
	return nil
}

// ChannelTimeout is the per-channel deadline as a duration.
func (s Settings) ChannelTimeout() time.Duration {
	return time.Duration(s.ChannelTimeoutMs) * time.Millisecond
}

// Disabled reports whether the user turned a provider off.
func (s Settings) Disabled(id string) bool {
	for _, d := range s.DisabledChannels {
		if d == id {
			return true
		}
	}
	return false
}
