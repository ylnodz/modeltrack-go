package provider

import (
	"testing"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/model"
)

func TestBuildReturnsAllFourteenSorted(t *testing.T) {
	cfg := config.Config{}
	list := Build(cfg)
	if len(list) != 14 {
		t.Fatalf("want 14 providers, got %d", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Key() > list[i].Key() {
			t.Fatalf("not sorted: %s > %s", list[i-1].Key(), list[i].Key())
		}
	}
}

func TestUnconfiguredListsEverythingWithoutKeys(t *testing.T) {
	cfg := config.Config{}
	// OpenRouter is public, so it is always configured.
	un := Unconfigured(cfg)
	if len(un) != 13 {
		t.Fatalf("want 13 unconfigured, got %d (%v)", len(un), un)
	}
	for _, k := range un {
		if k == "openrouter" {
			t.Fatal("openrouter must never be unconfigured")
		}
	}
}

func TestOpenRouterConfiguredWithoutKey(t *testing.T) {
	p := NewOpenRouter(config.Config{})
	if !p.Configured() {
		t.Fatal("openrouter should be configured with no key")
	}
	if !p.FilterFree() || !p.WatchPrice() || !p.WatchContext() {
		t.Fatal("openrouter flags wrong")
	}
}

func TestThinProviderFreeRules(t *testing.T) {
	// Groq-style: everything listed is free.
	groq := newThin(config.Config{}, Spec{Key: "groq", Label: "Groq", EnvKey: "GROQ_API_KEY",
		BaseURL: "https://api.groq.com/openai/v1", FreeNote: "free tier (rate-limited)"})
	if groq.Key() != "groq" {
		t.Fatalf("key=%s", groq.Key())
	}

	// Nebius-style: free only when both prices are known and zero.
	neb := newThin(config.Config{}, Spec{Key: "nebius", Label: "Nebius", EnvKey: "NEBIUS_API_KEY",
		BaseURL: "https://api.studio.nebius.com/v1", ZeroPriceFree: true})
	if neb.Key() != "nebius" {
		t.Fatalf("key=%s", neb.Key())
	}
}

func TestNebiusFreeRule(t *testing.T) {
	cfg := config.Config{}
	b := Base{KeyName: "nebius", Config: cfg}
	in, out := 0.0, 0.0
	if !b.DefaultIsFree(nil, &in, &out) {
		t.Fatal("zero/zero must be free")
	}
	in2 := 1.0
	if b.DefaultIsFree(nil, &in2, &out) {
		t.Fatal("paid must not be free")
	}
	if b.DefaultIsFree(nil, nil, nil) {
		t.Fatal("unknown pricing must not be free")
	}
}

func TestParsePriceSentinel(t *testing.T) {
	if got := model.ParsePrice(-1); got != nil {
		t.Fatalf("-1 sentinel should be nil, got %v", *got)
	}
	if got := model.ParsePrice("0.0000012"); got == nil || *got != 0.0000012 {
		t.Fatalf("per-token string parse failed: %v", got)
	}
	if got := model.ScalePrice(model.ParsePrice("0.0000012"), 1_000_000); got == nil || *got != 1.2 {
		t.Fatalf("scaling failed: %v", got)
	}
}
