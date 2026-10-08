package provider

import (
	"context"
	"strings"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/httpclient"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// GeminiProvider tracks Google AI Studio (Gemini API).
type GeminiProvider struct {
	Base
}

// NewGemini builds the Google AI Studio provider.
func NewGemini(cfg config.Config) *GeminiProvider {
	return &GeminiProvider{Base: Base{
		KeyName:    "gemini",
		LabelName:  "Google AI Studio",
		EnvKeyList: []string{"GEMINI_API_KEY"},
		Filter:     false,
		WatchCtx:   true,
		Config:     cfg,
	}}
}

// Fetch pulls the Gemini model list, keeping generateContent-capable models.
func (p *GeminiProvider) Fetch(ctx context.Context) ([]model.Model, error) {
	key := p.KeyFor("GEMINI_API_KEY")
	data, err := httpclient.GetJSON(ctx,
		"https://generativelanguage.googleapis.com/v1beta/models", nil,
		map[string]string{"key": key, "pageSize": "1000"})
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
		full := model.AsString(raw["name"])
		if full == "" {
			continue
		}
		mid := full
		if idx := strings.Index(full, "/"); idx >= 0 {
			mid = full[idx+1:]
		}
		if methods := modelMethods(raw["supportedGenerationMethods"]); len(methods) > 0 && !methods["generateContent"] {
			continue
		}
		out = append(out, model.Model{
			Source:      p.KeyName,
			PK:          mid,
			Name:        firstNonEmpty(model.AsString(raw["displayName"]), mid),
			Author:      "Google",
			URL:         "https://ai.google.dev/gemini-api/docs/models",
			IsFree:      true,
			FreeNote:    "free tier (RPM/RPD varies by model)",
			Context:     model.AsInt(raw["inputTokenLimit"]),
			Modalities:  []string{"text"},
			Description: model.AsString(raw["description"]),
		})
	}
	return out, nil
}

func modelMethods(v any) map[string]bool {
	out := map[string]bool{}
	if list, ok := v.([]any); ok {
		for _, item := range list {
			if s := model.AsString(item); s != "" {
				out[s] = true
			}
		}
	}
	return out
}
