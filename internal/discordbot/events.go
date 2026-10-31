package discordbot

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

// The server log is built from Discord's audit log, polled over plain HTTP
// like everything else here. It covers bans, kicks, timeouts, role, channel,
// invite, webhook and message-deletion events. It cannot see joins, leaves or
// the text of edited messages: those need a gateway connection and privileged
// intents (docs/moderation.md).
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

	lastID string
}

func snowflake(s string) uint64 { n, _ := strconv.ParseUint(s, 10, 64); return n }

// Poll fetches entries newer than the last one seen and returns the lines to
// post, oldest first. The first call only records the position, so a restart
// never replays history.
func (e *Events) Poll(ctx context.Context, botID string) ([]string, error) {
	q := url.Values{"limit": {"50"}}
	var page auditPage
	if err := e.API.Do(ctx, "GET", "/guilds/"+e.GuildID+"/audit-logs?"+q.Encode(), nil, &page); err != nil {
		return nil, err
	}
	first := e.lastID == ""
	var lines []string
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
		lines = append(lines, formatEntry(en))
	}
	e.lastID = newest
	if first {
		return nil, nil
	}
	return lines, nil
}

func formatEntry(en auditEntry) string {
	name, ok := auditActions[en.ActionType]
	if !ok {
		name = "Событие " + strconv.Itoa(en.ActionType)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**", name)
	if en.UserID != "" {
		fmt.Fprintf(&b, " · кто: <@%s>", en.UserID)
	}
	if en.TargetID != "" {
		fmt.Fprintf(&b, " · цель: %s", en.TargetID)
	}
	if en.Options.ChannelID != "" {
		fmt.Fprintf(&b, " · канал: <#%s>", en.Options.ChannelID)
	}
	if en.Options.Count != "" {
		fmt.Fprintf(&b, " · сообщений: %s", en.Options.Count)
	}
	if en.Reason != "" {
		fmt.Fprintf(&b, " · причина: %s", en.Reason)
	}
	return b.String()
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

func (e *Events) post(ctx context.Context, lines []string, logf func(string, ...any)) {
	var chans []struct{ ID, Name string }
	if err := e.API.Do(ctx, "GET", "/guilds/"+e.GuildID+"/channels", nil, &chans); err != nil {
		logf("events: %v", err)
		return
	}
	id := ""
	for _, c := range chans {
		if c.Name == e.Channel {
			id = c.ID
		}
	}
	if id == "" {
		logf("events: channel %q not found", e.Channel)
		return
	}
	// one message holds up to 2000 characters
	for len(lines) > 0 {
		var b strings.Builder
		n := 0
		for n < len(lines) && b.Len()+len(lines[n])+1 < 1900 {
			b.WriteString(lines[n] + "\n")
			n++
		}
		if n == 0 { // a single oversized line
			b.WriteString(lines[0][:1900])
			n = 1
		}
		lines = lines[n:]
		body := map[string]any{"content": b.String(), "allowed_mentions": map[string]any{"parse": []string{}}}
		if err := e.API.Do(ctx, "POST", "/channels/"+id+"/messages", body, nil); err != nil {
			logf("events: %v", err)
			return
		}
	}
}
