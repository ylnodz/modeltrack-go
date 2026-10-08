package provider

import (
	"context"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/httpclient"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// MistralProvider tracks Mistral La Plateforme.
type MistralProvider struct {
	Base
}

// NewMistral builds the Mistral provider.
func NewMistral(cfg config.Config) *MistralProvider {
	return &MistralProvider{Base: Base{
		KeyName:    "mistral",
		LabelName:  "Mistral La Plateforme",
		EnvKeyList: []string{"MISTRAL_API_KEY"},
		Filter:     false,
		WatchCtx:   true,
		Config:     cfg,
	}}
}

// Fetch pulls the Mistral model list.
func (p *MistralProvider) Fetch(ctx context.Context) ([]model.Model, error) {
	key := p.KeyFor("MISTRAL_API_KEY")
	data, err := httpclient.GetJSON(ctx, "https://api.mistral.ai/v1/models",
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
		out = append(out, model.Model{
			Source:      p.KeyName,
			PK:          mid,
			Name:        mid,
			Author:      "Mistral AI",
			URL:         "https://docs.mistral.ai/getting-started/models/",
			IsFree:      true,
			FreeNote:    "free Experiment tier (data used for training)",
			Context:     model.AsInt(raw["max_context_length"]),
			Description: model.AsString(raw["description"]),
		})
	}
	return out, nil
}
