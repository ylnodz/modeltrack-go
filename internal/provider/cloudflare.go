package provider

import (
	"context"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/httpclient"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// CloudflareProvider tracks Cloudflare Workers AI.
// Free within the daily neuron allocation, not unconditionally $0.
type CloudflareProvider struct {
	Base
}

// NewCloudflare builds the Cloudflare Workers AI provider.
func NewCloudflare(cfg config.Config) *CloudflareProvider {
	return &CloudflareProvider{Base: Base{
		KeyName:    "cloudflare",
		LabelName:  "Cloudflare Workers AI",
		EnvKeyList: []string{"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ACCOUNT_ID"},
		Filter:     false,
		WatchCost:  false,
		Config:     cfg,
	}}
}

// Fetch pulls the Workers AI model search results.
func (p *CloudflareProvider) Fetch(ctx context.Context) ([]model.Model, error) {
	token := p.KeyFor("CLOUDFLARE_API_TOKEN")
	account := p.KeyFor("CLOUDFLARE_ACCOUNT_ID")
	url := "https://api.cloudflare.com/client/v4/accounts/" + account + "/ai/models/search"

	data, err := httpclient.GetJSON(ctx, url, map[string]string{"Authorization": "Bearer " + token}, nil)
	if err != nil {
		return nil, err
	}

	items := cfItems(data)
	out := make([]model.Model, 0, len(items))
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		mid := firstNonEmpty(model.AsString(raw["id"]), model.AsString(raw["name"]))
		if mid == "" {
			continue
		}
		out = append(out, model.Model{
			Source:      p.KeyName,
			PK:          mid,
			Name:        firstNonEmpty(model.AsString(raw["name"]), mid),
			Author:      "Cloudflare",
			URL:         "https://developers.cloudflare.com/workers-ai/models/" + mid + "/",
			IsFree:      true,
			FreeNote:    "free within 10k neurons/day",
			Context:     model.AsInt(raw["required_input_size"]),
			Description: model.AsString(raw["description"]),
		})
	}
	return out, nil
}

func cfItems(data any) []any {
	if m, ok := data.(map[string]any); ok {
		for _, key := range []string{"result", "models"} {
			if list, ok := m[key].([]any); ok {
				return list
			}
		}
	}
	if list, ok := data.([]any); ok {
		return list
	}
	return nil
}
