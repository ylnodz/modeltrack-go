package provider

import (
	"context"
	"strings"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/httpclient"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// OpenRouterProvider tracks OpenRouter's public catalog.
// Its /models endpoint is public, so the key is optional.
type OpenRouterProvider struct {
	Base
}

// NewOpenRouter builds the OpenRouter provider.
func NewOpenRouter(cfg config.Config) *OpenRouterProvider {
	return &OpenRouterProvider{Base: Base{
		KeyName:    "openrouter",
		LabelName:  "OpenRouter",
		EnvKeyList: nil, // public endpoint
		Filter:     true,
		WatchCost:  true,
		WatchCtx:   true,
		Config:     cfg,
	}}
}

// Configured is always true: the endpoint is public.
func (p *OpenRouterProvider) Configured() bool { return true }

// Fetch pulls and normalizes the OpenRouter catalog.
func (p *OpenRouterProvider) Fetch(ctx context.Context) ([]model.Model, error) {
	headers := map[string]string{}
	if key := p.KeyFor("OPENROUTER_API_KEY"); key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	data, err := httpclient.GetJSON(ctx, "https://openrouter.ai/api/v1/models", headers, nil)
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
			// OpenRouter prices are USD per token → scale to per 1M.
			pIn = model.ScalePrice(model.ParsePrice(pricing["prompt"]), 1_000_000)
			pOut = model.ScalePrice(model.ParsePrice(pricing["completion"]), 1_000_000)
		}
		isFree := strings.HasSuffix(mid, ":free") ||
			(pIn != nil && pOut != nil && *pIn == 0 && *pOut == 0)

		author := "OpenRouter"
		if tp, ok := raw["top_provider"].(map[string]any); ok {
			if n := model.AsString(tp["name"]); n != "" {
				author = n
			}
		}
		note := ""
		if isFree {
			note = "free tier on OpenRouter"
		}
		extra := map[string]any(nil)
		if arch, ok := raw["architecture"].(map[string]any); ok && len(arch) > 0 {
			extra = map[string]any{"architecture": arch}
		}

		out = append(out, model.Model{
			Source:      p.KeyName,
			PK:          mid,
			Name:        firstNonEmpty(model.AsString(raw["name"]), mid),
			Author:      author,
			URL:         "https://openrouter.ai/models/" + mid,
			IsFree:      isFree,
			FreeNote:    note,
			Context:     model.AsInt(raw["context_length"]),
			Modalities:  model.DetectModalities(raw),
			PriceIn:     pIn,
			PriceOut:    pOut,
			Description: model.AsString(raw["description"]),
			Extra:       extra,
		})
	}
	return out, nil
}
