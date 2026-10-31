package discordbot

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

// The events channel is built from Discord's audit log, polled over plain HTTP.
// It is the only source that says who did something: bans, kicks, timeouts,
// role and channel changes, webhooks. What was written, who joined or spoke
// comes from the gateway (logs.go).
var auditActions = map[int]string{
	1: "Изменён сервер", 10: "Создан канал", 11: "Изменён канал", 12: "Удалён канал",
	13: "Права канала: добавлены", 14: "Права канала: изменены", 15: "Права канала: удалены",
	20: "Исключение", 21: "Чистка неактивных", 22: "Бан", 23: "Разбан", 24: "Изменён участник (ник, таймаут)",
	25: "Изменены роли участника", 28: "Добавлен бот", 30: "Создана роль", 31: "Изменена роль", 32: "Удалена роль",
	40: "Создано приглашение", 41: "Удалено приглашение", 50: "Создан вебхук", 51: "Изменён вебхук", 52: "Удалён вебхук",
	72: "Удалено сообщение", 73: "Массовое удаление", 74: "Закреплено сообщение", 75: "Откреплено сообщение",
	121: "Права команд изменены", 140: "AutoMod: правило создано", 141: "AutoMod: правило изменено", 142: "AutoMod: правило удалено",
	143: "AutoMod: сообщение заблокировано", 145: "AutoMod: таймаут",
}

type auditEntry struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	TargetID   string `json:"target_id"`
	ActionType int    `json:"action_type"`
	Reason     string `json:"reason"`
	Options    struct {
		Count     string `json:"count"`
		ChannelID string `json:"channel_id"`
	} `json:"options"`
}

type auditPage struct {
	Entries []auditEntry `json:"audit_log_entries"`
}

// Events copies new audit log entries into the events channel.
type Events struct {
	API     *discordsetup.Client
	GuildID string
	Channel string // channel name to write to
	Every   time.Duration
	// GatewayOn drops invite entries: the gateway log already shows them with the code.
	GatewayOn bool
	Now       func() time.Time // tests only
	Sink      *Sink            // nil: made on first use

	lastID string
}

// entryColor groups audit actions: removals red, creations green, the rest blue.
func entryColor(action int) int {
	switch action {
	case 12, 15, 20, 22, 32, 41, 52, 72, 73, 142, 21:
		return colRed
	case 10, 13, 30, 40, 50, 140, 28, 23:
		return colGreen
	case 143, 145:
		return colOrange
	}
	return colBlue
}

func snowflake(s string) uint64 { n, _ := strconv.ParseUint(s, 10, 64); return n }

// Poll fetches entries newer than the last one seen and returns the embeds to
// post, oldest first. The first call only records the position, so a restart
// never replays history.
func (e *Events) Poll(ctx context.Context, botID string) ([]map[string]any, error) {
	q := url.Values{"limit": {"50"}}
	var page auditPage
	if err := e.API.Do(ctx, "GET", "/guilds/"+e.GuildID+"/audit-logs?"+q.Encode(), nil, &page); err != nil {
		return nil, err
	}
	first := e.lastID == ""
	var lines []map[string]any
	newest := e.lastID
	for i := len(page.Entries) - 1; i >= 0; i-- { // the API answers newest first
		en := page.Entries[i]
		if snowflake(en.ID) <= snowflake(e.lastID) {
			continue
		}
		if snowflake(en.ID) > snowflake(newest) {
			newest = en.ID
		}
		if en.UserID == botID { // the bot's own commands write their own log entries
			continue
		}
		if e.GatewayOn && en.ActionType == 40 {
			continue
		}
		at := time.Now()
		if e.Now != nil {
			at = e.Now()
		}
		lines = append(lines, formatEntry(en, at))
	}
	e.lastID = newest
	if first {
		return nil, nil
	}
	return lines, nil
}

func formatEntry(en auditEntry, at time.Time) map[string]any {
	name, ok := auditActions[en.ActionType]
	if !ok {
		name = "Событие " + strconv.Itoa(en.ActionType)
	}
	var f []map[string]any
	if en.UserID != "" {
		f = append(f, field("Кто", "<@"+en.UserID+"> ("+en.UserID+")", true))
	}
	if en.TargetID != "" {
		f = append(f, field("Цель", en.TargetID, true))
	}
	if en.Options.ChannelID != "" {
		f = append(f, field("Канал", "<#"+en.Options.ChannelID+">", true))
	}
	if en.Options.Count != "" {
		f = append(f, field("Сообщений", en.Options.Count, true))
	}
	if en.Reason != "" {
		f = append(f, field("Причина", en.Reason, false))
	}
	return embed(name, entryColor(en.ActionType), at, f...)
}

// Run polls until ctx ends. Without the View Audit Log permission it tries
// again after ten minutes and says so once in the service log.
func (e *Events) Run(ctx context.Context, logf func(string, ...any)) {
	every := e.Every
	if every == 0 {
		every = 30 * time.Second
	}
	var me struct{ ID string }
	if err := e.API.Do(ctx, "GET", "/users/@me", nil, &me); err != nil {
		logf("events: %v", err)
	}
	warned := false
	for {
		wait := every
		lines, err := e.Poll(ctx, me.ID)
		switch {
		case err != nil:
			if !warned {
				logf("events: audit log not readable (needs the View Audit Log permission): %v", err)
				warned = true
			}
			wait = 10 * time.Minute
		case len(lines) > 0:
			warned = false
			e.post(ctx, lines, logf)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

func (e *Events) post(ctx context.Context, lines []map[string]any, logf func(string, ...any)) {
	if e.Sink == nil {
		e.Sink = &Sink{API: e.API, GuildID: e.GuildID}
	}
	if err := e.Sink.Embeds(ctx, e.Channel, lines...); err != nil {
		logf("events: %v", err)
	}
}
