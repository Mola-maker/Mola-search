// Package detective is the Detective page: one query in, live multi-source
// evidence, a verdict (or an honest ABSTAIN), the 30 search modes, the
// Yuanbao kill-switch proof and the stakeout watches.
package detective

import "github.com/yetone/magpie/internal/pluginrt"

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "detective", API: "^1", Requires: []string{"window"},
		Name: "Detective",
		Sub:  "Live multi-source investigation: 30 search modes, verdict or ABSTAIN, temporal tree, multimodal checks and stakeout watches",
		One:  "1 stakeout watch", Many: "{n} stakeout watches",
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "detective"}},
		Client: []string{"i18n.js", "client.js", "detective.css"},
	})
}
