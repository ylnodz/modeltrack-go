package provider

import (
	"context"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// Spec describes one provider that speaks the OpenAI /v1/models dialect verbatim.
type Spec struct {
	Key      string
	Label    string
	EnvKey   string
	BaseURL  string
	ModelURL string
	FreeNote string
	// ZeroPriceFree: is_free requires both prices known and zero.
	ZeroPriceFree bool
}

// thinProvider adapts a Spec to the Provider interface.
type thinProvider struct {
	Base
	spec Spec
}

// newThin builds a provider from a Spec.
func newThin(cfg config.Config, s Spec) *thinProvider {
	b := Base{
		KeyName:    s.Key,
		LabelName:  s.Label,
		EnvKeyList: []string{s.EnvKey},
		Config:     cfg,
		FreeNoteFn: func(map[string]any) string { return s.FreeNote },
		ModelURLFn: func(string) string { return s.ModelURL },
	}
	if !s.ZeroPriceFree {
		b.FreeRuleFn = func(map[string]any, *float64, *float64) bool { return true }
	}
	return &thinProvider{Base: b, spec: s}
}

// Fetch pulls the provider's OpenAI-compatible model list.
func (p *thinProvider) Fetch(ctx context.Context) ([]model.Model, error) {
	return p.OpenAICompatible(ctx, p.spec.BaseURL, p.KeyFor(p.spec.EnvKey),
		"Authorization", "Bearer ", nil, nil, 1.0)
}
