// Package bot is the Discord client: slash commands, send queue, track loop.
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/provider"
	"github.com/ylnodz/modeltrack-go/internal/render"
	"github.com/ylnodz/modeltrack-go/internal/state"
	"github.com/ylnodz/modeltrack-go/internal/tracker"
)

// PageSize is the embeds-per-page for /free provider:<name>.
const PageSize = 10

// Bot owns the Discord session, the send queue, and the tracking loop.
type Bot struct {
	Config  config.Config
	Store   *state.Store
	Tracker *tracker.Tracker
	Session *discordgo.Session

	queue     chan queued
	pagerMu   sync.Mutex
	pagers    map[string]*pager
	pagerSeq  int
	closeOnce sync.Once
}

type queued struct {
	embed   *discordgo.MessageEmbed
	content string
}

// New builds the bot (the session is opened by Run).
func New(cfg config.Config, store *state.Store, trk *tracker.Tracker) (*Bot, error) {
	s, err := discordgo.New("Bot " + cfg.DiscordBotToken)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages

	b := &Bot{
		Config:  cfg,
		Store:   store,
		Tracker: trk,
		Session: s,
		queue:   make(chan queued, 256),
		pagers:  map[string]*pager{},
	}

	s.AddHandler(b.onReady)
	s.AddHandler(b.onInteraction)

	trk.Sender = b.sendEvent
	return b, nil
}

// Run opens the gateway connection, registers commands, and blocks.
func (b *Bot) Run(ctx context.Context) error {
	if err := b.registerCommands(); err != nil {
		return fmt.Errorf("register commands: %w", err)
	}
	go b.consumeQueue(ctx)
	go b.trackLoop(ctx)

	if err := b.Session.Open(); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}
	slog.Info("logged in", "user", b.Session.State.User.Username)

	<-ctx.Done()
	b.closeOnce.Do(func() { _ = b.Session.Close() })
	return nil
}

func (b *Bot) onReady(_ *discordgo.Session, r *discordgo.Ready) {
	slog.Info("ready", "user", r.User.Username)
}

// ── send queue ────────────────────────────────────────────────

func (b *Bot) sendEvent(ctx context.Context, e state.Event, content string) error {
	emb, defaultContent := render.EventEmbed(e)
	c := content
	if c == "" {
		c = defaultContent
	}
	select {
	case b.queue <- queued{embed: emb, content: c}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Bot) consumeQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-b.queue:
			b.deliver(ctx, item)
		}
	}
}

func (b *Bot) deliver(ctx context.Context, item queued) {
	for attempt := 0; attempt < 3; attempt++ {
		_, err := b.Session.ChannelMessageSendComplex(b.Config.DiscordChannelID, &discordgo.MessageSend{
			Content: item.content,
			Embeds:  []*discordgo.MessageEmbed{item.embed},
		})
		if err == nil {
			return
		}
		if rl, ok := err.(*discordgo.RESTError); ok && rl.Response != nil && rl.Response.StatusCode == 429 {
			delay := 2 * time.Second
			if rl.Response.Header.Get("Retry-After") != "" {
				if d, perr := time.ParseDuration(rl.Response.Header.Get("Retry-After") + "ms"); perr == nil {
					delay = d
				}
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			continue
		}
		slog.Warn("send failed", "err", err)
		return
	}
}

// ── tracking loop ─────────────────────────────────────────────

func (b *Bot) trackLoop(ctx context.Context) {
	// First cycle shortly after start (seeded silently by the tracker).
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return
	}
	for {
		b.Tracker.RunCycle(ctx)
		select {
		case <-time.After(b.Config.TrackInterval):
		case <-ctx.Done():
			return
		}
	}
}

// ── slash commands ────────────────────────────────────────────

