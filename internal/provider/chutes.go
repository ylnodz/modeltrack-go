package provider

import (
	"context"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/httpclient"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// ChutesProvider tracks Chutes.ai, whose prices arrive per token.
type ChutesProvider struct {
	Base
}

// NewChutes builds the Chutes.ai provider.
func NewChutes(cfg config.Config) *ChutesProvider {
	return &ChutesProvider{Base: Base{
		KeyName:    "chutes",
		LabelName:  "Chutes.ai",
		EnvKeyList: []string{"CHUTES_API_KEY"},
		Filter:     true,
		WatchCost:  true,
		Config:     cfg,
	}}
}

// Fetch pulls the Chutes model list.
func (p *ChutesProvider) Fetch(ctx context.Context) ([]model.Model, error) {
	key := p.KeyFor("CHUTES_API_KEY")
	data, err := httpclient.GetJSON(ctx, "https://llm.chutes.ai/v1/models",
		map[string]string{"Authorization": "Bearer " + key}, nil)
	if err != nil {
		return nil, err
	}
	m, _ := data.(map[string]any)
	if m == nil {
		return nil, nil
	}
	items, _ := m["data"].([]any)

	out := make([]model.Model, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		mid := model.AsString(raw["id"])
		if mid == "" {
			continue
		}
		var pricing map[string]any
		if pv, ok := raw["pricing"].(map[string]any); ok {
			pricing = pv
		}
		var pIn, pOut *float64
		if pricing != nil {
			pIn = model.ScalePrice(model.ParsePrice(pricing["prompt"]), 1_000_000)
			pOut = model.ScalePrice(model.ParsePrice(pricing["completion"]), 1_000_000)
		}
		// Free when prices are known and zero; conservative default otherwise.
		isFree := true
		if pIn != nil && pOut != nil {
			isFree = *pIn == 0 && *pOut == 0
		}
		out = append(out, model.Model{
			Source:      p.KeyName,
			PK:          mid,
			Name:        mid,
			Author:      firstNonEmpty(model.AsString(raw["owned_by"]), "Chutes"),
			URL:         "https://chutes.ai/app",
			IsFree:      isFree,
			FreeNote:    "free tier (rate-limited)",
			Context:     model.AsInt(raw["context_length"]),
			PriceIn:     pIn,
			PriceOut:    pOut,
			Description: model.AsString(raw["description"]),
		})
	}
	return out, nil
}
