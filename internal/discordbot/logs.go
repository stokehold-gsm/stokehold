package discordbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

const (
	maxCachedMessages = 5000 // old text is kept only for the latest messages, so memory stays bounded
	noContentText     = "текст недоступен: включите Message Content Intent в Developer Portal"
)

// Logs turns gateway events into the detailed log channels (messages, members,
// voice, server, AutoMod). Old message text, member profiles and voice states
// are remembered in memory, which is what lets an edit show «Было / Стало» and
// a role change show what changed. After a restart the cache starts empty and
// fills again: a message sent before that shows no old text.
type Logs struct {
	Sink *Sink
	// NoContent reports that Discord did not grant the Message Content intent.
	NoContent func() bool
	Log       func(string, ...any)

	mu      sync.Mutex
	msgs    map[string]cachedMsg
	order   []string
	members map[string]memberSnap
	voice   map[string]string
	roles   map[string]string
	threads map[string]threadSnap
	wg      sync.WaitGroup
}

type cachedMsg struct {
	Author, Name, Channel, Content string
	Bot                            bool
	Files                          []string
}

type memberSnap struct {
	Username, Global, Nick, Avatar, GuildAvatar string
	Roles                                       []string
	JoinedAt                                    string
}

type threadSnap struct {
	Name             string
	Archived, Locked bool
}

type userData struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Avatar     string `json:"avatar"`
	Bot        bool   `json:"bot"`
}

type memberData struct {
	User     userData `json:"user"`
	Nick     string   `json:"nick"`
	Avatar   string   `json:"avatar"`
	Roles    []string `json:"roles"`
	JoinedAt string   `json:"joined_at"`
}

func (m memberData) snap() memberSnap {
	return memberSnap{Username: m.User.Username, Global: m.User.GlobalName, Nick: m.Nick, Avatar: m.User.Avatar, GuildAvatar: m.Avatar, Roles: m.Roles, JoinedAt: m.JoinedAt}
}

type threadData struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
	OwnerID  string `json:"owner_id"`
	Meta     struct {
		Archived bool `json:"archived"`
		Locked   bool `json:"locked"`
	} `json:"thread_metadata"`
}

func (l *Logs) logf(f string, a ...any) {
	if l.Log != nil {
		l.Log(f, a...)
	}
}

func (l *Logs) now() time.Time { return l.Sink.now() }

// Handle takes one gateway dispatch. The cache is updated here, in event
// order; posting runs in the background so a slow Discord answer never delays
// heartbeats.
func (l *Logs) Handle(_ context.Context, name string, data json.RawMessage) {
	l.mu.Lock()
	if l.msgs == nil {
		l.msgs, l.members, l.voice, l.roles, l.threads = map[string]cachedMsg{}, map[string]memberSnap{}, map[string]string{}, map[string]string{}, map[string]threadSnap{}
	}
	ch, e := l.dispatch(name, data)
	l.mu.Unlock()
	if e == nil {
		return
	}
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := l.Sink.Embeds(ctx, ch, e); err != nil {
			l.logf("logs: %v", err)
		}
	}()
}

// dispatch updates the cache and returns the channel and embed to post, if any.
func (l *Logs) dispatch(name string, data json.RawMessage) (string, map[string]any) {
	var common struct {
		GuildID string `json:"guild_id"`
	}
	json.Unmarshal(data, &common)
	if common.GuildID != "" && common.GuildID != l.Sink.GuildID {
		return "", nil
	}
	switch name {
	case "GUILD_CREATE":
		l.guildCreate(data)
	case "GUILD_MEMBERS_CHUNK":
		var c struct {
			Members []memberData `json:"members"`
		}
		json.Unmarshal(data, &c)
		for _, m := range c.Members {
			l.members[m.User.ID] = m.snap()
		}
	case "GUILD_ROLE_CREATE", "GUILD_ROLE_UPDATE":
		var r struct {
			Role struct{ ID, Name string } `json:"role"`
		}
		json.Unmarshal(data, &r)
		l.roles[r.Role.ID] = r.Role.Name
	case "GUILD_ROLE_DELETE":
		var r struct {
			RoleID string `json:"role_id"`
		}
		json.Unmarshal(data, &r)
		delete(l.roles, r.RoleID)
	case "MESSAGE_CREATE":
		var m msgData
		json.Unmarshal(data, &m)
		l.remember(m)
	case "MESSAGE_UPDATE":
		return l.messageUpdate(data)
	case "MESSAGE_DELETE":
		return l.messageDelete(data)
	case "MESSAGE_DELETE_BULK":
		return l.messageBulk(data)
	case "GUILD_MEMBER_ADD":
		return l.memberAdd(data)
	case "GUILD_MEMBER_REMOVE":
		return l.memberRemove(data)
	case "GUILD_MEMBER_UPDATE":
		return l.memberUpdate(data)
	case "VOICE_STATE_UPDATE":
		return l.voiceUpdate(data)
	case "INVITE_CREATE", "INVITE_DELETE":
		return l.invite(name, data)
	case "THREAD_CREATE", "THREAD_UPDATE", "THREAD_DELETE":
		return l.thread(name, data)
	case "AUTO_MODERATION_ACTION_EXECUTION":
		return l.automod(data)
	}
	return "", nil
}

