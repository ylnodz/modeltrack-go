// Package model defines the normalized model representation shared by every
// provider, plus the shared price/modality parsing rules.
package model

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Model is one catalog entry from one provider.
type Model struct {
	Source      string
	PK          string
	Name        string
	Author      string
	URL         string
	IsFree      bool
	FreeNote    string
	Context     *int
	Modalities  []string
	PriceIn     *float64
	PriceOut    *float64
	Description string
	Extra       map[string]any
}

// Key is the globally unique "source:pk" identity.
func (m Model) Key() string { return m.Source + ":" + m.PK }

// ModalitiesString renders modalities as a comma-joined list for storage.
func (m Model) ModalitiesString() string {
	if len(m.Modalities) == 0 {
		return "text"
	}
	return strings.Join(m.Modalities, ",")
}

// ExtraJSON renders the provider-specific extras as compact JSON.
func (m Model) ExtraJSON() string {
	if len(m.Extra) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m.Extra)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ParsePrice converts a provider price into USD-per-1M-tokens, or nil when
// unavailable. Handles OpenRouter's per-token strings and the sentinel -1.
func ParsePrice(value any) *float64 {
	if value == nil {
		return nil
	}
	num, ok := toFloat(value)
	if !ok {
		return nil
	}
	if num < 0 {
		return nil
	}
	return &num
}

// ScalePrice multiplies a price by a factor (per-token → per-1M).
func ScalePrice(p *float64, factor float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p * factor
	return &v
}

// DetectModalities best-effort detects input modalities from a provider payload.
func DetectModalities(raw map[string]any) []string {
	mods := []string{"text"}
	seen := map[string]bool{"text": true}
	if arch, ok := raw["architecture"].(map[string]any); ok {
		if inputs, ok := arch["input_modalities"].([]any); ok {
			for _, m := range inputs {
				s, ok := m.(string)
				if ok && s != "" && !seen[s] {
					mods = append(mods, s)
					seen[s] = true
				}
			}
		}
	}
	for _, key := range []string{"image", "audio", "video"} {
		if truthy(raw["supports_"+key]) || truthy(raw[key+"_input"]) {
			if !seen[key] {
				mods = append(mods, key)
				seen[key] = true
			}
		}
	}
	return mods
}

// AsInt converts a JSON scalar into *int when possible.
func AsInt(v any) *int {
	switch t := v.(type) {
	case nil:
		return nil
	case int:
		return &t
	case int64:
		n := int(t)
		return &n
	case float64:
		n := int(t)
		return &n
	case json.Number:
		n, err := strconv.Atoi(t.String())
		if err != nil {
			f, ferr := t.Float64()
			if ferr != nil {
				return nil
			}
			n = int(f)
		}
		return &n
	case string:
		if t == "" {
			return nil
		}
		n, err := strconv.Atoi(t)
		if err != nil {
			return nil
		}
		return &n
	}
	return nil
}

// AsString converts a JSON scalar into a string.
func AsString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	return 0, false
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != "" && t != "false"
	case nil:
		return false
	}
	return true
}
