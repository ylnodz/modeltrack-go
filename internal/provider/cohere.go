package provider

import (
	"context"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/httpclient"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// CohereProvider tracks Cohere's chat-capable models.
type CohereProvider struct {
	Base
}

// NewCohere builds the Cohere provider.
func NewCohere(cfg config.Config) *CohereProvider {
	return &CohereProvider{Base: Base{
		KeyName:    "cohere",
		LabelName:  "Cohere",
		EnvKeyList: []string{"COHERE_API_KEY"},
		Filter:     false,
		WatchCtx:   true,
		Config:     cfg,
	}}
}

// Fetch pulls the Cohere model list, keeping chat endpoints.
func (p *CohereProvider) Fetch(ctx context.Context) ([]model.Model, error) {
	key := p.KeyFor("COHERE_API_KEY")
	data, err := httpclient.GetJSON(ctx, "https://api.cohere.com/v1/models",
		map[string]string{"Authorization": "Bearer " + key}, nil)
	if err != nil {
		return nil, err
	}
	m, _ := data.(map[string]any)
	if m == nil {
		return nil, nil
	}
	items, _ := m["models"].([]any)

	out := make([]model.Model, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		mid := model.AsString(raw["name"])
		if mid == "" {
			continue
		}
		if endpoints, ok := raw["endpoints"].([]any); ok && len(endpoints) > 0 {
			hasChat := false
			for _, e := range endpoints {
				if model.AsString(e) == "chat" {
					hasChat = true
				}
			}
			if !hasChat {
				continue
			}
		}
		out = append(out, model.Model{
			Source:   p.KeyName,
			PK:       mid,
			Name:     mid,
			Author:   "Cohere",
			URL:      "https://docs.cohere.com/docs/models",
			IsFree:   true,
			FreeNote: "free trial tier (non-commercial)",
			Context:  model.AsInt(raw["context_length"]),
		})
	}
	return out, nil
}