func (l *Logs) guildCreate(data json.RawMessage) {
	var g struct {
		Roles       []struct{ ID, Name string } `json:"roles"`
		Members     []memberData                `json:"members"`
		Threads     []threadData                `json:"threads"`
		VoiceStates []struct {
			UserID    string `json:"user_id"`
			ChannelID string `json:"channel_id"`
		} `json:"voice_states"`
	}
	json.Unmarshal(data, &g)
	for _, r := range g.Roles {
		l.roles[r.ID] = r.Name
	}
	for _, m := range g.Members {
		l.members[m.User.ID] = m.snap()
	}
	for _, t := range g.Threads {
		l.threads[t.ID] = threadSnap{t.Name, t.Meta.Archived, t.Meta.Locked}
	}
	for _, v := range g.VoiceStates {
		if v.ChannelID != "" {
			l.voice[v.UserID] = v.ChannelID
		}
	}
}

// who is «name (@mention)»: the name stays readable after the member leaves.
func who(id, name string) string {
	if name == "" {
		return "<@" + id + ">"
	}
	return name + " (<@" + id + ">)"
}

func avatarURL(id, hash string) string {
	if hash == "" {
		return ""
	}
	return "https://cdn.discordapp.com/avatars/" + id + "/" + hash + ".png"
}

func withThumb(e map[string]any, url string) map[string]any {
	if url != "" {
		e["thumbnail"] = map[string]any{"url": url}
	}
	return e
}

// Messages

