package render

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/ylnodz/modeltrack-go/internal/model"
	"github.com/ylnodz/modeltrack-go/internal/state"
)

func TestEventEmbedShowsContextAndPrice(t *testing.T) {
	ctxLen := 131072
	m := model.Model{
		Source:     "openrouter",
		PK:         "x/y:free",
		Name:       "Y",
		IsFree:     true,
		Context:    &ctxLen,
		Modalities: []string{"text", "image"},
	}
	emb, _ := EventEmbed(state.Event{Kind: state.New, Model: m})

	if !strings.Contains(emb.Title, "Y") {
		t.Fatalf("title=%q", emb.Title)
	}
	if !strings.Contains(emb.Footer.Text, "131K ctx") {
		t.Fatalf("footer=%q", emb.Footer.Text)
	}
	if !strings.Contains(emb.Footer.Text, "OpenRouter") {
		t.Fatalf("footer=%q", emb.Footer.Text)
	}
	if !strings.Contains(emb.Footer.Text, "img") {
		t.Fatalf("footer=%q", emb.Footer.Text)
	}
	if emb.Description != "`free`" {
		t.Fatalf("description=%q", emb.Description)
	}
}

func TestPriceLineForPaidModel(t *testing.T) {
	in, out := 1.5, 6.0
	m := model.Model{Source: "groq", PK: "m", Name: "M", PriceIn: &in, PriceOut: &out}
	emb, _ := EventEmbed(state.Event{Kind: state.New, Model: m})

	if emb.Description != "`$1.50/M in · $6.00/M out`" {
		t.Fatalf("description=%q", emb.Description)
	}
}

func TestBecameFreeCarriesContent(t *testing.T) {
	m := model.Model{Source: "groq", PK: "m", Name: "M", IsFree: true}
	_, content := EventEmbed(state.Event{Kind: state.BecameFree, Model: m})
	if !strings.Contains(content, "now free on Groq") {
		t.Fatalf("content=%q", content)
	}
}

func TestDigestTotals(t *testing.T) {
	counts := map[string]state.Count{
		"openrouter": {Total: 467, Free: 19},
		"groq":       {Total: 12, Free: 12},
	}
	emb := Digest(counts)

	if !strings.Contains(emb.Description, "19 free / 467 total") {
		t.Fatalf("description=%q", emb.Description)
	}
	if !strings.Contains(emb.Footer.Text, "31 free models") {
		t.Fatalf("footer=%q", emb.Footer.Text)
	}
}

func TestChunkSplitsAtTen(t *testing.T) {
	if MaxEmbedsPerMessage != 10 {
		t.Fatalf("cap=%d", MaxEmbedsPerMessage)
	}
	real := make([]*discordgo.MessageEmbed, 25)
	for i := range real {
		real[i] = &discordgo.MessageEmbed{}
	}
	pages := Chunk(real, MaxEmbedsPerMessage)
	if len(pages) != 3 {
		t.Fatalf("want 3 pages, got %d", len(pages))
	}
	if len(pages[0]) != 10 || len(pages[2]) != 5 {
		t.Fatalf("bad page sizes: %d/%d", len(pages[0]), len(pages[2]))
	}
}
