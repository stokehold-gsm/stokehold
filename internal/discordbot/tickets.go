package discordbot

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

// Shop tickets (docs/shop-operations.md). A customer opens a ticket and gets a
// private channel. Shop operators are only told about it; the one who takes it
// is added to the channel, so the others never see the customer's data.
// All state lives in the channel topic: "ticket:<customer id>:<claimer id or ->".

const (
	permView    = discordsetup.PermViewChannel
	permSend    = discordsetup.PermSendMessages
	permHistory = discordsetup.PermReadHistory
	permEmbed   = discordsetup.PermEmbedLinks
	// Attach files is left out on purpose: the bot can only grant what its own
	// role has, and it is not invited with that permission.
	ticketAllow = permView | permSend | permHistory | permEmbed
)

// Tickets holds the names the ticket flow looks up by name, so it needs no
// channel ids in configuration.
type Tickets struct {
	Category string // category the ticket channels are created in
	Notify   string // staff channel that announces new tickets
	Orders   string // staff channel with one line per closed ticket
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func ticketName(username, id string) string {
	s := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(username), "-"), "-")
	if s == "" {
		s = id[max(0, len(id)-6):]
	}
	return "заказ-" + s[:min(len(s), 20)]
}

func parseTopic(topic string) (customer, claimer string, ok bool) {
	p := strings.Split(topic, ":")
	if len(p) != 3 || p[0] != "ticket" || p[1] == "" {
		return "", "", false
	}
	if p[2] == "-" {
		p[2] = ""
	}
	return p[1], p[2], true
}

type guildInfo struct {
	roles map[string]string // name -> id
	chans map[string]chanInfo
}

type chanInfo struct{ ID, Topic, Parent string }

func (t *Tickets) lookup(ctx context.Context, h *Handler, guild string) (guildInfo, error) {
	g := guildInfo{roles: map[string]string{}, chans: map[string]chanInfo{}}
	var roles []struct{ ID, Name string }
	if err := h.API.Do(ctx, "GET", "/guilds/"+guild+"/roles", nil, &roles); err != nil {
		return g, err
	}
	for _, r := range roles {
		g.roles[r.Name] = r.ID
	}
	var chans []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Topic    string `json:"topic"`
		ParentID string `json:"parent_id"`
	}
	if err := h.API.Do(ctx, "GET", "/guilds/"+guild+"/channels", nil, &chans); err != nil {
		return g, err
	}
	for _, c := range chans {
		g.chans[c.Name] = chanInfo{c.ID, c.Topic, c.ParentID}
		if strings.HasPrefix(c.Topic, "ticket:") {
			g.chans[c.ID] = chanInfo{c.ID, c.Topic, c.ParentID}
		}
	}
	return g, nil
}

func perm(v int64) string { return fmt.Sprint(v) }

func (t *Tickets) open(ctx context.Context, h *Handler, in interaction) string {
	uid := in.Member.User.ID
	g, err := t.lookup(ctx, h, in.GuildID)
	if err != nil {
		return "Не удалось открыть тикет, попробуйте позже."
	}
	cat, notify := g.chans[t.Category], g.chans[t.Notify]
	if cat.ID == "" || notify.ID == "" {
		return "Магазин ещё не настроен."
	}
	for _, c := range g.chans {
		if cu, _, ok := parseTopic(c.Topic); ok && cu == uid {
			return "У вас уже есть открытый тикет: <#" + c.ID + ">"
		}
	}
	ow := []map[string]any{
		{"id": in.GuildID, "type": 0, "allow": "0", "deny": perm(permView)},
		{"id": uid, "type": 1, "allow": perm(ticketAllow), "deny": "0"},
	}
	for _, name := range []string{discordsetup.RoleShopCurator, discordsetup.RoleFounder} {
		if id := g.roles[name]; id != "" {
			ow = append(ow, map[string]any{"id": id, "type": 0, "allow": perm(ticketAllow), "deny": "0"})
		}
	}
	var me struct{ ID string }
	if err := h.API.Do(ctx, "GET", "/users/@me", nil, &me); err != nil {
		return "Не удалось открыть тикет, попробуйте позже."
	}
	ow = append(ow, map[string]any{"id": me.ID, "type": 1, "allow": perm(ticketAllow | discordsetup.PermManageMessages), "deny": "0"})

	var made struct{ ID string }
	err = h.API.Do(ctx, "POST", "/guilds/"+in.GuildID+"/channels", map[string]any{
		"name": ticketName(in.Member.User.Username, uid), "type": 0, "parent_id": cat.ID,
		"topic": "ticket:" + uid + ":-", "permission_overwrites": ow,
	}, &made)
	if err != nil {
		return "Не удалось создать канал тикета. Напишите кому-нибудь из команды."
	}
	h.API.Do(ctx, "POST", "/channels/"+made.ID+"/messages", map[string]any{
		"content":          "<@" + uid + ">",
		"allowed_mentions": map[string]any{"users": []string{uid}},
		"embeds": []map[string]any{{
			"title": "Тикет открыт",
			"color": 0xe8451a,
			"description": "Команда магазина уже получила уведомление. Как только кто-то возьмёт тикет, он появится здесь.\n\n" +
				"Опишите, какой тариф вам нужен и на какой площадке вы оплачиваете (Boosty или FunPay), и укажите номер заказа.\n\n" +
				"Пароли, коды 2FA и данные карты здесь не нужны, никто из команды их не попросит.",
		}},
		"components": []map[string]any{{"type": 1, "components": []map[string]any{
			{"type": 2, "style": 4, "label": "Закрыть тикет", "custom_id": "tkt:close:" + made.ID},
		}}},
	}, nil)
	h.API.Do(ctx, "POST", "/channels/"+notify.ID+"/messages", map[string]any{
		"content":          "<@&" + g.roles[discordsetup.RoleOperator] + "> новый тикет",
		"allowed_mentions": map[string]any{"roles": []string{g.roles[discordsetup.RoleOperator]}},
		"embeds": []map[string]any{{
			"title": "Новый тикет", "color": 0xf5b041,
			"description": "Канал: <#" + made.ID + ">\nКлиент: <@" + uid + ">\nНажмите «Взять», чтобы получить доступ к каналу.",
		}},
		"components": []map[string]any{{"type": 1, "components": []map[string]any{
			{"type": 2, "style": 3, "label": "Взять", "custom_id": "tkt:claim:" + made.ID},
		}}},
	}, nil)
	return "Тикет создан: <#" + made.ID + ">"
}

