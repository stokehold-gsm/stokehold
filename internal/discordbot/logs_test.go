package discordbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func logRig(t *testing.T) (*Logs, *guild) {
	h, g := modRig(t)
	return &Logs{Sink: &Sink{API: h.API, GuildID: "g", Now: func() time.Time { return now }}}, g
}

func (l *Logs) feed(name, payload string) {
	l.Handle(context.Background(), name, json.RawMessage(payload))
	l.wg.Wait()
}

func (g *guild) posted(chanID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.byPath["/channels/"+chanID+"/messages"]
}

func mustContain(t *testing.T, posts []string, want ...string) {
	t.Helper()
	if len(posts) == 0 {
		t.Fatalf("nothing was posted, want %v", want)
	}
	last := strings.NewReplacer("\\"+"u003c", "<", "\\"+"u003e", ">", "\\"+"u0026", "&").Replace(posts[len(posts)-1]) // Go escapes angle brackets in JSON
	for _, w := range want {
		if !strings.Contains(last, w) {
			t.Errorf("post lacks %q:\n%s", w, last)
		}
	}
}

func TestEditedMessageShowsOldAndNewText(t *testing.T) {
	l, g := logRig(t)
	l.feed("MESSAGE_CREATE", `{"id":"m1","channel_id":"c1","guild_id":"g","content":"привет","author":{"id":"u1","username":"ivan"}}`)
	l.feed("MESSAGE_UPDATE", `{"id":"m1","channel_id":"c1","guild_id":"g","content":"пока","edited_timestamp":"2026-11-01T12:00:00Z","author":{"id":"u1","username":"ivan"}}`)
	mustContain(t, g.posted("cLm"), "Сообщение изменено", "привет", "пока", "<@u1>", "<#c1>", "channels/g/c1/m1", `"parse":[]`)

	n := len(g.posted("cLm"))
	// a link preview updates the message without touching the text
	l.feed("MESSAGE_UPDATE", `{"id":"m1","channel_id":"c1","guild_id":"g","content":"пока","embeds":[{}]}`)
	l.feed("MESSAGE_UPDATE", `{"id":"m1","channel_id":"c1","guild_id":"g","embeds":[{}]}`)
	if len(g.posted("cLm")) != n {
		t.Error("an update without an edit must not be logged")
	}
}

func TestEditOfUnseenMessageSaysSo(t *testing.T) {
	l, g := logRig(t)
	l.feed("MESSAGE_UPDATE", `{"id":"old","channel_id":"c1","guild_id":"g","content":"новый","edited_timestamp":"2026-11-01T12:00:00Z","author":{"id":"u1","username":"ivan"}}`)
	mustContain(t, g.posted("cLm"), "не было в памяти бота", "новый")
}

func TestDeletedMessageShowsTextAndFiles(t *testing.T) {
	l, g := logRig(t)
	l.feed("MESSAGE_CREATE", `{"id":"m2","channel_id":"c1","guild_id":"g","content":"секрет","author":{"id":"u1","username":"ivan"},"attachments":[{"filename":"a.png","url":"https://cdn/a.png"}]}`)
	l.feed("MESSAGE_DELETE", `{"id":"m2","channel_id":"c1","guild_id":"g"}`)
	mustContain(t, g.posted("cLm"), "Сообщение удалено", "секрет", "a.png", "<@u1>")
}

func TestBotMessagesAreNotLogged(t *testing.T) {
	l, g := logRig(t)
	l.feed("MESSAGE_CREATE", `{"id":"b1","channel_id":"c1","guild_id":"g","content":"x","author":{"id":"bot","username":"bot","bot":true}}`)
	l.feed("MESSAGE_DELETE", `{"id":"b1","channel_id":"c1","guild_id":"g"}`)
	if len(g.posted("cLm")) != 0 {
		t.Error("bot messages are noise")
	}
}

func TestWithoutContentIntentTheLogSaysWhy(t *testing.T) {
	l, g := logRig(t)
	l.NoContent = func() bool { return true }
	l.feed("MESSAGE_UPDATE", `{"id":"m","channel_id":"c1","guild_id":"g","content":"","edited_timestamp":"2026-11-01T12:00:00Z","author":{"id":"u1","username":"ivan"}}`)
	mustContain(t, g.posted("cLm"), "Message Content Intent")
}