type msgData struct {
	ID              string  `json:"id"`
	ChannelID       string  `json:"channel_id"`
	Content         *string `json:"content"`
	EditedTimestamp *string `json:"edited_timestamp"`
	Author          struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"author"`
	Attachments []struct {
		Filename string `json:"filename"`
		URL      string `json:"url"`
	} `json:"attachments"`
}

func (l *Logs) remember(m msgData) {
	if m.ID == "" {
		return
	}
	c := cachedMsg{Author: m.Author.ID, Name: m.Author.Username, Channel: m.ChannelID, Bot: m.Author.Bot}
	if m.Content != nil {
		c.Content = *m.Content
	}
	for _, a := range m.Attachments {
		c.Files = append(c.Files, "["+a.Filename+"]("+a.URL+")")
	}
	if _, ok := l.msgs[m.ID]; !ok {
		l.order = append(l.order, m.ID)
		if len(l.order) > maxCachedMessages {
			delete(l.msgs, l.order[0])
			l.order = l.order[1:]
		}
	}
	l.msgs[m.ID] = c
}

func (l *Logs) jump(channel, id string) string {
	return "[Перейти к сообщению](https://discord.com/channels/" + l.Sink.GuildID + "/" + channel + "/" + id + ")"
}

func (l *Logs) noContent() bool { return l.NoContent != nil && l.NoContent() }

func (l *Logs) messageUpdate(data json.RawMessage) (string, map[string]any) {
	var m msgData
	json.Unmarshal(data, &m)
	old, had := l.msgs[m.ID]
	// link previews arrive as updates without a text change or an edit mark
	if m.Content == nil || (m.EditedTimestamp == nil && had) {
		return "", nil
	}
	if m.Author.ID == "" {
		m.Author.ID, m.Author.Username, m.Author.Bot = old.Author, old.Name, old.Bot
	}
	if m.ChannelID == "" {
		m.ChannelID = old.Channel
	}
	blind := l.noContent()
	if !blind && had && old.Content == *m.Content {
		return "", nil
	}
	l.remember(m)
	if m.Author.Bot {
		return "", nil
	}
	before, after := "не было в памяти бота", *m.Content
	switch {
	case blind:
		before, after = noContentText, noContentText
	case had:
		before = old.Content
	}
	e := embed("✏️ Сообщение изменено", colYellow, l.now(),
		field("Автор", who(m.Author.ID, m.Author.Username), true),
		field("Канал", "<#"+m.ChannelID+">", true),
		field("Было", before, false), field("Стало", after, false))
	e["description"] = l.jump(m.ChannelID, m.ID)
	return discordsetup.LogMessages, e
}

func (l *Logs) messageDelete(data json.RawMessage) (string, map[string]any) {
	var d struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
	}
	json.Unmarshal(data, &d)
	old, had := l.msgs[d.ID]
	delete(l.msgs, d.ID)
	if had && old.Bot {
		return "", nil
	}
	author, text := "неизвестен", "не было в памяти бота"
	if had {
		author, text = who(old.Author, old.Name), old.Content
	}
	if l.noContent() {
		text = noContentText
	}
	f := []map[string]any{field("Автор", author, true), field("Канал", "<#"+d.ChannelID+">", true), field("Текст", text, false)}
	if had && len(old.Files) > 0 {
		f = append(f, field("Вложения", strings.Join(old.Files, "\n"), false))
	}
	return discordsetup.LogMessages, embed("🗑 Сообщение удалено", colRed, l.now(), f...)
}

func (l *Logs) messageBulk(data json.RawMessage) (string, map[string]any) {
	var d struct {
		IDs       []string `json:"ids"`
		ChannelID string   `json:"channel_id"`
	}
	json.Unmarshal(data, &d)
	var lines []string
	for _, id := range d.IDs {
		if m, ok := l.msgs[id]; ok && len(lines) < 10 && !m.Bot {
			lines = append(lines, "<@"+m.Author+">: "+clip(strings.ReplaceAll(m.Content, "\n", " "), 80))
		}
		delete(l.msgs, id)
	}
	f := []map[string]any{field("Канал", "<#"+d.ChannelID+">", true), field("Сообщений", strconv.Itoa(len(d.IDs)), true)}
	if len(lines) > 0 {
		f = append(f, field("Что было (первые)", strings.Join(lines, "\n"), false))
	}
	return discordsetup.LogMessages, embed("🧹 Массовое удаление", colRed, l.now(), f...)
}

// Members

func snowflakeTime(id string) time.Time {
	return time.UnixMilli(int64(snowflake(id)>>22) + 1420070400000)
}

func (l *Logs) memberAdd(data json.RawMessage) (string, map[string]any) {
	var m memberData
	json.Unmarshal(data, &m)
	if m.User.Bot {
		return "", nil
	}
	l.members[m.User.ID] = m.snap()
	created := snowflakeTime(m.User.ID)
	e := embed("📥 Участник зашёл", colGreen, l.now(),
		field("Участник", who(m.User.ID, m.User.Username), true),
		field("Аккаунт создан", fmt.Sprintf("<t:%d:R>", created.Unix()), true))
	return discordsetup.LogMembers, withThumb(e, avatarURL(m.User.ID, m.User.Avatar))
}

func (l *Logs) memberRemove(data json.RawMessage) (string, map[string]any) {
	var m memberData
	json.Unmarshal(data, &m)
	snap, had := l.members[m.User.ID]
	delete(l.members, m.User.ID)
	delete(l.voice, m.User.ID)
	if m.User.Bot {
		return "", nil
	}
	f := []map[string]any{field("Участник", who(m.User.ID, m.User.Username), true)}
	if had {
		if snap.JoinedAt != "" {
			if t, err := time.Parse(time.RFC3339, snap.JoinedAt); err == nil {
				f = append(f, field("На сервере с", fmt.Sprintf("<t:%d:R>", t.Unix()), true))
			}
		}
		if len(snap.Roles) > 0 {
			f = append(f, field("Роли", l.roleNames(snap.Roles), false))
		}
	}
	return discordsetup.LogMembers, withThumb(embed("📤 Участник вышел", colRed, l.now(), f...), avatarURL(m.User.ID, m.User.Avatar))
}

func (l *Logs) roleNames(ids []string) string {
	var out []string
	for _, id := range ids {
		if n := l.roles[id]; n != "" {
			out = append(out, n)
		} else {
			out = append(out, "<@&"+id+">")
		}
	}
	return strings.Join(out, ", ")
}

func diffIDs(from, to []string) (added, removed []string) {
	in := func(list []string, x string) bool {
		for _, y := range list {
			if x == y {
				return true
			}
		}
		return false
	}
	for _, x := range to {
		if !in(from, x) {
			added = append(added, x)
		}
	}
	for _, x := range from {
		if !in(to, x) {
			removed = append(removed, x)
		}
	}
	return
}

// memberUpdate reports one change per event; when several change at once the
// roles come first and the profile changes are caught by the next update.
func (l *Logs) memberUpdate(data json.RawMessage) (string, map[string]any) {
	var m memberData
	json.Unmarshal(data, &m)
	old, had := l.members[m.User.ID]
	now := m.snap()
	if m.User.Username == "" { // partial user object: keep what we knew
		now.Username, now.Global, now.Avatar = old.Username, old.Global, old.Avatar
	}
	if now.JoinedAt == "" {
		now.JoinedAt = old.JoinedAt
	}
	l.members[m.User.ID] = now
	if !had || m.User.Bot {
		return "", nil
	}
	name := who(m.User.ID, now.Username)
	if added, removed := diffIDs(old.Roles, now.Roles); len(added)+len(removed) > 0 {
		f := []map[string]any{field("Участник", name, true)}
		if len(added) > 0 {
			f = append(f, field("Выдано", l.roleNames(added), true))
		}
		if len(removed) > 0 {
			f = append(f, field("Снято", l.roleNames(removed), true))
		}
		return discordsetup.LogMembers, embed("🎭 Роли изменены", colPurple, l.now(), f...)
	}
	if old.Nick != now.Nick {
		return discordsetup.LogMembers, embed("✏️ Ник изменён", colBlue, l.now(),
			field("Участник", name, true), field("Было → стало", orNone(old.Nick)+" → "+orNone(now.Nick), false))
	}
	if old.Username != now.Username || old.Global != now.Global {
		return discordsetup.LogMembers, withThumb(embed("👤 Профиль изменён", colBlue, l.now(),
			field("Участник", name, true),
			field("Имя", orNone(old.Username)+" → "+orNone(now.Username)+"\nОтображаемое: "+orNone(old.Global)+" → "+orNone(now.Global), false)), avatarURL(m.User.ID, now.Avatar))
	}
	if old.Avatar != now.Avatar || old.GuildAvatar != now.GuildAvatar {
		return discordsetup.LogMembers, withThumb(embed("👤 Профиль изменён", colBlue, l.now(),
			field("Участник", name, true), field("Аватар", "изменён", false)), avatarURL(m.User.ID, now.Avatar))
	}
	return "", nil
}

func orNone(s string) string {
	if s == "" {
		return "нет"
	}
	return s
}

// Voice

func (l *Logs) voiceUpdate(data json.RawMessage) (string, map[string]any) {
	var v struct {
		UserID    string     `json:"user_id"`
		ChannelID string     `json:"channel_id"`
		Member    memberData `json:"member"`
	}
	json.Unmarshal(data, &v)
	was := l.voice[v.UserID]
	if v.ChannelID == "" {
		delete(l.voice, v.UserID)
	} else {
		l.voice[v.UserID] = v.ChannelID
	}
	if was == v.ChannelID || v.Member.User.Bot {
		return "", nil // mute, deafen and camera changes keep the channel
	}
	name := who(v.UserID, v.Member.User.Username)
	switch {
	case was == "":
		return discordsetup.LogVoice, embed("🔊 Вход в голосовой", colGreen, l.now(), field("Участник", name, true), field("Канал", "<#"+v.ChannelID+">", true))
	case v.ChannelID == "":
		return discordsetup.LogVoice, embed("🔇 Выход из голосового", colOrange, l.now(), field("Участник", name, true), field("Канал", "<#"+was+">", true))
	}
	return discordsetup.LogVoice, embed("🔀 Перемещение", colBlue, l.now(), field("Участник", name, true), field("Из", "<#"+was+">", true), field("В", "<#"+v.ChannelID+">", true))
}

// Invites and threads

func (l *Logs) invite(name string, data json.RawMessage) (string, map[string]any) {
	var i struct {
		Code      string   `json:"code"`
		ChannelID string   `json:"channel_id"`
		Inviter   userData `json:"inviter"`
		MaxAge    int      `json:"max_age"`
		MaxUses   int      `json:"max_uses"`
		Temporary bool     `json:"temporary"`
	}
	json.Unmarshal(data, &i)
	if name == "INVITE_DELETE" {
		return discordsetup.LogServer, embed("🔗 Приглашение удалено", colRed, l.now(), field("Код", i.Code, true), field("Канал", "<#"+i.ChannelID+">", true))
	}
	limit, age := "∞", "∞"
	if i.MaxUses > 0 {
		limit = strconv.Itoa(i.MaxUses)
	}
	if i.MaxAge > 0 {
		age = strconv.Itoa(i.MaxAge/3600) + " ч."
	}
	f := []map[string]any{field("Код", i.Code, true)}
	if i.Inviter.ID != "" {
		f = append(f, field("Автор", who(i.Inviter.ID, i.Inviter.Username), true))
	}
	f = append(f, field("Канал", "<#"+i.ChannelID+">", true), field("Лимит", limit, true), field("Срок", age, true))
	if i.Temporary {
		f = append(f, field("Временное", "да, участник уйдёт при выходе из сети", false))
	}
	return discordsetup.LogServer, embed("🔗 Создано приглашение", colGreen, l.now(), f...)
}

func (l *Logs) thread(name string, data json.RawMessage) (string, map[string]any) {
	var t threadData
	json.Unmarshal(data, &t)
	label := "<#" + t.ID + "> (" + t.Name + ")"
	switch name {
	case "THREAD_CREATE":
		l.threads[t.ID] = threadSnap{t.Name, t.Meta.Archived, t.Meta.Locked}
		f := []map[string]any{field("Ветка", label, true), field("Родитель", "<#"+t.ParentID+">", true)}
		if t.OwnerID != "" {
			f = append(f, field("Автор", "<@"+t.OwnerID+">", true))
		}
		return discordsetup.LogServer, embed("🧵 Ветка создана", colGreen, l.now(), f...)
	case "THREAD_DELETE":
		old := l.threads[t.ID]
		delete(l.threads, t.ID)
		if old.Name != "" {
			t.Name = old.Name
		}
		return discordsetup.LogServer, embed("🧵 Ветка удалена", colRed, l.now(), field("Ветка", t.Name, true), field("Родитель", "<#"+t.ParentID+">", true))
	}
	old, had := l.threads[t.ID]
	l.threads[t.ID] = threadSnap{t.Name, t.Meta.Archived, t.Meta.Locked}
	if !had {
		return "", nil
	}
	f := []map[string]any{field("Ветка", label, true)}
	if old.Name != t.Name {
		f = append(f, field("Название", old.Name+" → "+t.Name, true))
	}
	if old.Locked != t.Meta.Locked {
		f = append(f, field("Блокировка", map[bool]string{true: "закрыта", false: "снята"}[t.Meta.Locked], true))
	}
	if old.Archived != t.Meta.Archived {
		f = append(f, field("Архив", map[bool]string{true: "в архиве", false: "из архива"}[t.Meta.Archived], true))
	}
	if len(f) == 1 {
		return "", nil
	}
	return discordsetup.LogServer, embed("🧵 Ветка изменена", colBlue, l.now(), f...)
}

// AutoMod

var triggerNames = map[int]string{1: "ключевые слова", 3: "спам", 4: "набор слов Discord", 5: "массовые упоминания", 6: "профиль участника"}
var actionNames = map[int]string{1: "сообщение заблокировано", 2: "оповещение", 3: "таймаут", 4: "взаимодействие заблокировано"}

func (l *Logs) automod(data json.RawMessage) (string, map[string]any) {
	var a struct {
		Action struct {
			Type int `json:"type"`
		} `json:"action"`
		TriggerType    int    `json:"rule_trigger_type"`
		UserID         string `json:"user_id"`
		ChannelID      string `json:"channel_id"`
		Content        string `json:"content"`
		MatchedContent string `json:"matched_content"`
	}
	json.Unmarshal(data, &a)
	f := []map[string]any{
		field("Участник", "<@"+a.UserID+"> ("+a.UserID+")", true),
		field("Правило", triggerNames[a.TriggerType], true),
		field("Действие", actionNames[a.Action.Type], true),
	}
	if a.ChannelID != "" {
		f = append(f, field("Канал", "<#"+a.ChannelID+">", true))
	}
	if a.MatchedContent != "" {
		f = append(f, field("Совпало", a.MatchedContent, true))
	}
	if a.Content != "" {
		f = append(f, field("Текст", a.Content, false))
	}
	return discordsetup.LogAutomod, embed("🛡 AutoMod сработал", colOrange, l.now(), f...)
}

// Command logs one slash command into the bot log: who ran what and where.
func (l *Logs) Command(ctx context.Context, in interaction) {
	var args []string
	for _, o := range in.Data.Options {
		args = append(args, o.Name+":"+opt(in.Data.Options, o.Name))
	}
	e := embed("⌨️ /"+in.Data.Name, colGrey, l.now(),
		field("Кто", who(in.Member.User.ID, in.Member.User.Username), true),
		field("Канал", "<#"+in.ChannelID+">", true))
	if len(args) > 0 {
		e["description"] = clip(strings.Join(args, " "), 1500)
	}
	if err := l.Sink.Embeds(ctx, discordsetup.BotLogChannel, e); err != nil {
		l.logf("logs: %v", err)
	}
}
