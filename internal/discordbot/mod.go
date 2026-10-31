package discordbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

const (
	typeCommand = 2
	respDefer   = 5

	maxTimeout = 28 * 24 * 60 // Discord's own limit, in minutes
)

// Command definitions for registration (PUT /applications/{id}/guilds/{id}/commands).
// Every command is hidden from members without the timeout permission, but the
// real check is rankOf below: Discord's visibility settings can be edited by
// any server admin.
func Commands() []map[string]any {
	user := map[string]any{"type": 6, "name": "user", "description": "Участник", "required": true}
	reason := map[string]any{"type": 3, "name": "reason", "description": "Причина и пункт правил", "required": true, "max_length": 400}
	cmd := func(name, desc string, opts ...map[string]any) map[string]any {
		return map[string]any{
			"name": name, "description": desc, "options": opts,
			"default_member_permissions": strconv.FormatInt(discordsetup.PermModerate, 10),
			"dm_permission":              false,
		}
	}
	return append(append(ReportCommands(), AppealCommands()...),
		cmd("warn", "Выдать предупреждение", user, reason),
		cmd("warnings", "Показать предупреждения участника", user),
		cmd("timeout", "Отправить в таймаут", user,
			map[string]any{"type": 4, "name": "minutes", "description": "Минуты (хелпер: до 10)", "required": true, "min_value": 1, "max_value": maxTimeout}, reason),
		cmd("untimeout", "Снять таймаут", user),
		cmd("kick", "Исключить с сервера", user, reason),
		cmd("ban", "Забанить (кураторы)", user, reason),
		cmd("unban", "Разбанить по id (кураторы)", map[string]any{"type": 3, "name": "user_id", "description": "id пользователя", "required": true, "max_length": 25}, reason),
		cmd("purge", "Удалить последние сообщения в канале", map[string]any{"type": 4, "name": "count", "description": "Сколько, 2-100", "required": true, "min_value": 2, "max_value": 100}),
	)
}

// Ranks of the staff roles. The shop roles protect their holders from
// moderation but give no moderation rights.
const (
	rankHelper  = 1
	rankMod     = 2
	rankCurator = 3
	rankFounder = 4
)

var roleRank = map[string]int{
	discordsetup.RoleFounder: rankFounder, discordsetup.RoleCurator: rankCurator,
	discordsetup.RoleMod: rankMod, discordsetup.RoleHelper: rankHelper,
	discordsetup.RoleShopCurator: rankCurator, discordsetup.RoleOperator: rankMod,
}

type Warning struct {
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Reason string    `json:"reason"`
}

type Mod struct {
	API       *discordsetup.Client
	LogName   string // name of the moderation log channel
	Warns     *Warns
	Now       func() time.Time
	Cases     func() int // numbers log entries as «Дело #N»; nil leaves them unnumbered
	Record    func(Case) // remembers numbered warnings, timeouts, kicks and bans for appeals; nil skips it
	logChanID string
}

func (m *Mod) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

type option struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

func opt(opts []option, name string) string {
	for _, o := range opts {
		if o.Name == name {
			var s string
			if json.Unmarshal(o.Value, &s) == nil {
				return s
			}
			return strings.Trim(string(o.Value), `"`)
		}
	}
	return ""
}

