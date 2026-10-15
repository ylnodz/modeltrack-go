// Package bot is the Discord client: slash commands, send queue, track loop.
package bot

import (
	"context"
	"errors"
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
	ready     chan struct{}
	pagerMu   sync.Mutex
	pagers    map[string]*pager
	pagerSeq  int
	closeOnce sync.Once
	readyOnce sync.Once
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
	// discordgo logs through its own logger, which writes to stdout behind
	// slog's back. Route it into slog so its diagnostics honour LOG_LEVEL and
	// keep our format.
	//
	// Its LogError level is not reserved for fatal problems — it also fires for
	// the benign "rate limit unmarshal error" notice emitted when a 429 body is
	// not a TooManyRequests object. That one is noise, but the rest are real
	// (e.g. "error decoding websocket message", which is how a gateway closed by
	// an intercepting proxy surfaces), so only that message is demoted to Debug
	// and everything else keeps Error.
	discordgo.Logger = func(msgL, caller int, format string, a ...interface{}) {
		msg := fmt.Sprintf(format, a...)
		if strings.Contains(msg, "rate limit unmarshal error") {
			slog.Debug("discordgo", "msg", msg)
			return
		}
		switch msgL {
		case discordgo.LogError, discordgo.LogWarning:
			slog.Error("discordgo", "msg", msg)
		default:
			slog.Debug("discordgo", "msg", msg)
		}
	}
	s.LogLevel = discordgo.LogInformational
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages
	// Surface non-JSON REST responses (Cloudflare blocks, proxy error pages)
	// instead of letting them reduce to a bare json error at the call site.
	// discordgo.New always installs an http.Client, so this only wraps it.
	if s.Client != nil {
		s.Client.Transport = newDiagTransport(s.Client.Transport)
	}

	b := &Bot{
		Config:  cfg,
		Store:   store,
		Tracker: trk,
		Session: s,
		queue:   make(chan queued, 256),
		ready:   make(chan struct{}),
		pagers:  map[string]*pager{},
	}

	s.AddHandler(b.onReady)
	s.AddHandler(b.onInteraction)

	trk.Sender = b.sendEvent
	return b, nil
}

// Run opens the gateway connection and blocks until ctx is done. Slash
// commands are registered from the READY event, not here: Session.State.User
// is nil until the gateway handshake completes.
func (b *Bot) Run(ctx context.Context) error {
	go b.consumeQueue(ctx)
	go b.trackLoop(ctx)

	if err := b.openWithRetry(ctx); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}

	<-ctx.Done()
	b.closeOnce.Do(func() { _ = b.Session.Close() })
	return nil
}

// Gateway handshake retry policy. Open fetches the gateway URL over REST and
// then connects; a transient upstream failure (proxy error page, rate-limit
// block, 5xx) would otherwise kill the process at startup, and the host runs
// this binary directly, so a transient block must not be fatal.
//
// Transient failures are retried indefinitely with a capped backoff so the bot
// comes up on its own once Discord or the edge recovers. Configuration errors
// (see isFatalHandshakeError) still fail immediately — those cannot be fixed by
// waiting.
//
// Vars rather than consts so tests can inject failures and shrink the backoff.
var (
	openBaseDelay = 5 * time.Second
	openMaxDelay  = 5 * time.Minute

	// sessionOpen performs one handshake attempt.
	sessionOpen = func(s *discordgo.Session) error { return s.Open() }
)

// openWithRetry retries a transient handshake failure with capped exponential
// backoff until it succeeds or ctx is cancelled.
func (b *Bot) openWithRetry(ctx context.Context) error {
	var err error
	for attempt := 1; ; attempt++ {
		err = sessionOpen(b.Session)
		if err == nil {
			if attempt > 1 {
				slog.Info("gateway connection established", "attempt", attempt)
			}
			return nil
		}
		if isFatalHandshakeError(err) {
			return err
		}

		delay := openBaseDelay << uint(min(attempt-1, 16))
		if delay <= 0 || delay > openMaxDelay {
			delay = openMaxDelay
		}
		slog.Warn("gateway handshake failed; retrying",
			"attempt", attempt, "retry_in", delay.String(), "err", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// isFatalHandshakeError reports failures a retry cannot fix: 4004 is an invalid
// token and 4014 disallowed intents, both configuration problems rather than
// transient upstream failures.
func isFatalHandshakeError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "4004") ||
		strings.Contains(msg, "4014") ||
		strings.Contains(msg, "Authentication failed")
}

func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	if r.User == nil {
		slog.Error("ready event without user; skipping command registration")
		return
	}
	slog.Info("ready", "user", r.User.Username, "id", r.User.ID)
	if err := registerCommands(s, r.User.ID, b.Config.DiscordGuildID, b.buildCommands()); err != nil {
		slog.Error("register slash commands", "err", err)
	} else {
		slog.Info("registered slash commands", "count", len(b.buildCommands()))
	}
	// Unblock the send queue: messages posted before the gateway handshake
	// completes are held rather than dropped. sync.Once keeps reconnects safe.
	if b.ready != nil {
		b.readyOnce.Do(func() { close(b.ready) })
	}
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
	// Hold deliveries until the gateway is ready so early events aren't dropped.
	if b.ready != nil {
		select {
		case <-b.ready:
		case <-ctx.Done():
			return
		}
	}
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

// registerCommands publishes the slash commands for the application. It is a
// no-op when appID is empty, so it can never nil-deref before the gateway is
// ready. Overwrite (not Create) keeps it idempotent across reconnects.
//
// Exactly one scope owns the commands. When a guild is configured the commands
// are guild-scoped (instant propagation) and the global scope is cleared;
// otherwise they are global. Discord stores the two scopes independently and
// renders a command once per scope it exists in, so an old global copy left
// behind by a previous deployment shows up as a duplicate /free.
func registerCommands(s *discordgo.Session, appID, guildID string, cmds []*discordgo.ApplicationCommand) error {
	if s == nil {
		return errors.New("nil session")
	}
	if appID == "" {
		return errors.New("empty application id (gateway not ready)")
	}
	if guildID != "" {
		if _, err := s.ApplicationCommandBulkOverwrite(appID, "", nil); err != nil {
			return fmt.Errorf("clear global commands: %w", err)
		}
		_, err := s.ApplicationCommandBulkOverwrite(appID, guildID, cmds)
		return err
	}
	_, err := s.ApplicationCommandBulkOverwrite(appID, "", cmds)
	return err
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
