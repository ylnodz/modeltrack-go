package provider

import (
	"context"
	"strings"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/httpclient"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

// NIMProvider tracks NVIDIA NIM. Free endpoints come from NGC metadata.
type NIMProvider struct {
	Base
}

// NewNIM builds the NVIDIA NIM provider.
func NewNIM(cfg config.Config) *NIMProvider {
	return &NIMProvider{Base: Base{
		KeyName:    "nim",
		LabelName:  "NVIDIA NIM",
		EnvKeyList: []string{"NVIDIA_API_KEY"},
		Filter:     true,
		WatchCtx:   false,
		Config:     cfg,
	}}
}

// Fetch pulls the NIM catalog plus the NGC free-endpoint labels.
func (p *NIMProvider) Fetch(ctx context.Context) ([]model.Model, error) {
	headers := map[string]string{"Authorization": "Bearer " + p.KeyFor("NVIDIA_API_KEY")}
	data, err := httpclient.GetJSON(ctx, "https://integrate.api.nvidia.com/v1/models", headers, nil)
	if err != nil {
		return nil, err
	}
	m, _ := data.(map[string]any)
	if m == nil {
		return nil, nil
	}
	items, _ := m["data"].([]any)
	if len(items) == 0 {
		return nil, nil
	}

	// Free-endpoint metadata is best-effort; failure degrades to all-paid.
	freeMap := map[string]bool{}
	if epData, err := httpclient.GetJSON(ctx, "https://api.ngc.nvidia.com/v2/endpoints", nil, map[string]string{"page-size": "1000"}); err == nil {
		if em, ok := epData.(map[string]any); ok {
			if endpoints, ok := em["artifacts"].([]any); ok {
				for _, e := range endpoints {
					ep, ok := e.(map[string]any)
					if !ok {
						continue
					}
					isFree := false
					if labels, ok := ep["labels"].([]any); ok {
						for _, l := range labels {
							if model.AsString(l) == "Free Endpoint" {
								isFree = true
							}
						}
					}
					for _, field := range []string{"name", "displayName"} {
						if v := model.AsString(ep[field]); v != "" {
							freeMap[v] = isFree
						}
					}
				}
			}
		}
	}

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
		short := mid
		if idx := strings.LastIndex(mid, "/"); idx >= 0 {
			short = mid[idx+1:]
		}
		isFree := freeMap[mid] || freeMap[short]
		note := ""
		if isFree {
			note = "free endpoint available"
		}
		out = append(out, model.Model{
			Source:   p.KeyName,
			PK:       mid,
			Name:     mid,
			Author:   firstNonEmpty(model.AsString(raw["owned_by"]), "NVIDIA"),
			URL:      "https://build.nvidia.com/" + mid,
			IsFree:   isFree,
			FreeNote: note,
		})
	}
	return out, nil
}
