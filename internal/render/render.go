// Package render builds the Discord embeds. One color per embed, dense byline.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/ylnodz/modeltrack-go/internal/model"
	"github.com/ylnodz/modeltrack-go/internal/state"
)

// Embed colors.
const (
	FreeColor       = 0x2ECC71
	PaidColor       = 0xE67E22
	PriceColor      = 0xF1C40F
	GreyColor       = 0x95A5A6
	DefaultNewColor = 0x9B59B6
)

// MaxEmbedsPerMessage is Discord's hard cap.
const MaxEmbedsPerMessage = 10

const descLimit = 200

var labels = map[string]string{
	"openrouter":  "OpenRouter",
	"nim":         "NVIDIA NIM",
	"cloudflare":  "Cloudflare",
	"ollama":      "Ollama Cloud",
	"groq":        "Groq",
	"gemini":      "Google AI Studio",
	"mistral":     "Mistral",
	"huggingface": "HuggingFace",
	"siliconflow": "SiliconFlow",
	"zai":         "Z.AI",
	"cohere":      "Cohere",
	"sambanova":   "SambaNova",
	"chutes":      "Chutes",
	"nebius":      "Nebius",
}

var modalityIcon = map[string]string{
	"text":      "text",
	"image":     "img",
	"audio":     "audio",
	"video":     "video",
	"embedding": "embed",
}

// Label returns the display label for a provider key.
func Label(source string) string {
	if l, ok := labels[source]; ok {
		return l
	}
	return source
}

// EventEmbed renders an event into an embed plus optional message content.
// The content is non-empty only for free/paid/deprecated transitions.
func EventEmbed(e state.Event) (*discordgo.MessageEmbed, string) {
	m := e.Model
	switch e.Kind {
	case state.BecameFree:
		return baseEmbed(m, FreeColor, ""), fmt.Sprintf("%s is now free on %s", m.Name, Label(m.Source))
	case state.BecamePaid:
		return baseEmbed(m, PaidColor, ""), fmt.Sprintf("%s is no longer free on %s", m.Name, Label(m.Source))
	case state.PriceChange:
		emb := baseEmbed(m, PriceColor, "")
		emb.Fields = append(emb.Fields, &discordgo.MessageEmbedField{
			Name:  "Price change",
			Value: fmt.Sprintf("was %s/%s → %s/%s/M", money(oldFloat(e.Old, "price_in")), money(oldFloat(e.Old, "price_out")), money(m.PriceIn), money(m.PriceOut)),
		})
		return emb, ""
	case state.ContextChange:
		emb := baseEmbed(m, PriceColor, "")
		emb.Fields = append(emb.Fields, &discordgo.MessageEmbedField{
			Name:  "Context change",
			Value: fmt.Sprintf("was %s → %s", ctxText(oldInt(e.Old, "context")), ctxText(m.Context)),
		})
		return emb, ""
	case state.Deprecated:
		return baseEmbed(m, GreyColor, ""), fmt.Sprintf("%s no longer listed on %s", m.Name, Label(m.Source))
	default:
		color := DefaultNewColor
		prefix := ""
		if m.IsFree {
			color = FreeColor
			prefix = "🟢 "
		}
		return baseEmbed(m, color, prefix), ""
	}
}

// Digest renders the single summary embed for /free.
func Digest(counts map[string]state.Count) *discordgo.MessageEmbed {
	keys := sortedKeys(counts)
	var lines []string
	totalFree := 0
	for _, src := range keys {
		c := counts[src]
		totalFree += c.Free
		if c.Free > 0 {
			lines = append(lines, fmt.Sprintf("`%-16s %4d free / %d total`", Label(src), c.Free, c.Total))
		}
	}
	desc := strings.Join(lines, "\n")
	if desc == "" {
		desc = "No free models tracked yet."
	}
	return &discordgo.MessageEmbed{
		Title:       "Free models",
		Description: desc,
		Color:       FreeColor,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("%d free models · use /free provider:<name>", totalFree),
		},
	}
}

// Chunk splits embeds into Discord-sized batches.
func Chunk(embeds []*discordgo.MessageEmbed, size int) [][]*discordgo.MessageEmbed {
	if size <= 0 {
		size = MaxEmbedsPerMessage
	}
	var out [][]*discordgo.MessageEmbed
	for i := 0; i < len(embeds); i += size {
		end := i + size
		if end > len(embeds) {
			end = len(embeds)
		}
		out = append(out, embeds[i:end])
	}
	return out
}

// ── internals ─────────────────────────────────────────────────

func baseEmbed(m model.Model, color int, prefix string) *discordgo.MessageEmbed {
	emb := &discordgo.MessageEmbed{
		Title:       prefix + m.Name,
		URL:         m.URL,
		Color:       color,
		Description: "`" + priceLine(m) + "`",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("%s · %s · %s", Label(m.Source), ctxText(m.Context), modsText(m)),
		},
	}
	if desc := truncate(m.Description, descLimit); desc != "" {
		emb.Fields = append(emb.Fields, &discordgo.MessageEmbedField{
			Name: "\u200b", Value: desc,
		})
	}
	return emb
}

func priceLine(m model.Model) string {
	if m.IsFree {
		if m.FreeNote != "" {
			return m.FreeNote
		}
		return "free"
	}
	if m.PriceIn == nil && m.PriceOut == nil {
		if m.FreeNote != "" {
			return m.FreeNote
		}
		return "pricing n/a"
	}
	return fmt.Sprintf("%s/M in · %s/M out", money(m.PriceIn), money(m.PriceOut))
}

func money(v *float64) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprintf("$%.2f", *v)
}

func ctxText(v *int) string {
	if v == nil || *v == 0 {
		return "ctx n/a"
	}
	if *v >= 1000 {
		return fmt.Sprintf("%dK ctx", int(float64(*v)/1000+0.5))
	}
	return fmt.Sprintf("%d ctx", *v)
}

func modsText(m model.Model) string {
	if len(m.Modalities) == 0 {
		return "text"
	}
	parts := make([]string, 0, len(m.Modalities))
	for _, mod := range m.Modalities {
		if icon, ok := modalityIcon[mod]; ok {
			parts = append(parts, icon)
		} else {
			parts = append(parts, mod)
		}
	}
	return strings.Join(parts, " · ")
}

func truncate(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	cut := text[:limit-1]
	if idx := strings.LastIndex(cut, " "); idx > 0 {
		cut = cut[:idx]
	}
	return cut + "…"
}

func oldFloat(old map[string]any, key string) *float64 {
	if old == nil {
		return nil
	}
	switch v := old[key].(type) {
	case float64:
		return &v
	case *float64:
		return v
	}
	return nil
}

func oldInt(old map[string]any, key string) *int {
	if old == nil {
		return nil
	}
	switch v := old[key].(type) {
	case int:
		return &v
	case *int:
		return v
	}
	return nil
}

func sortedKeys(m map[string]state.Count) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
