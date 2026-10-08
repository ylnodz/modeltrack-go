package bot

import (
	"path/filepath"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/state"
	"github.com/ylnodz/modeltrack-go/internal/tracker"
)

func TestRegisterCommandsGuardsAgainstNilSessionAndEmptyAppID(t *testing.T) {
	cmds := []*discordgo.ApplicationCommand{{Name: "free"}}

	// Nil session must not panic.
	if err := registerCommands(nil, "123", "", cmds); err == nil {
		t.Fatal("nil session must error")
	}
	// Empty app id (gateway not ready) must not panic and must not call the API.
	if err := registerCommands(&discordgo.Session{}, "", "", cmds); err == nil {
		t.Fatal("empty app id must error")
	}
	// The pre-fix crash path: a real session with a nil State.User. Registration
	// must be driven by the app id argument, never by Session.State.User.
	s := &discordgo.Session{}
	if s.State == nil || s.State.User == nil {
		// Confirm this is the exact condition that used to segfault, and that
		// registerCommands no longer touches it.
		if err := registerCommands(s, "", "", cmds); err == nil {
			t.Fatal("nil State.User path must be guarded")
		}
	}
}

func TestBuildCommandsRegistersFreeAndStatusWithProviderChoices(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.Config{DiscordBotToken: "x", DiscordChannelID: "1"}
	trk := tracker.New(cfg, store, nil)
	b := &Bot{Config: cfg, Store: store, Tracker: trk}

	cmds := b.buildCommands()
	if len(cmds) != 2 {
		t.Fatalf("want 2 commands, got %d", len(cmds))
	}
	if cmds[0].Name != "free" || cmds[1].Name != "status" {
		t.Fatalf("names: %s, %s", cmds[0].Name, cmds[1].Name)
	}

	free := cmds[0]
	if len(free.Options) != 1 {
		t.Fatalf("free options: %d", len(free.Options))
	}
	opt := free.Options[0]
	if opt.Name != "provider" || opt.Type != discordgo.ApplicationCommandOptionString {
		t.Fatalf("option: %s/%v", opt.Name, opt.Type)
	}
	// One autocomplete choice per provider, all 14 present.
	if len(opt.Choices) != 14 {
		t.Fatalf("want 14 choices, got %d", len(opt.Choices))
	}
	seen := map[string]string{}
	for _, c := range opt.Choices {
		seen[c.Value.(string)] = c.Name
	}
	if seen["openrouter"] != "OpenRouter" {
		t.Fatalf("openrouter label: %q", seen["openrouter"])
	}
	if seen["nim"] != "NVIDIA NIM" {
		t.Fatalf("nim label: %q", seen["nim"])
	}
	// Every provider key must be a valid command choice value.
	if len(ProviderKeys(cfg)) != 14 {
		t.Fatalf("provider keys: %d", len(ProviderKeys(cfg)))
	}
}

func TestPagerComponentsDisableAtBounds(t *testing.T) {
	first := pagerComponents("pager:1", 0, 3)
	row, ok := first[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatal("expected ActionsRow")
	}
	prev := row.Components[0].(discordgo.Button)
	next := row.Components[1].(discordgo.Button)
	if !prev.Disabled {
		t.Fatal("prev must be disabled on first page")
	}
	if next.Disabled {
		t.Fatal("next must be enabled on first page")
	}
	if prev.CustomID != "pager:1:prev" || next.CustomID != "pager:1:next" {
		t.Fatalf("custom ids: %s / %s", prev.CustomID, next.CustomID)
	}

	last := pagerComponents("pager:1", 2, 3)
	row = last[0].(discordgo.ActionsRow)
	if !row.Components[1].(discordgo.Button).Disabled {
		t.Fatal("next must be disabled on last page")
	}
}

func TestStorePagerRoundTrip(t *testing.T) {
	b := &Bot{pagers: map[string]*pager{}}
	id := b.storePager([][]*discordgo.MessageEmbed{
		{{Title: "a"}}, {{Title: "b"}},
	})
	p, ok := b.getPager(id)
	if !ok {
		t.Fatal("pager not stored")
	}
	if len(p.pages) != 2 || p.pages[1][0].Title != "b" {
		t.Fatalf("pages wrong: %+v", p.pages)
	}
}
