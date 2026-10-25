// The engine's side of the modality checks: which cards a mode asks for,
// and how an uploaded image or a page URL gets to the check that reads it.
// The checks themselves live in multimodal.go; this file only decides and
// collects, so a failing modality never fails the investigation.
package searchdet

import (
	"context"
	"log"
)

// ImageCard is the uploaded-image check as the window calls it.
func ImageCard(name string, data []byte) (MultimodalCard, error) {
	return imageCardFromBytes(name, data)
}

// multimodal runs every check the plan asked for and returns the cards that
// produced something. A check that fails is logged and skipped: the text
// evidence stands on its own.
func (e *Engine) multimodal(ctx context.Context, opt Options, keys Keys, plan modePlan, cites []Citation) []MultimodalCard {
	if len(plan.Multimodal) == 0 {
		return []MultimodalCard{}
	}
	var cards []MultimodalCard
	for _, want := range plan.Multimodal {
		cctx, cancel := httpDeadline(ctx)
		switch want {
		case "weather":
			if card, ok := weatherCard(cctx, opt.Query, keys); ok {
				cards = append(cards, card)
			}
		case "image":
			if len(opt.ImageData) > 0 {
				card, err := imageCardFromBytes(opt.ImageName, opt.ImageData)
				if err != nil {
					log.Printf("detective: image card: %v", err)
				} else {
					cards = append(cards, card)
				}
			}
		case "video":
			if card, ok := videoCard(cctx, opt.Query, keys); ok {
				cards = append(cards, card)
			}
		case "dom":
			pageURL := opt.PageURL
			if pageURL == "" && len(cites) > 0 {
				pageURL = cites[0].URL
			}
			if pageURL != "" {
				card, err := domSnapshot(cctx, pageURL)
				if err != nil {
					log.Printf("detective: dom snapshot: %v", err)
				} else {
					cards = append(cards, card)
				}
			}
			if opt.EnableCU {
				if card, ok := adbSnapshot(cctx); ok {
					cards = append(cards, card)
				}
			}
		case "adb":
			if card, ok := adbSnapshot(cctx); ok {
				cards = append(cards, card)
			}
		}
		cancel()
	}
	if cards == nil {
		cards = []MultimodalCard{}
	}
	return cards
}
