package discordbot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reportRig(t *testing.T) (*Handler, *guild, *time.Time) {
	h, g := modRig(t)
	clock := now
	h.Reports = &Reports{API: h.API, Sink: &Sink{API: h.API, GuildID: "g", Now: func() time.Time { return clock }}, Mod: h.Mod,
		Channel: "📣・жалобы", Now: func() time.Time { return clock }}
	h.Mod.Cases = h.Reports.NextCase
	return h, g, &clock
}

func report(user, reason string) interaction {
	in := cmd("report", nil, map[string]any{"user": user, "reason": reason})
	in.Member.User.Username = "halsmen"
	in.Data.Resolved.Users = map[string]resolvedUser{"victim": {ID: "victim", Username: "porojok"}, "robot": {ID: "robot", Username: "bot", Bot: true}}
	return in
}

func press(act string, n string, roles ...string) interaction {
	in := interaction{GuildID: "g", ChannelID: "cRep"}
	in.Message.ID = "m77"
	in.Member.User.ID = "staffer"
	in.Member.Roles = roles
	in.Data.CustomID = "rep:" + act + ":" + n
	return in
}

func TestReportPostsEmbedWithButtonsAndPingsModerators(t *testing.T) {
	h, g, _ := reportRig(t)
	out := h.Reports.run(context.Background(), report("victim", "Он бустер в доте"))
	if !strings.Contains(out, "#1") {
		t.Fatalf("%q", out)
	}
	mustContain(t, g.posted("cRep"), "Жалоба #1 · 🟡 Новая", "halsmen", "porojok", "Он бустер в доте", "<@&rM>", `"roles":["rM"]`,
		"rep:take:1", "rep:warn:1", "rep:done:1", "rep:reject:1", "Взять в работу", "Предупредить", "Решено", "Отклонить")
	mustContain(t, g.posted("cLog"), "Новая жалоба #1", "Он бустер в доте")
}

func TestReportRefusals(t *testing.T) {
	h, g, clock := reportRig(t)
	ctx := context.Background()
	if out := h.Reports.run(ctx, report("invoker", "я")); !strings.Contains(out, "на себя") {
		t.Errorf("self: %q", out)
	}
	if out := h.Reports.run(ctx, report("robot", "бот")); !strings.Contains(out, "ботов") {
		t.Errorf("bot: %q", out)
	}
	if len(g.posted("cRep")) != 0 {
		t.Fatal("a refused report was posted")
	}
	h.Reports.run(ctx, report("victim", "раз"))
	if out := h.Reports.run(ctx, report("victim", "два")); !strings.Contains(out, "Слишком часто") {
		t.Errorf("a second report at once: %q", out)
	}
	for i := 0; i < reportsPerHour; i++ {
		*clock = clock.Add(reportGap + time.Second)
		h.Reports.run(ctx, report("victim", "ещё"))
	}
	*clock = clock.Add(reportGap + time.Second)
	if out := h.Reports.run(ctx, report("victim", "лишняя")); !strings.Contains(out, "Слишком часто") {
		t.Errorf("hourly cap: %q", out)
	}
	*clock = clock.Add(time.Hour)
	if out := h.Reports.run(ctx, report("victim", "через час")); !strings.Contains(out, "отправлена") {
		t.Errorf("the limit should lift: %q", out)
	}
}

func TestReportFromMessageMenu(t *testing.T) {
	h, g, _ := reportRig(t)
	in := cmd(ReportMessageCommand, nil, nil)
	in.Data.TargetID = "msg9"
	in.Data.Resolved.Messages = map[string]struct {
		Content string       `json:"content"`
		Author  resolvedUser `json:"author"`
	}{"msg9": {Content: "плохой текст", Author: resolvedUser{ID: "victim", Username: "porojok"}}}
	if out := h.Reports.run(context.Background(), in); !strings.Contains(out, "отправлена") {
		t.Fatalf("%q", out)
	}
	mustContain(t, g.posted("cRep"), "плохой текст", "channels/g/cx/msg9", "porojok")
}

