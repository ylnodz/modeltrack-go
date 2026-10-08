package provider

import (
	"github.com/ylnodz/modeltrack-go/internal/config"
)

// Build returns every known provider, sorted by key.
func Build(cfg config.Config) []Provider {
	list := []Provider{
		NewOpenRouter(cfg),
		NewNIM(cfg),
		NewCloudflare(cfg),
		NewOllama(cfg),
		NewGemini(cfg),
		NewMistral(cfg),
		NewCohere(cfg),
		NewChutes(cfg),
		newThin(cfg, Spec{
			Key: "groq", Label: "Groq", EnvKey: "GROQ_API_KEY",
			BaseURL:  "https://api.groq.com/openai/v1",
			ModelURL: "https://console.groq.com/docs/models",
			FreeNote: "free tier (rate-limited)",
		}),
		newThin(cfg, Spec{
			Key: "huggingface", Label: "HuggingFace Inference", EnvKey: "HUGGINGFACE_API_KEY",
			BaseURL:  "https://router.huggingface.co/v1",
			ModelURL: "https://huggingface.co/",
			FreeNote: "free within Inference Providers routing limits",
		}),
		newThin(cfg, Spec{
			Key: "siliconflow", Label: "SiliconFlow", EnvKey: "SILICONFLOW_API_KEY",
			BaseURL:  "https://api.siliconflow.cn/v1",
			ModelURL: "https://cloud.siliconflow.cn/models",
			FreeNote: "free tier (subset of models, rate-limited)",
		}),
		newThin(cfg, Spec{
			Key: "zai", Label: "Z.AI", EnvKey: "ZAI_API_KEY",
			BaseURL:  "https://api.z.ai/api/paas/v4",
			ModelURL: "https://docs.z.ai/guides/overview/pricing",
			FreeNote: "free tier (GLM Flash series)",
		}),
		newThin(cfg, Spec{
			Key: "sambanova", Label: "SambaNova Cloud", EnvKey: "SAMBANOVA_API_KEY",
			BaseURL:  "https://api.sambanova.ai/v1",
			ModelURL: "https://cloud.sambanova.ai/",
			FreeNote: "free tier (rate-limited)",
		}),
		newThin(cfg, Spec{
			Key: "nebius", Label: "Nebius Token Factory", EnvKey: "NEBIUS_API_KEY",
			BaseURL:  "https://api.studio.nebius.com/v1",
			ModelURL: "https://studio.nebius.com/",
			FreeNote: "free tier (rate-limited)", ZeroPriceFree: true,
		}),
	}
	sortByKey(list)
	return list
}

// Unconfigured returns the keys with missing credentials.
func Unconfigured(cfg config.Config) []string {
	var out []string
	for _, p := range Build(cfg) {
		if !p.Configured() {
			out = append(out, p.Key())
		}
	}
	return out
}

func sortByKey(list []Provider) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].Key() < list[j-1].Key(); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