func (m *Mod) run(ctx context.Context, in interaction) string {
	var roles []struct{ ID, Name string }
	if err := m.API.Do(ctx, "GET", "/guilds/"+in.GuildID+"/roles", nil, &roles); err != nil {
		return "Не удалось получить роли, попробуйте позже."
	}
	names := map[string]string{}
	for _, r := range roles {
		names[r.ID] = r.Name
	}
	rank := 0
	for _, id := range in.Member.Roles {
		if r := roleRank[names[id]]; !shopRole[names[id]] {
			rank = max(rank, r)
		}
	}
	cmd := in.Data.Name
	need := map[string]int{"warn": rankMod, "warnings": rankHelper, "timeout": rankHelper, "untimeout": rankMod,
		"kick": rankMod, "ban": rankCurator, "unban": rankCurator, "purge": rankMod}
	if need[cmd] == 0 {
		return "Неизвестная команда."
	}
	if rank < need[cmd] {
		return "У вас нет прав на эту команду. Порядок описан в канале устава персонала."
	}
	by := in.Member.User.ID
	reason := opt(in.Data.Options, "reason")

	if cmd == "purge" {
		return m.purge(ctx, in, opt(in.Data.Options, "count"))
	}
	target := opt(in.Data.Options, "user")
	if cmd == "unban" {
		target = strings.TrimSpace(opt(in.Data.Options, "user_id"))
		if _, err := strconv.ParseUint(target, 10, 64); err != nil {
			return "Это не похоже на id пользователя."
		}
		if err := m.API.Do(ctx, "DELETE", "/guilds/"+in.GuildID+"/bans/"+target, nil, nil); err != nil {
			return "Не удалось снять бан (его может не быть)."
		}
		m.log(ctx, in.GuildID, "🔓 Разбан", target, by, reason, "")
		return "Бан снят."
	}
	if target == "" || target == by {
		return "Укажите другого участника."
	}
	var member struct {
		User  struct{ Bot bool } `json:"user"`
		Roles []string           `json:"roles"`
	}
	if err := m.API.Do(ctx, "GET", "/guilds/"+in.GuildID+"/members/"+target, nil, &member); err != nil {
		if cmd == "ban" { // may ban someone who already left
			member.Roles = nil
		} else {
			return "Участника нет на сервере."
		}
	}
	if member.User.Bot {
		return "Ботов модерируют кураторы вручную."
	}
	for _, id := range member.Roles {
		if r := roleRank[names[id]]; r > 0 && r >= rank {
			return "Это сотрудник. Решения о персонале принимают кураторы."
		}
	}
	g := "/guilds/" + in.GuildID
	switch cmd {
	case "warn":
		at := m.now()
		n := m.Warns.Add(target, Warning{At: at, By: by, Reason: reason})
		recent := m.Warns.Count(target, m.now().AddDate(0, 0, -30))
		m.punished(ctx, in.GuildID, m.nextCase(), "⚠️ Предупреждение", fmt.Sprintf("всего %d, за 30 дней %d", n, recent), Case{Kind: kindWarn, Target: target, By: by, Reason: reason, At: at})
		hint := ""
		switch {
		case recent >= 5:
			hint = " Пять предупреждений за 30 дней: по правилам это исключение."
		case recent >= 3:
			hint = " Три предупреждения за 30 дней: по правилам это таймаут на сутки."
		}
		return fmt.Sprintf("Предупреждение записано (за 30 дней: %d).%s", recent, hint)
	case "warnings":
		list := m.Warns.List(target, m.now().AddDate(0, 0, -90))
		if len(list) == 0 {
			return "За последние 90 дней предупреждений нет."
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Предупреждения за 90 дней (%d):\n", len(list))
		for _, w := range list {
			fmt.Fprintf(&b, "• %s <@%s>: %s\n", w.At.UTC().Format("2006-01-02"), w.By, w.Reason)
		}
		return b.String()
	case "timeout":
		mins, _ := strconv.Atoi(opt(in.Data.Options, "minutes"))
		if mins < 1 || mins > maxTimeout {
			return "Недопустимый срок."
		}
		if rank == rankHelper && mins > 10 {
			return "Хелпер может выдать таймаут не больше 10 минут. Позовите модератора."
		}
		if rank == rankMod && mins > 7*24*60 {
			return "Модератор может выдать таймаут до 7 дней. Дольше решает куратор."
		}
		until := m.now().Add(time.Duration(mins) * time.Minute).UTC().Format(time.RFC3339)
		if err := m.API.Do(ctx, "PATCH", g+"/members/"+target, map[string]any{"communication_disabled_until": until}, nil); err != nil {
			return "Не удалось выдать таймаут."
		}
		m.punished(ctx, in.GuildID, m.nextCase(), "🔇 Таймаут", fmt.Sprintf("%d мин", mins), Case{Kind: kindTimeout, Target: target, By: by, Reason: reason, At: m.now(), Minutes: mins})
		return fmt.Sprintf("Таймаут на %d мин выдан.", mins)
	case "untimeout":
		if err := m.API.Do(ctx, "PATCH", g+"/members/"+target, map[string]any{"communication_disabled_until": nil}, nil); err != nil {
			return "Не удалось снять таймаут."
		}
		m.log(ctx, in.GuildID, "🔈 Таймаут снят", target, by, "", "")
		return "Таймаут снят."
	case "kick":
		// The member must hear the case number before they lose the shared server.
		c, n := Case{Kind: kindKick, Target: target, By: by, Reason: reason, At: m.now()}, m.nextCase()
		m.dmCase(ctx, n, c)
		if err := m.API.Do(ctx, "DELETE", g+"/members/"+target, nil, nil); err != nil {
			return "Не удалось исключить участника."
		}
		m.logN(ctx, in.GuildID, n, "👢 Исключение", target, by, reason, "")
		m.record(n, c)
		return "Участник исключён."
	default: // ban
		if err := m.API.Do(ctx, "PUT", g+"/bans/"+target, map[string]any{}, nil); err != nil {
			return "Бан не прошёл: у бота нет права Ban Members или его роль ниже."
		}
		m.record(m.log(ctx, in.GuildID, "🔨 Бан", target, by, reason, ""), Case{Kind: kindBan, Target: target, By: by, Reason: reason, At: m.now()})
		return "Участник забанен."
	}
}

func (m *Mod) purge(ctx context.Context, in interaction, countStr string) string {
	n, err := strconv.Atoi(countStr)
	if err != nil || n < 2 || n > 100 {
		return "Укажите число от 2 до 100."
	}
	var msgs []struct {
		ID string `json:"id"`
	}
	q := url.Values{"limit": {strconv.Itoa(n)}}
	if err := m.API.Do(ctx, "GET", "/channels/"+in.ChannelID+"/messages?"+q.Encode(), nil, &msgs); err != nil {
		return "Не удалось прочитать сообщения."
	}
	var ids []string
	for _, x := range msgs {
		ids = append(ids, x.ID)
	}
	if len(ids) < 2 {
		return "Нечего удалять."
	}
	if err := m.API.Do(ctx, "POST", "/channels/"+in.ChannelID+"/messages/bulk-delete", map[string]any{"messages": ids}, nil); err != nil {
		return "Не удалось удалить (сообщения старше 14 дней Discord удалить массово не даёт)."
	}
	m.log(ctx, in.GuildID, "🧹 Очистка", "", in.Member.User.ID, "", fmt.Sprintf("%d сообщений в <#%s>", len(ids), in.ChannelID))
	return fmt.Sprintf("Удалено сообщений: %d.", len(ids))
}

// log writes one entry to the moderation log channel. A failure to log never
// undoes the action, it only shows up in the bot's own log.
// It returns the case number, 0 when entries are not numbered.
func (m *Mod) log(ctx context.Context, guildID, what, target, by, reason, extra string) int {
	return m.logN(ctx, guildID, m.nextCase(), what, target, by, reason, extra)
}

func (m *Mod) nextCase() int {
	if m.Cases == nil {
		return 0
	}
	return m.Cases()
}

// punished logs a numbered punishment, remembers it for appeals and tells the member.
func (m *Mod) punished(ctx context.Context, guildID string, n int, title, extra string, c Case) {
	m.logN(ctx, guildID, n, title, c.Target, c.By, c.Reason, extra)
	m.record(n, c)
	m.dmCase(ctx, n, c)
}

// dmCase gives the member the case number they need for /appeal. A ban is
// not announced: a banned member shares no server with the bot.
func (m *Mod) dmCase(ctx context.Context, n int, c Case) {
	if n == 0 || c.Kind == kindBan {
		return
	}
	dmUser(ctx, m.API, c.Target, fmt.Sprintf("На сервере вам вынесено наказание: %s (дело #%d).\nПричина: %s\nЕсли не согласны, подайте апелляцию командой /appeal в течение 14 дней, номер дела %d.",
		strings.ToLower(kindLabel[c.Kind]), n, c.Reason, n))
}

func (m *Mod) logN(ctx context.Context, guildID string, n int, what, target, by, reason, extra string) int {
	fields := []map[string]any{{"name": "Кто", "value": "<@" + by + ">", "inline": true}}
	if target != "" {
		fields = append(fields, map[string]any{"name": "Участник", "value": "<@" + target + "> (" + target + ")", "inline": true})
	}
	if reason != "" {
		fields = append(fields, map[string]any{"name": "Причина", "value": reason})
	}
	if extra != "" {
		fields = append(fields, map[string]any{"name": "Детали", "value": extra})
	}
	if n > 0 {
		what = fmt.Sprintf("Дело #%d · %s", n, what)
	}
	m.logEmbed(ctx, guildID, embed(what, colYellow, m.now(), fields...))
	return n
}

// record keeps a numbered punishment so that it can be appealed later.
func (m *Mod) record(n int, c Case) {
	if n > 0 && m.Record != nil {
		c.N = n
		m.Record(c)
	}
}

// logEmbed posts a ready embed to the moderation log.
func (m *Mod) logEmbed(ctx context.Context, guildID string, e map[string]any) {
	id := m.channel(ctx, guildID)
	if id == "" {
		return
	}
	body := map[string]any{"embeds": []map[string]any{e}, "allowed_mentions": map[string]any{"parse": []string{}}}
	_ = m.API.Do(ctx, "POST", "/channels/"+id+"/messages", body, nil)
}

func (m *Mod) channel(ctx context.Context, guildID string) string {
	if m.logChanID != "" {
		return m.logChanID
	}
	var chans []struct{ ID, Name string }
	if m.API.Do(ctx, "GET", "/guilds/"+guildID+"/channels", nil, &chans) != nil {
		return ""
	}
	for _, c := range chans {
		if c.Name == m.LogName {
			m.logChanID = c.ID
		}
	}
	return m.logChanID
}

// Warns keeps warnings in memory and in a small JSON file.
type Warns struct {
	mu   sync.Mutex
	path string
	data map[string][]Warning
}

func OpenWarns(path string) (*Warns, error) {
	w := &Warns{path: path, data: map[string][]Warning{}}
	if path == "" {
		return w, nil
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return w, nil // a missing file is a fresh start
	}
	return w, json.Unmarshal(b, &w.data)
}

func (w *Warns) Add(user string, x Warning) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data[user] = append(w.data[user], x)
	w.save()
	return len(w.data[user])
}

func (w *Warns) List(user string, since time.Time) []Warning {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []Warning
	for _, x := range w.data[user] {
		if x.At.After(since) {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

func (w *Warns) Count(user string, since time.Time) int { return len(w.List(user, since)) }

// Remove drops one warning, found by who gave it and when; false if it is gone.
func (w *Warns) Remove(user, by string, at time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, x := range w.data[user] {
		if x.By == by && x.At.Equal(at) {
			w.data[user] = append(w.data[user][:i:i], w.data[user][i+1:]...)
			w.save()
			return true
		}
	}
	return false
}

func (w *Warns) save() {
	if w.path == "" {
		return
	}
	b, _ := json.Marshal(w.data)
	_ = os.WriteFile(w.path, b, 0o600)
}

// shop staff are protected from moderators but cannot moderate themselves
var shopRole = map[string]bool{discordsetup.RoleShopCurator: true, discordsetup.RoleOperator: true}