func (b *Bot) registerCommands() error {
	cmds := b.buildCommands()

	for _, c := range cmds {
		var err error
		if b.Config.DiscordGuildID != "" {
			_, err = b.Session.ApplicationCommandCreate(b.Session.State.User.ID, b.Config.DiscordGuildID, c)
		} else {
			_, err = b.Session.ApplicationCommandCreate(b.Session.State.User.ID, "", c)
		}
		if err != nil {
			return err
		}
	}
	slog.Info("registered slash commands", "count", len(cmds))
	return nil
}

// buildCommands returns the slash-command definitions, including the provider
// autocomplete choices derived from the live provider list.
func (b *Bot) buildCommands() []*discordgo.ApplicationCommand {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(b.Tracker.Provider))
	for _, p := range b.Tracker.Provider {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  p.Label(),
			Value: p.Key(),
		})
	}

	return []*discordgo.ApplicationCommand{
		{
			Name:        "free",
			Description: "List free models",
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "provider",
				Description: "Limit to a single provider",
				Required:    false,
				Choices:     choices,
			}},
		},
		{
			Name:        "status",
			Description: "Show tracker status",
		},
	}
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		b.onComponent(s, i)
		return
	case discordgo.InteractionApplicationCommand:
		// handled below
	default:
		return
	}

	switch i.ApplicationCommandData().Name {
	case "free":
		b.handleFree(s, i)
	case "status":
		b.handleStatus(s, i)
	}
}

func (b *Bot) handleFree(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		slog.Warn("defer failed", "err", err)
		return
	}

	key := ""
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "provider" {
			key = opt.StringValue()
		}
	}

	models, err := b.Store.FreeModels(ctx, key)
	if err != nil {
		b.followup(s, i, "Failed to read state.")
		return
	}
	if len(models) == 0 {
		b.followup(s, i, "No free models tracked yet.")
		return
	}

	if key == "" {
		counts, err := b.Store.Counts(ctx)
		if err != nil {
			b.followup(s, i, "Failed to read state.")
			return
		}
		b.followupEmbed(s, i, render.Digest(counts))
		return
	}

	embeds := make([]*discordgo.MessageEmbed, 0, len(models))
	for _, m := range models {
		emb, _ := render.EventEmbed(state.Event{Kind: state.New, Model: m})
		embeds = append(embeds, emb)
	}
	pages := render.Chunk(embeds, PageSize)

	content := fmt.Sprintf("🆓 %d free on %s", len(models), render.Label(key))
	var components []discordgo.MessageComponent
	if len(pages) > 1 {
		id := b.storePager(pages)
		components = pagerComponents(id, 0, len(pages))
	}

	_, err = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content:    content,
		Embeds:     pages[0],
		Components: components,
	})
	if err != nil {
		slog.Warn("followup failed", "err", err)
	}
}

func (b *Bot) handleStatus(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		return
	}

	_, summary, status := b.Tracker.Snapshot()
	counts, _ := b.Store.Counts(ctx)

	keys := make([]string, 0, len(status))
	for k := range status {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var lines []string
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("`%s: %s`", k, status[k]))
	}

	totalFree, totalAll := 0, 0
	for _, c := range counts {
		totalFree += c.Free
		totalAll += c.Total
	}

	embed := &discordgo.MessageEmbed{
		Title: "ModelTrack status",
		Color: 0x3498DB,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "Last cycle",
				Value: fmt.Sprintf("models %d · events %d · %dms", summary.Models, summary.Events, summary.ElapsedMS),
			},
			{
				Name:  "Providers",
				Value: strings.Join(lines, "\n"),
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("%d free / %d total", totalFree, totalAll),
		},
	}
	b.followupEmbed(s, i, embed)
}

func (b *Bot) followup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: content})
}

func (b *Bot) followupEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed) {
	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Embeds: []*discordgo.MessageEmbed{emb}})
}

func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// ProviderKeys returns every provider key (used by tests).
func ProviderKeys(cfg config.Config) []string {
	var out []string
	for _, p := range provider.Build(cfg) {
		out = append(out, p.Key())
	}
	return out
}
