package provider

import (
	"context"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/httpclient"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// OllamaCloudProvider tracks Ollama Cloud tags.
type OllamaCloudProvider struct {
	Base
}

// NewOllama builds the Ollama Cloud provider.
func NewOllama(cfg config.Config) *OllamaCloudProvider {
	return &OllamaCloudProvider{Base: Base{
		KeyName:    "ollama",
		LabelName:  "Ollama Cloud",
		EnvKeyList: []string{"OLLAMA_CLOUD_API_KEY"},
		Filter:     false,
		Config:     cfg,
	}}
}

// Fetch pulls the Ollama Cloud tag list.
func (p *OllamaCloudProvider) Fetch(ctx context.Context) ([]model.Model, error) {
	base := trimmed(p.Config.OllamaCloudBaseURL)
	key := p.KeyFor("OLLAMA_CLOUD_API_KEY")

	data, err := httpclient.GetJSON(ctx, base+"/api/tags", map[string]string{"Authorization": "Bearer " + key}, nil)
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
		mid := firstNonEmpty(model.AsString(raw["name"]), model.AsString(raw["model"]))
		if mid == "" {
			continue
		}
		short := mid
		if idx := indexByte(mid, ':'); idx >= 0 {
			short = mid[:idx]
		}
		var extra map[string]any
		if details, ok := raw["details"].(map[string]any); ok && len(details) > 0 {
			extra = map[string]any{"details": details}
		}
		out = append(out, model.Model{
			Source:      p.KeyName,
			PK:          mid,
			Name:        mid,
			Author:      "Ollama",
			URL:         base + "/library/" + short,
			IsFree:      true,
			FreeNote:    "free within Ollama Cloud usage tier",
			Description: model.AsString(raw["description"]),
			Extra:       extra,
		})
	}
	return out, nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
