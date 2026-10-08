// Package config loads runtime configuration from the environment.
//
// Every provider key is optional. Only the two Discord credentials are
// required for the bot to run; a provider with a missing key is reported as
// "unconfigured" and skipped during tracking cycles.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config mirrors the Python ModelTrack configuration 1:1.
type Config struct {
	// Discord (required)
	DiscordBotToken  string
	DiscordChannelID string
	DiscordGuildID   string

	// Runtime
	Port            int
	DBPath          string
	TrackInterval   time.Duration
	ProviderTimeout time.Duration
	LogLevel        string
	AnnounceMention bool

	// Provider keys
	OpenRouterAPIKey    string
	NvidiaAPIKey        string
	CloudflareAPIToken  string
	CloudflareAccountID string
	OllamaCloudBaseURL  string
	OllamaCloudAPIKey   string
	GroqAPIKey          string
	GeminiAPIKey        string
	MistralAPIKey       string
	HuggingFaceAPIKey   string
	SiliconFlowAPIKey   string
	ZaiAPIKey           string
	CohereAPIKey        string
	SambaNovaAPIKey     string
	ChutesAPIKey        string
	NebiusAPIKey        string
}

// Load reads configuration from the process environment.
func Load() Config {
	return Config{
		DiscordBotToken:  os.Getenv("DISCORD_BOT_TOKEN"),
		DiscordChannelID: os.Getenv("DISCORD_CHANNEL_ID"),
		DiscordGuildID:   os.Getenv("DISCORD_GUILD_ID"),

		Port:            envInt("PORT", 8000),
		DBPath:          envStr("DB_PATH", "/home/container/modeltrack.db"),
		TrackInterval:   time.Duration(envInt("TRACK_INTERVAL_MINUTES", 30)) * time.Minute,
		ProviderTimeout: time.Duration(envInt("PROVIDER_TIMEOUT_SECONDS", 25)) * time.Second,
		LogLevel:        strings.ToUpper(envStr("LOG_LEVEL", "INFO")),
		AnnounceMention: envBool("ANNOUNCE_MENTION", false),

		OpenRouterAPIKey:    os.Getenv("OPENROUTER_API_KEY"),
		NvidiaAPIKey:        os.Getenv("NVIDIA_API_KEY"),
		CloudflareAPIToken:  os.Getenv("CLOUDFLARE_API_TOKEN"),
		CloudflareAccountID: os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		OllamaCloudBaseURL:  envStr("OLLAMA_CLOUD_BASE_URL", "https://ollama.com"),
		OllamaCloudAPIKey:   os.Getenv("OLLAMA_CLOUD_API_KEY"),
		GroqAPIKey:          os.Getenv("GROQ_API_KEY"),
		GeminiAPIKey:        os.Getenv("GEMINI_API_KEY"),
		MistralAPIKey:       os.Getenv("MISTRAL_API_KEY"),
		HuggingFaceAPIKey:   os.Getenv("HUGGINGFACE_API_KEY"),
		SiliconFlowAPIKey:   os.Getenv("SILICONFLOW_API_KEY"),
		ZaiAPIKey:           os.Getenv("ZAI_API_KEY"),
		CohereAPIKey:        os.Getenv("COHERE_API_KEY"),
		SambaNovaAPIKey:     os.Getenv("SAMBANOVA_API_KEY"),
		ChutesAPIKey:        os.Getenv("CHUTES_API_KEY"),
		NebiusAPIKey:        os.Getenv("NEBIUS_API_KEY"),
	}
}

// Get returns the value for an UPPER_SNAKE env name (provider convenience).
func (c Config) Get(name string) string {
	switch name {
	case "OPENROUTER_API_KEY":
		return c.OpenRouterAPIKey
	case "NVIDIA_API_KEY":
		return c.NvidiaAPIKey
	case "CLOUDFLARE_API_TOKEN":
		return c.CloudflareAPIToken
	case "CLOUDFLARE_ACCOUNT_ID":
		return c.CloudflareAccountID
	case "OLLAMA_CLOUD_API_KEY":
		return c.OllamaCloudAPIKey
	case "GROQ_API_KEY":
		return c.GroqAPIKey
	case "GEMINI_API_KEY":
		return c.GeminiAPIKey
	case "MISTRAL_API_KEY":
		return c.MistralAPIKey
	case "HUGGINGFACE_API_KEY":
		return c.HuggingFaceAPIKey
	case "SILICONFLOW_API_KEY":
		return c.SiliconFlowAPIKey
	case "ZAI_API_KEY":
		return c.ZaiAPIKey
	case "COHERE_API_KEY":
		return c.CohereAPIKey
	case "SAMBANOVA_API_KEY":
		return c.SambaNovaAPIKey
	case "CHUTES_API_KEY":
		return c.ChutesAPIKey
	case "NEBIUS_API_KEY":
		return c.NebiusAPIKey
	}
	return os.Getenv(name)
}

func envStr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	return n
}

func envBool(name string, def bool) bool {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