func TestReportButtonsNeedStaffAndFollowTheStatus(t *testing.T) {
	h, g, _ := reportRig(t)
	ctx := context.Background()
	h.Reports.run(ctx, report("victim", "флуд"))
	h.Mod.Warns, _ = OpenWarns("")

	if out := h.Reports.press(ctx, press("take", "1")); !strings.Contains(out, "персонал") {
		t.Errorf("a plain member pressed a button: %q", out)
	}
	if out := h.Reports.press(ctx, press("take", "1", "rH")); !strings.Contains(out, "за вами") {
		t.Errorf("take: %q", out)
	}
	mustPatch := func() {
		t.Helper()
		g.mu.Lock()
		defer g.mu.Unlock()
		if !containsStr(g.writes, "PATCH /channels/cRep/messages/m77") {
			t.Fatalf("the complaint message was not updated: %v", g.writes)
		}
	}
	mustPatch()
	if out := h.Reports.press(ctx, press("warn", "1", "rH")); !strings.Contains(out, "модераторы") {
		t.Errorf("a helper warned: %q", out)
	}
	if h.Mod.Warns.Count("victim", now.AddDate(0, 0, -1)) != 0 {
		t.Fatal("a refused warning was recorded")
	}

	if out := h.Reports.press(ctx, press("warn", "1", "rM")); !strings.Contains(out, "Предупреждение выдано") {
		t.Errorf("warn: %q", out)
	}
	if h.Mod.Warns.Count("victim", now.AddDate(0, 0, -1)) != 1 {
		t.Error("the warning must go through the usual warning list")
	}
	mustContain(t, g.byPath["/channels/dm1/messages"], "предупреждение")
	mustContain(t, g.posted("cLog"), "Жалоба #1")
	if !strings.Contains(strings.Join(g.byPath["/channels/cLog/messages"], ""), "Дело #") {
		t.Error("moderation entries should be numbered")
	}
	if out := h.Reports.press(ctx, press("done", "1", "rM")); !strings.Contains(out, "уже закрыта") {
		t.Errorf("a closed complaint took another press: %q", out)
	}
	if out := h.Reports.press(ctx, press("done", "42", "rM")); !strings.Contains(out, "нет") {
		t.Errorf("unknown complaint: %q", out)
	}
}

func TestReportDoneRejectAndStaffTargets(t *testing.T) {
	h, g, clock := reportRig(t)
	ctx := context.Background()
	h.Reports.run(ctx, report("victim", "а"))
	if out := h.Reports.press(ctx, press("reject", "1", "rH")); !strings.Contains(out, "отклонена") {
		t.Errorf("%q", out)
	}
	*clock = clock.Add(time.Minute)
	h.Reports.run(ctx, report("victim", "б"))
	if out := h.Reports.press(ctx, press("done", "2", "rM")); !strings.Contains(out, "решённая") {
		t.Errorf("%q", out)
	}
	*clock = clock.Add(time.Minute)
	h.Reports.run(ctx, report("mod2", "на модератора"))
	h.Mod.Warns, _ = OpenWarns("")
	if out := h.Reports.press(ctx, press("warn", "3", "rM")); !strings.Contains(out, "сотрудник") || h.Mod.Warns.Count("mod2", now.AddDate(0, 0, -1)) != 0 {
		t.Errorf("a moderator warned an equal: %q", out)
	}
	if !strings.Contains(strings.Join(g.posted("cLog"), ""), "Жалоба #2") {
		t.Error("the rejected complaint was not logged")
	}
}

func TestReportsSurviveARestart(t *testing.T) {
	h, _, _ := reportRig(t)
	h.Reports.Path = filepath.Join(t.TempDir(), "reports.json")
	h.Reports.run(context.Background(), report("victim", "до перезапуска"))
	again := &Reports{Path: h.Reports.Path}
	again.load()
	if r := again.st.Items[1]; r == nil || r.Reason != "до перезапуска" || again.st.Next != 1 {
		t.Errorf("%+v", again.st)
	}
}

func TestReportButtonsAreRouted(t *testing.T) {
	h, _, _ := reportRig(t)
	names := map[string]bool{}
	for _, c := range Commands() {
		names[c["name"].(string)] = true
		if c["name"] == reportCommand && c["default_member_permissions"] != nil {
			t.Error("everybody may report")
		}
	}
	if !names[reportCommand] || !names[ReportMessageCommand] || !names["warn"] {
		t.Errorf("commands: %v", names)
	}
	if !h.Reports.handles(reportCommand) || h.Reports.handles("warn") {
		t.Error("routing")
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