func TestMessageCacheIsBounded(t *testing.T) {
	l, _ := logRig(t)
	for i := 0; i < maxCachedMessages+50; i++ {
		l.feed("MESSAGE_CREATE", fmt.Sprintf(`{"id":"%d","channel_id":"c","guild_id":"g","content":"x","author":{"id":"u"}}`, i))
	}
	if len(l.msgs) != maxCachedMessages || len(l.order) != maxCachedMessages {
		t.Errorf("cache holds %d messages", len(l.msgs))
	}
	if _, ok := l.msgs["0"]; ok {
		t.Error("the oldest message should have been dropped")
	}
}

func TestOtherGuildsAreIgnored(t *testing.T) {
	l, g := logRig(t)
	l.feed("INVITE_CREATE", `{"guild_id":"elsewhere","code":"x","channel_id":"c"}`)
	if len(g.posted("cLs")) != 0 {
		t.Error("an event of another server was logged")
	}
}

func TestMemberJoinLeaveAndRoles(t *testing.T) {
	l, g := logRig(t)
	l.feed("GUILD_CREATE", `{"id":"g","roles":[{"id":"r1","name":"Moderator"},{"id":"r2","name":"Pro"}],"members":[{"user":{"id":"u1","username":"ivan"},"roles":["r2"],"joined_at":"2026-10-01T00:00:00Z"}]}`)

	l.feed("GUILD_MEMBER_ADD", `{"guild_id":"g","user":{"id":"u9","username":"newbie","avatar":"abc"},"roles":[]}`)
	mustContain(t, g.posted("cLmem"), "Участник зашёл", "newbie", "<@u9>", "<t:", "avatars/u9/abc")

	l.feed("GUILD_MEMBER_UPDATE", `{"guild_id":"g","user":{"id":"u1","username":"ivan"},"roles":["r1","r2"]}`)
	mustContain(t, g.posted("cLmem"), "Роли изменены", "Moderator")
	if strings.Contains(g.posted("cLmem")[len(g.posted("cLmem"))-1], "Снято") {
		t.Error("nothing was removed")
	}

	l.feed("GUILD_MEMBER_UPDATE", `{"guild_id":"g","user":{"id":"u1","username":"ivan"},"roles":["r1","r2"],"nick":"Ваня"}`)
	mustContain(t, g.posted("cLmem"), "Ник изменён", "нет → Ваня")

	l.feed("GUILD_MEMBER_UPDATE", `{"guild_id":"g","user":{"id":"u1","username":"ivan","avatar":"new"},"roles":["r1","r2"],"nick":"Ваня"}`)
	mustContain(t, g.posted("cLmem"), "Профиль изменён", "Аватар", "avatars/u1/new")

	l.feed("GUILD_MEMBER_UPDATE", `{"guild_id":"g","user":{"id":"u1","username":"ivan2","avatar":"new"},"roles":["r1","r2"],"nick":"Ваня"}`)
	mustContain(t, g.posted("cLmem"), "Профиль изменён", "ivan → ivan2")

	l.feed("GUILD_MEMBER_REMOVE", `{"guild_id":"g","user":{"id":"u1","username":"ivan"}}`)
	mustContain(t, g.posted("cLmem"), "Участник вышел", "Moderator, Pro", "На сервере с")
}

func TestMemberUpdateWithoutSnapshotIsSilent(t *testing.T) {
	l, g := logRig(t)
	l.feed("GUILD_MEMBER_UPDATE", `{"guild_id":"g","user":{"id":"u1","username":"ivan"},"roles":["r1"]}`)
	if len(g.posted("cLmem")) != 0 {
		t.Error("no old state, nothing to compare")
	}
}

