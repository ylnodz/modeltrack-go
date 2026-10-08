package bot

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// pager holds a paginated result for button-driven navigation.
type pager struct {
	pages [][]*discordgo.MessageEmbed
	idx   int
}

func (b *Bot) storePager(pages [][]*discordgo.MessageEmbed) string {
	b.pagerMu.Lock()
	defer b.pagerMu.Unlock()
	b.pagerSeq++
	id := fmt.Sprintf("pager:%d", b.pagerSeq)
	b.pagers[id] = &pager{pages: pages}
	return id
}

func (b *Bot) getPager(id string) (*pager, bool) {
	b.pagerMu.Lock()
	defer b.pagerMu.Unlock()
	p, ok := b.pagers[id]
	return p, ok
}

func pagerComponents(id string, idx, total int) []discordgo.MessageComponent {
	prevDisabled := idx <= 0
	nextDisabled := idx >= total-1
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Prev",
					Style:    discordgo.SecondaryButton,
					CustomID: id + ":prev",
					Disabled: prevDisabled,
				},
				discordgo.Button{
					Label:    "Next",
					Style:    discordgo.SecondaryButton,
					CustomID: id + ":next",
					Disabled: nextDisabled,
				},
			},
		},
	}
}

// onComponent handles Prev/Next button presses for a stored pager.
func (b *Bot) onComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID
	var id, action string
	for idx := len(customID) - 1; idx >= 0; idx-- {
		if customID[idx] == ':' {
			id, action = customID[:idx], customID[idx+1:]
			break
		}
	}
	p, ok := b.getPager(id)
	if !ok {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{Content: "This view expired."},
		})
		return
	}

	b.pagerMu.Lock()
	switch action {
	case "next":
		p.idx++
	case "prev":
		p.idx--
	}
	if p.idx < 0 {
		p.idx = 0
	}
	if p.idx >= len(p.pages) {
		p.idx = len(p.pages) - 1
	}
	idx := p.idx
	pages := p.pages
	b.pagerMu.Unlock()

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     pages[idx],
			Components: pagerComponents(id, idx, len(pages)),
		},
	})
}