func hasRole(g guildInfo, member []string, names ...string) bool {
	for _, n := range names {
		for _, r := range member {
			if g.roles[n] != "" && g.roles[n] == r {
				return true
			}
		}
	}
	return false
}

func (t *Tickets) claim(ctx context.Context, h *Handler, in interaction, chanID string) string {
	g, err := t.lookup(ctx, h, in.GuildID)
	if err != nil {
		return "Не удалось взять тикет, попробуйте позже."
	}
	if !hasRole(g, in.Member.Roles, discordsetup.RoleOperator, discordsetup.RoleShopCurator, discordsetup.RoleFounder) {
		return "Тикеты берут сотрудники магазина."
	}
	c, ok := g.chans[chanID]
	if !ok || c.Parent != g.chans[t.Category].ID {
		return "Тикет уже закрыт."
	}
	customer, claimer, _ := parseTopic(c.Topic)
	if claimer != "" {
		return "Тикет уже взял <@" + claimer + ">."
	}
	op := in.Member.User.ID
	if err := h.API.Do(ctx, "PUT", "/channels/"+chanID+"/permissions/"+op, map[string]any{"type": 1, "allow": perm(ticketAllow), "deny": "0"}, nil); err != nil {
		return "Не удалось выдать доступ к каналу."
	}
	h.API.Do(ctx, "PATCH", "/channels/"+chanID, map[string]any{"topic": "ticket:" + customer + ":" + op}, nil)
	h.API.Do(ctx, "PATCH", "/channels/"+in.ChannelID+"/messages/"+in.Message.ID, map[string]any{
		"content": "", "components": []any{},
		"embeds": []map[string]any{{"title": "Тикет взят", "color": 0x2ecc71, "description": "Канал: <#" + chanID + ">\nВзял: <@" + op + ">"}},
	}, nil)
	h.API.Do(ctx, "POST", "/channels/"+chanID+"/messages", map[string]any{
		"content":          "<@" + customer + ">, ваш тикет взял <@" + op + ">.",
		"allowed_mentions": map[string]any{"users": []string{customer}},
	}, nil)
	return "Вы взяли тикет: <#" + chanID + ">"
}

func (t *Tickets) close(ctx context.Context, h *Handler, in interaction, chanID string) string {
	g, err := t.lookup(ctx, h, in.GuildID)
	if err != nil {
		return "Не удалось закрыть тикет, попробуйте позже."
	}
	c, ok := g.chans[chanID]
	if !ok || c.Parent != g.chans[t.Category].ID {
		return "Тикет уже закрыт."
	}
	customer, claimer, _ := parseTopic(c.Topic)
	who := in.Member.User.ID
	if who != customer && who != claimer && !hasRole(g, in.Member.Roles, discordsetup.RoleShopCurator, discordsetup.RoleFounder) {
		return "Закрыть тикет может клиент, тот кто его взял, или куратор магазина."
	}
	if orders := g.chans[t.Orders]; orders.ID != "" {
		by := "никто не брал"
		if claimer != "" {
			by = "<@" + claimer + ">"
		}
		h.API.Do(ctx, "POST", "/channels/"+orders.ID+"/messages", map[string]any{
			"content":          "Тикет закрыт. Клиент: <@" + customer + ">, исполнитель: " + by + ", закрыл: <@" + who + ">.",
			"allowed_mentions": map[string]any{"parse": []string{}},
		}, nil)
	}
	if err := h.API.Do(ctx, "DELETE", "/channels/"+chanID, nil, nil); err != nil {
		return "Не удалось закрыть канал."
	}
	return "Тикет закрыт."
}
