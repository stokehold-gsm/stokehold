package discordbot

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

// appealRig is a guild where "victim" already got case #1, a warning given by "mod1".
func appealRig(t *testing.T) (*Handler, *guild, *time.Time) {
	h, g, clock := reportRig(t)
	h.Appeals = &Appeals{API: h.API, Sink: h.Reports.Sink, Mod: h.Mod, Channel: discordsetup.CuratorsChannel, Now: func() time.Time { return *clock }}
	h.Mod.Record = h.Appeals.Record
	h.Mod.Now = func() time.Time { return *clock }
	h.Mod.Warns, _ = OpenWarns("")
	warn := cmd("warn", []string{"rM"}, map[string]any{"user": "victim", "reason": "п.4 реклама"})
	warn.Member.User.ID = "mod1"
	if out := h.Mod.run(context.Background(), warn); !strings.Contains(out, "записано") {
		t.Fatalf("setup: %q", out)
	}
	return h, g, clock
}

func appeal(user string, n int, text string) interaction {
	in := cmd(appealCommand, nil, map[string]any{"case": n, "text": text})
	in.Member.User.ID, in.Member.User.Username = user, "porojok"
	return in
}

func decide(act string, n int, by string, roles ...string) interaction {
	in := interaction{GuildID: "g", ChannelID: "cCur"}
	in.Message.ID = "m88"
	in.Member.User.ID, in.Member.Roles = by, roles
	in.Data.CustomID = "apl:" + act + ":" + strconvI(n)
	return in
}

func strconvI(n int) string { return strconv.Itoa(n) }

func TestAppealNeedsYourOwnRecentCase(t *testing.T) {
	h, g, clock := appealRig(t)
	ctx := context.Background()
	if out := h.Appeals.run(ctx, appeal("stranger", 1, "это не моё")); !strings.Contains(out, "такого номера нет") {
		t.Errorf("somebody else's case: %q", out)
	}
	if out := h.Appeals.run(ctx, appeal("victim", 99, "нет такого")); !strings.Contains(out, "такого номера нет") {
		t.Errorf("unknown case: %q", out)
	}
	if out := h.Appeals.run(ctx, appeal("victim", 1, "   ")); !strings.Contains(out, "доводы") {
		t.Errorf("empty text: %q", out)
	}
	if len(g.posted("cCur")) != 0 {
		t.Fatal("a refused appeal was posted")
	}

	*clock = clock.Add(14*24*time.Hour + time.Minute)
	if out := h.Appeals.run(ctx, appeal("victim", 1, "поздно")); !strings.Contains(out, "истёк") {
		t.Errorf("after 14 days: %q", out)
	}
}

func TestBanIsAppealedInDirectMessages(t *testing.T) {
	h, _, _ := appealRig(t)
	h.Appeals.Record(Case{N: 7, Kind: kindBan, Target: "victim", By: "cur1", Reason: "скам", At: now})
	if out := h.Appeals.run(context.Background(), appeal("victim", 7, "ошибка")); !strings.Contains(out, "личных сообщениях") {
		t.Errorf("%q", out)
	}
}

func TestOneAppealPerCaseAndItGoesToCurators(t *testing.T) {
	h, g, _ := appealRig(t)
	ctx := context.Background()
	out := h.Appeals.run(ctx, appeal("victim", 1, "я не рекламировал"))
	if !strings.Contains(out, "передана кураторам") || !strings.Contains(out, "72") {
		t.Fatalf("%q", out)
	}
	mustContain(t, g.posted("cCur"), "Апелляция по делу #1 · 🟡 Новая", "я не рекламировал", "п.4 реклама", "<@mod1>", "<@&rC>",
		"apl:accept:1", "apl:reject:1", "Принять", "Отклонить", "72 ч")
	if out := h.Appeals.run(ctx, appeal("victim", 1, "ещё раз")); !strings.Contains(out, "уже подана") {
		t.Errorf("a second appeal on the same case: %q", out)
	}
}