func TestVoiceJoinMoveLeave(t *testing.T) {
	l, g := logRig(t)
	vs := func(ch string) string {
		c := "null"
		if ch != "" {
			c = `"` + ch + `"`
		}
		return `{"guild_id":"g","user_id":"u1","channel_id":` + c + `,"member":{"user":{"id":"u1","username":"ivan"}}}`
	}
	l.feed("VOICE_STATE_UPDATE", vs("v1"))
	mustContain(t, g.posted("cLv"), "Вход в голосовой", "<#v1>", "ivan")
	l.feed("VOICE_STATE_UPDATE", vs("v1")) // muted: same channel
	if len(g.posted("cLv")) != 1 {
		t.Error("a mute is not a move")
	}
	l.feed("VOICE_STATE_UPDATE", vs("v2"))
	mustContain(t, g.posted("cLv"), "Перемещение", "<#v1>", "<#v2>")
	l.feed("VOICE_STATE_UPDATE", vs(""))
	mustContain(t, g.posted("cLv"), "Выход из голосового", "<#v2>")
}

func TestInviteAndThreadEvents(t *testing.T) {
	l, g := logRig(t)
	l.feed("INVITE_CREATE", `{"guild_id":"g","code":"abc123","channel_id":"c5","inviter":{"id":"u1","username":"ivan"},"max_age":2592000,"max_uses":0}`)
	mustContain(t, g.posted("cLs"), "Создано приглашение", "abc123", "720 ч.", "∞", "ivan")

	l.feed("THREAD_CREATE", `{"guild_id":"g","id":"t1","name":"изолятор","parent_id":"c7","owner_id":"u1","thread_metadata":{}}`)
	mustContain(t, g.posted("cLs"), "Ветка создана", "изолятор", "<#c7>")
	l.feed("THREAD_UPDATE", `{"guild_id":"g","id":"t1","name":"изолятор","parent_id":"c7","thread_metadata":{"locked":true}}`)
	mustContain(t, g.posted("cLs"), "Ветка изменена", "закрыта")
	l.feed("THREAD_UPDATE", `{"guild_id":"g","id":"t1","name":"изолятор","parent_id":"c7","thread_metadata":{"locked":true,"archived":true}}`)
	mustContain(t, g.posted("cLs"), "в архиве")
	l.feed("THREAD_DELETE", `{"guild_id":"g","id":"t1","parent_id":"c7"}`)
	mustContain(t, g.posted("cLs"), "Ветка удалена", "изолятор")
}

func TestAutomodExecution(t *testing.T) {
	l, g := logRig(t)
	l.feed("AUTO_MODERATION_ACTION_EXECUTION", `{"guild_id":"g","action":{"type":1},"rule_trigger_type":3,"user_id":"u1","channel_id":"c1","content":"купи","matched_content":"купи"}`)
	mustContain(t, g.posted("cLa"), "AutoMod сработал", "спам", "сообщение заблокировано", "<@u1>")
}

func TestBulkDeleteListsWhatWasThere(t *testing.T) {
	l, g := logRig(t)
	l.feed("MESSAGE_CREATE", `{"id":"a","channel_id":"c1","guild_id":"g","content":"первое","author":{"id":"u1"}}`)
	l.feed("MESSAGE_DELETE_BULK", `{"ids":["a","b"],"channel_id":"c1","guild_id":"g"}`)
	mustContain(t, g.posted("cLm"), "Массовое удаление", "первое", `"value":"2"`)
}

func TestCommandIsLoggedWithOptions(t *testing.T) {
	l, g := logRig(t)
	in := cmd("warn", nil, map[string]any{"user": "victim", "reason": "п.2"})
	in.Member.User.Username = "mod"
	l.Command(context.Background(), in)
	mustContain(t, g.posted("cBot"), "/warn", "user:victim", "reason:п.2", "<@invoker>", "<#cx>")
}

func TestAuditEntriesBecomeEmbeds(t *testing.T) {
	e := formatEntry(auditEntry{ID: "1", UserID: "u1", TargetID: "u2", ActionType: 22, Reason: "спам"}, now)
	if e["title"] != "Бан" || e["color"] != colRed || !strings.Contains(fmt.Sprint(e["fields"]), "спам") {
		t.Errorf("%v", e)
	}
}
