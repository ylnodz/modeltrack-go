// Package provider defines the provider contract and shared helpers.
package provider

import (
	"context"
	"fmt"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/httpclient"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// Provider is one cloud LLM API provider.
//
// Fetch returns the provider's current catalog. IsFree must never blanket
// assert true without a documented free-tier policy.
type Provider interface {
	Key() string
	Label() string
	EnvKeys() []string
	FilterFree() bool
	WatchPrice() bool
	WatchContext() bool
	Configured() bool
	Fetch(ctx context.Context) ([]model.Model, error)
}

// Base carries the metadata and helpers every provider shares.
type Base struct {
	KeyName    string
	LabelName  string
	EnvKeyList []string
	Filter     bool
	WatchCost  bool
	WatchCtx   bool
	Config     config.Config

	// FreeRuleFn overrides the default free rule when non-nil.
	FreeRuleFn func(raw map[string]any, in, out *float64) bool
	// FreeNoteFn supplies the provider-specific free-tier note when non-nil.
	FreeNoteFn func(raw map[string]any) string
	// ModelURLFn supplies a provider-specific model page URL when non-nil.
	ModelURLFn func(pk string) string
}

func (b Base) Key() string        { return b.KeyName }
func (b Base) Label() string      { return b.LabelName }
func (b Base) EnvKeys() []string  { return b.EnvKeyList }
func (b Base) FilterFree() bool   { return b.Filter }
func (b Base) WatchPrice() bool   { return b.WatchCost }
func (b Base) WatchContext() bool { return b.WatchCtx }

// Configured reports whether every declared env key is present.
func (b Base) Configured() bool {
	for _, k := range b.EnvKeyList {
		if b.Config.Get(k) == "" {
			return false
		}
	}
	return true
}

// KeyFor reads one env value through the config.
func (b Base) KeyFor(env string) string { return b.Config.Get(env) }

// ModelURL is overridable per provider for a direct model page.
func (b Base) ModelURL(pk string) string {
	if b.ModelURLFn != nil {
		return b.ModelURLFn(pk)
	}
	return ""
}

// DefaultIsFree: free when both prices are known and zero.
func (b Base) DefaultIsFree(_ map[string]any, in, out *float64) bool {
	if in == nil || out == nil {
		return false
	}
	return *in == 0 && *out == 0
}

// FreeNote default returns "".
func (b Base) FreeNote(map[string]any) string { return "" }

// OpenAICompatible fetches a standard {"data":[...]} model list.
func (b Base) OpenAICompatible(
	ctx context.Context,
	baseURL string,
	apiKey string,
	authHeader string,
	authPrefix string,
	extraHeaders map[string]string,
	params map[string]string,
	priceScale float64,
) ([]model.Model, error) {
	headers := map[string]string{}
	for k, v := range extraHeaders {
		headers[k] = v
	}
	if apiKey != "" {
		headers[authHeader] = authPrefix + apiKey
	}

	data, err := httpclient.GetJSON(ctx, trimmed(baseURL)+"/models", headers, params)
	if err != nil {
		return nil, err
	}

	items := extractItems(data)
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
		var pricing map[string]any
		if p, ok := raw["pricing"].(map[string]any); ok {
			pricing = p
		}
		var pIn, pOut *float64
		if pricing != nil {
			pIn = model.ParsePrice(pricing["prompt"])
			pOut = model.ParsePrice(pricing["completion"])
		}
		if priceScale != 1.0 {
			pIn = model.ScalePrice(pIn, priceScale)
			pOut = model.ScalePrice(pOut, priceScale)
		}

		out = append(out, model.Model{
			Source:      b.KeyName,
			PK:          mid,
			Name:        firstNonEmpty(model.AsString(raw["name"]), model.AsString(raw["display_name"]), mid),
			Author:      firstNonEmpty(model.AsString(raw["owned_by"]), model.AsString(raw["provider"]), b.LabelName),
			URL:         b.ModelURL(mid),
			IsFree:      isFreeHook(b, raw, pIn, pOut),
			FreeNote:    freeNoteHook(b, raw),
			Context:     model.AsInt(firstNonNil(raw["context_length"], raw["context_window"])),
			Modalities:  model.DetectModalities(raw),
			PriceIn:     pIn,
			PriceOut:    pOut,
			Description: model.AsString(raw["description"]),
			Extra:       extraFields(raw),
		})
	}
	return out, nil
}

// FreeRule is a small interface for providers overriding the free rule.
type FreeRule interface {
	IsFree(raw map[string]any, in, out *float64) bool
}

func isFreeHook(b Base, raw map[string]any, in, out *float64) bool {
	if b.FreeRuleFn != nil {
		return b.FreeRuleFn(raw, in, out)
	}
	return b.DefaultIsFree(raw, in, out)
}

func freeNoteHook(b Base, raw map[string]any) string {
	if b.FreeNoteFn != nil {
		return b.FreeNoteFn(raw)
	}
	return b.FreeNote(raw)
}

// ── shared helpers ────────────────────────────────────────────

func extractItems(data any) []any {
	switch t := data.(type) {
	case map[string]any:
		for _, key := range []string{"data", "models"} {
			if list, ok := t[key].([]any); ok {
				return list
			}
		}
		if _, ok := t["result"]; ok {
			return nil
		}
	case []any:
		return t
	}
	return nil
}

func extraFields(raw map[string]any) map[string]any {
	keep := map[string]any{}
	for _, k := range []string{"details", "architecture", "object", "created", "max_tokens", "deprecation"} {
		if v, ok := raw[k]; ok && v != nil && v != "" {
			keep[k] = v
		}
	}
	if len(keep) == 0 {
		return nil
	}
	return keep
}

func trimmed(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// Bearer builds an Authorization header map.
func Bearer(key string) map[string]string {
	if key == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + key}
}

// MustItems returns the []any under a key or nil.
func MustItems(data any, key string) []any {
	if m, ok := data.(map[string]any); ok {
		if list, ok := m[key].([]any); ok {
			return list
		}
	}
	return nil
}

// AsMap coerces a JSON object.
func AsMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// AsList coerces a JSON array.
func AsList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// ErrUnavailable marks a provider that cannot fetch right now.
var ErrUnavailable = fmt.Errorf("provider unavailable")