func TestCasesAreRemembered(t *testing.T) {
	h, _, _ := appealRig(t)
	c := h.Appeals.st.Cases[1]
	if c == nil || c.Kind != kindWarn || c.Target != "victim" || c.By != "mod1" {
		t.Fatalf("%+v", c)
	}
	h.Mod.run(context.Background(), cmd("timeout", []string{"rM"}, map[string]any{"user": "victim", "reason": "флуд", "minutes": 60}))
	h.Mod.run(context.Background(), cmd("kick", []string{"rM"}, map[string]any{"user": "victim", "reason": "повтор"}))
	if h.Appeals.st.Cases[2] == nil || h.Appeals.st.Cases[2].Minutes != 60 || h.Appeals.st.Cases[3] == nil || h.Appeals.st.Cases[3].Kind != kindKick {
		t.Errorf("%+v %+v", h.Appeals.st.Cases[2], h.Appeals.st.Cases[3])
	}
}

func TestPunishedMemberIsToldTheCaseNumber(t *testing.T) {
	_, g, _ := appealRig(t)
	mustContain(t, g.posted("dm1"), "дело #1", "/appeal")
}

func TestIssuerCannotDecideTheirOwnCase(t *testing.T) {
	h, _, _ := appealRig(t)
	ctx := context.Background()
	h.Appeals.run(ctx, appeal("victim", 1, "прошу снять"))
	if out := h.Appeals.press(ctx, decide("accept", 1, "mod1", "rC")); !strings.Contains(out, "вынесли вы") {
		t.Errorf("the issuer decided: %q", out)
	}
	if h.Mod.Warns.Count("victim", now.AddDate(0, 0, -1)) != 1 {
		t.Fatal("the warning was cancelled by the person who gave it")
	}
	if out := h.Appeals.press(ctx, decide("accept", 1, "other", "rM")); !strings.Contains(out, "кураторы") {
		t.Errorf("a plain moderator decided: %q", out)
	}
	if out := h.Appeals.press(ctx, decide("accept", 1, "stranger")); !strings.Contains(out, "кураторы") {
		t.Errorf("a plain member decided: %q", out)
	}
}

func TestAcceptedAppealCancelsTheWarningAndTellsTheMember(t *testing.T) {
	h, g, _ := appealRig(t)
	ctx := context.Background()
	h.Appeals.run(ctx, appeal("victim", 1, "прошу снять"))
	if out := h.Appeals.press(ctx, decide("accept", 1, "cur2", "rC")); !strings.Contains(out, "принята") {
		t.Fatalf("%q", out)
	}
	if h.Mod.Warns.Count("victim", now.AddDate(0, 0, -1)) != 0 {
		t.Error("the warning is still there")
	}
	if !h.Appeals.st.Cases[1].Cancelled || h.Appeals.st.Appeals[1].Status != stAccepted {
		t.Error("the case was not marked as cancelled")
	}
	mustContain(t, g.posted("dm1"), "Апелляция по делу #1 принята", "наказание отменено")
	mustContain(t, g.posted("cLog"), "Апелляция принята", "по делу #1")
	if !g.wrote("PATCH /channels/cCur/messages/m88") {
		t.Error("the card was not updated")
	}
	if out := h.Appeals.press(ctx, decide("accept", 1, "cur3", "rC")); !strings.Contains(out, "окончательное") {
		t.Errorf("a second decision: %q", out)
	}
	if out := h.Appeals.run(ctx, appeal("victim", 1, "ещё")); !strings.Contains(out, "уже") {
		t.Errorf("an appeal on a cancelled case: %q", out)
	}
}

func TestAcceptedTimeoutAppealLiftsTheTimeoutAndKickIsOnlyRecorded(t *testing.T) {
	h, g, _ := appealRig(t)
	ctx := context.Background()
	h.Mod.run(ctx, cmd("timeout", []string{"rM"}, map[string]any{"user": "victim", "reason": "флуд", "minutes": 60}))
	h.Appeals.run(ctx, appeal("victim", 2, "я не флудил"))
	h.Appeals.press(ctx, decide("accept", 2, "cur2", "rC"))
	if !g.wrote("PATCH /guilds/g/members/victim") {
		t.Error("the timeout was not lifted")
	}
	h.Appeals.Record(Case{N: 9, Kind: kindKick, Target: "victim", By: "mod1", Reason: "повтор", At: now})
	h.Appeals.run(ctx, appeal("victim", 9, "вернулся, прошу снять"))
	if out := h.Appeals.press(ctx, decide("accept", 9, "cur2", "rC")); !strings.Contains(out, "принята") {
		t.Fatalf("%q", out)
	}
	if !h.Appeals.st.Cases[9].Cancelled {
		t.Error("a kick must be recorded as cancelled")
	}
	mustContain(t, g.posted("dm1"), "Исключение отменить нельзя")
}

func TestRejectedAppealNeedsAReasonAndIsFinal(t *testing.T) {
	h, g, _ := appealRig(t)
	ctx := context.Background()
	h.Appeals.run(ctx, appeal("victim", 1, "прошу снять"))
	if out := h.Appeals.press(ctx, decide("rejectm", 1, "cur2", "rC")); !strings.Contains(out, "причина") {
		t.Errorf("a rejection without a reason: %q", out)
	}
	if out := h.Appeals.press(ctx, withReason(decide("rejectm", 1, "cur2", "rC"), "нарушение подтверждено логами")); !strings.Contains(out, "отклонена") {
		t.Fatalf("%q", out)
	}
	mustContain(t, g.posted("dm1"), "Апелляция по делу #1 отклонена", "нарушение подтверждено логами", "окончательное")
	if h.Mod.Warns.Count("victim", now.AddDate(0, 0, -1)) != 1 {
		t.Error("a rejected appeal must keep the warning")
	}
	if out := h.Appeals.run(ctx, appeal("victim", 1, "по новой")); !strings.Contains(out, "уже подана") {
		t.Errorf("a repeat appeal: %q", out)
	}
}

func TestAppealsSurviveARestart(t *testing.T) {
	h, _, _ := appealRig(t)
	h.Appeals.Path = t.TempDir() + "/appeals.json"
	h.Appeals.Record(Case{N: 4, Kind: kindWarn, Target: "victim", By: "mod1", At: now})
	again := &Appeals{Path: h.Appeals.Path}
	again.load()
	if again.st.Cases[4] == nil {
		t.Errorf("%+v", again.st)
	}
}

func TestRejectButtonsOpenAReasonWindow(t *testing.T) {
	rg := newRig(t, roles)
	rg.h.Appeals = &Appeals{Mod: &Mod{API: rg.h.API}}
	rg.h.Reports = &Reports{}
	for before, want := range map[string]string{"apl:reject:5": "apl:rejectm:5", "rep:reject:3": "rep:rejectm:3"} {
		rec := rg.post(`{"type":3,"guild_id":"g","member":{"user":{"id":"u1"},"roles":[]},"data":{"custom_id":"`+before+`"}}`, now, true)
		body := rec.Body.String()
		if !strings.Contains(body, `"type":9`) || !strings.Contains(body, want) || !strings.Contains(body, `"required":true`) {
			t.Errorf("%s: %s", before, body)
		}
	}
	// the submitted window is routed to the same handlers and answered at once
	rec := rg.post(`{"type":5,"guild_id":"g","token":"t","member":{"user":{"id":"u1"},"roles":[]},"data":{"custom_id":"apl:rejectm:5","components":[{"components":[{"custom_id":"reason","value":"x"}]}]}}`, now, true)
	if !strings.Contains(rec.Body.String(), `"type":5`) {
		t.Errorf("a modal submit must be deferred: %s", rec.Body)
	}
	rg.h.wg.Wait()
}

func TestAppealCommandIsRegisteredForEveryone(t *testing.T) {
	for _, c := range Commands() {
		if c["name"] == appealCommand {
			if c["default_member_permissions"] != nil {
				t.Error("everybody may appeal")
			}
			return
		}
	}
	t.Error("/appeal is not in the command set")
}
