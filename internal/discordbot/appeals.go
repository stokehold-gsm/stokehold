package discordbot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

// Appeals: a member contests a warning, timeout or kick with /appeal. The card
// goes to the curators' channel; a Curator or Founder who did not issue the
// punishment accepts it (the punishment is cancelled) or rejects it with a
// reason. The decision is final. Bans are appealed in direct messages.

const (
	appealCommand = "appeal"
	appealWindow  = 14 * 24 * time.Hour
	appealTarget  = 72 * time.Hour

	kindWarn    = "warn"
	kindTimeout = "timeout"
	kindKick    = "kick"
	kindBan     = "ban"

	stAccepted = "accepted"
)

func AppealCommands() []map[string]any {
	return []map[string]any{
		{"name": appealCommand, "description": "Обжаловать предупреждение, таймаут или исключение", "dm_permission": false, "options": []map[string]any{
			{"type": 4, "name": "case", "description": "Номер дела из личного сообщения или журнала", "required": true, "min_value": 1},
			{"type": 3, "name": "text", "description": "Почему решение неверно", "required": true, "max_length": 1000},
		}},
	}
}

// Case is one numbered punishment from the moderation log.
type Case struct {
	N         int       `json:"n"`
	Kind      string    `json:"kind"`
	Target    string    `json:"target"`
	By        string    `json:"by"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"` // for a warning also the time key of its entry in the warning list
	Minutes   int       `json:"minutes,omitempty"`
	Cancelled bool      `json:"cancelled,omitempty"`
	Appealed  bool      `json:"appealed,omitempty"`
}

type Appeal struct {
	N        int       `json:"n"` // the case number
	User     string    `json:"user"`
	UserName string    `json:"user_name"`
	Text     string    `json:"text"`
	At       time.Time `json:"at"`
	Status   string    `json:"status"`
	By       string    `json:"by,omitempty"`
	Note     string    `json:"note,omitempty"`
}

type appealState struct {
	Cases   map[int]*Case   `json:"cases"`
	Appeals map[int]*Appeal `json:"appeals"`
}

// Appeals keeps cases and appeals in memory and in a small JSON file.
type Appeals struct {
	API     *discordsetup.Client
	Sink    *Sink
	Mod     *Mod
	Channel string // channel name for appeal cards
	Path    string // "" keeps everything in memory
	Now     func() time.Time

	mu     sync.Mutex
	st     appealState
	loaded bool
}

func (a *Appeals) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Appeals) load() {
	if a.loaded {
		return
	}
	a.loaded = true
	a.st = appealState{Cases: map[int]*Case{}, Appeals: map[int]*Appeal{}}
	if b, err := os.ReadFile(a.Path); err == nil && len(b) > 0 {
		json.Unmarshal(b, &a.st)
		if a.st.Cases == nil {
			a.st.Cases = map[int]*Case{}
		}
		if a.st.Appeals == nil {
			a.st.Appeals = map[int]*Appeal{}
		}
	}
}

func (a *Appeals) save() {
	if a.Path == "" {
		return
	}
	b, _ := json.Marshal(a.st)
	_ = os.WriteFile(a.Path, b, 0o600)
}

// Record is Mod.Record: every numbered punishment is remembered.
func (a *Appeals) Record(c Case) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.load()
	a.st.Cases[c.N] = &c
	a.save()
}

func (a *Appeals) handles(name string) bool { return name == appealCommand }

// run files an appeal for the member who typed the command.
func (a *Appeals) run(ctx context.Context, in interaction) string {
	user := in.Member.User.ID
	n, _ := strconv.Atoi(opt(in.Data.Options, "case"))
	text := strings.TrimSpace(opt(in.Data.Options, "text"))
	if n <= 0 || text == "" {
		return "Укажите номер дела и доводы."
	}
	a.mu.Lock()
	a.load()
	c := a.st.Cases[n]
	switch {
	case c == nil || c.Target != user:
		a.mu.Unlock()
		return "Среди ваших дел такого номера нет. Номер указан в личном сообщении от бота."
	case c.Kind == kindBan:
		a.mu.Unlock()
		return "Блокировку обжалуют в личных сообщениях администрации: заблокированный участник не может использовать команды."
	case c.Appealed:
		a.mu.Unlock()
		return "По этому делу апелляция уже подана. Решение по ней окончательное, повторно не подают."
	case c.Cancelled:
		a.mu.Unlock()
		return "Это наказание уже отменено."
	case a.now().Sub(c.At) > appealWindow:
		a.mu.Unlock()
		return "Срок подачи апелляции, 14 дней, истёк."
	}
	ap := &Appeal{N: n, User: user, UserName: in.Member.User.Username, Text: text, At: a.now(), Status: stNew}
	c.Appealed = true
	a.st.Appeals[n] = ap
	a.save()
	snap, cs := *ap, *c
	a.mu.Unlock()

	body := map[string]any{"embeds": []map[string]any{a.embed(snap, cs)}, "components": a.buttons(snap)}
	var roles []struct{ ID, Name string }
	a.API.Do(ctx, "GET", "/guilds/"+in.GuildID+"/roles", nil, &roles)
	for _, ro := range roles {
		if ro.Name == discordsetup.RoleCurator {
			body["content"] = "<@&" + ro.ID + ">"
			body["allowed_mentions"] = map[string]any{"roles": []string{ro.ID}}
		}
	}
	id := a.Sink.channelID(ctx, a.Channel)
	if id == "" || a.API.Do(ctx, "POST", "/channels/"+id+"/messages", body, nil) != nil {
		a.mu.Lock()
		c.Appealed = false
		delete(a.st.Appeals, n)
		a.save()
		a.mu.Unlock()
		return "Не удалось передать апелляцию кураторам, попробуйте позже или напишите им лично."
	}
	a.Mod.logEmbed(ctx, in.GuildID, embed(fmt.Sprintf("📨 Апелляция по делу #%d", n), colBlue, a.now(),
		field("Кто", who(user, ap.UserName), true), field("Вид", kindLabel[cs.Kind], true), field("Доводы", text, false)))
	return fmt.Sprintf("Апелляция по делу #%d передана кураторам. Ответ придёт в личные сообщения в течение 72 часов. Решение окончательное.", n)
}

var kindLabel = map[string]string{kindWarn: "Предупреждение", kindTimeout: "Таймаут", kindKick: "Исключение", kindBan: "Бан"}

func (a *Appeals) embed(ap Appeal, c Case) map[string]any {
	title, color := fmt.Sprintf("Апелляция по делу #%d · 🟡 Новая", ap.N), colYellow
	switch ap.Status {
	case stAccepted:
		title, color = fmt.Sprintf("Апелляция по делу #%d · 🟢 Принята", ap.N), colGreen
	case stRejected:
		title, color = fmt.Sprintf("Апелляция по делу #%d · ⚪ Отклонена", ap.N), colGrey
	}
	f := []map[string]any{
		field("Апеллирует", who(ap.User, ap.UserName), true),
		field("Наказание", kindLabel[c.Kind]+" от "+c.At.UTC().Format("2006-01-02"), true),
		field("Выдал", "<@"+c.By+">", true),
		field("Причина наказания", c.Reason, false),
		field("Доводы участника", ap.Text, false),
	}
	if ap.By != "" {
		f = append(f, field("Решил", "<@"+ap.By+">", true))
	}
	if ap.Note != "" {
		f = append(f, field("Пояснение", ap.Note, false))
	}
	e := embed(title, color, ap.At, f...)
	if ap.Status == stNew {
		e["footer"] = map[string]any{"text": "Решить в течение 72 ч. Не тому, кто выносил решение. Решение окончательное."}
	}
	return e
}

func (a *Appeals) buttons(ap Appeal) []map[string]any {
	if ap.Status != stNew {
		return []map[string]any{}
	}
	n := strconv.Itoa(ap.N)
	return []map[string]any{{"type": 1, "components": []map[string]any{
		{"type": 2, "style": 3, "label": "Принять", "custom_id": "apl:accept:" + n},
		{"type": 2, "style": 4, "label": "Отклонить", "custom_id": "apl:reject:" + n},
	}}}
}

// press handles «Принять» and the reason window of «Отклонить».
func (a *Appeals) press(ctx context.Context, in interaction) string {
	parts := strings.Split(in.Data.CustomID, ":")
	if len(parts) != 3 {
		return "Эта кнопка не поддерживается."
	}
	n, _ := strconv.Atoi(parts[2])
	act := parts[1]
	if act != "accept" && act != "rejectm" {
		return "Эта кнопка не поддерживается."
	}
	if a.Mod.rank(ctx, in.GuildID, in.Member.Roles) < rankCurator {
		return "Апелляции разбирают кураторы."
	}
	by := in.Member.User.ID
	note := strings.TrimSpace(modalValue(in, "reason"))
	if act == "rejectm" && note == "" {
		return "Нужна причина отказа: участник её увидит."
	}

	a.mu.Lock()
	a.load()
	ap, c := a.st.Appeals[n], a.st.Cases[n]
	switch {
	case ap == nil || c == nil:
		a.mu.Unlock()
		return "Такой апелляции нет."
	case ap.Status != stNew:
		a.mu.Unlock()
		return "Апелляция уже решена. Решение окончательное."
	case by == c.By:
		a.mu.Unlock()
		return "Это решение вынесли вы. Апелляцию разбирает другой куратор."
	}
	snap := *c
	a.mu.Unlock()

	// Cancel first: when it fails the appeal stays open for another try.
	detail := ""
	if act == "accept" {
		switch snap.Kind {
		case kindWarn:
			if !a.Mod.Warns.Remove(snap.Target, snap.By, snap.At) {
				detail = "предупреждения в списке уже не было"
			}
		case kindTimeout:
			if snap.At.Add(time.Duration(snap.Minutes) * time.Minute).After(a.now()) {
				if a.API.Do(ctx, "PATCH", "/guilds/"+in.GuildID+"/members/"+snap.Target, map[string]any{"communication_disabled_until": nil}, nil) != nil {
					return "Не удалось снять таймаут (участник мог выйти). Апелляция осталась открытой."
				}
			} else {
				detail = "таймаут уже истёк"
			}
		case kindKick:
			detail = "исключение отменить нельзя, оно записано как отменённое"
		}
	}

	a.mu.Lock()
	ap.By = by
	if act == "accept" {
		ap.Status, c.Cancelled = stAccepted, true
	} else {
		ap.Status, ap.Note = stRejected, note
	}
	a.save()
	asnap, csnap := *ap, *c
	a.mu.Unlock()

	reply, dm, title := "", "", "↩️ Апелляция принята"
	if act == "accept" {
		reply = fmt.Sprintf("Апелляция по делу #%d принята, наказание отменено.", n)
		dm = fmt.Sprintf("Апелляция по делу #%d принята: наказание отменено.", n)
		if snap.Kind == kindKick {
			dm += " Исключение отменить нельзя, но оно записано как отменённое, вы можете вернуться на сервер по приглашению."
		}
	} else {
		reply = fmt.Sprintf("Апелляция по делу #%d отклонена.", n)
		dm = fmt.Sprintf("Апелляция по делу #%d отклонена. Причина: %s\nРешение окончательное, повторно по этому делу не подают.", n, note)
		title = "⛔ Апелляция отклонена"
	}
	if err := a.API.Do(ctx, "PATCH", "/channels/"+in.ChannelID+"/messages/"+in.Message.ID, map[string]any{
		"embeds": []map[string]any{a.embed(asnap, csnap)}, "components": a.buttons(asnap),
	}, nil); err != nil {
		reply += " Карточку обновить не удалось."
	}
	extra := fmt.Sprintf("апелляция по делу #%d", n)
	if detail != "" {
		extra += ": " + detail
	}
	a.Mod.log(ctx, in.GuildID, title, snap.Target, by, note, extra)
	dmUser(ctx, a.API, snap.Target, dm)
	return reply
}

// modalValue reads one text input of a submitted modal.
func modalValue(in interaction, id string) string {
	for _, row := range in.Data.Components {
		for _, c := range row.Components {
			if c.CustomID == id {
				return c.Value
			}
		}
	}
	return ""
}

// rejectModal is the window «Отклонить» opens: a reason is required.
func rejectModal(customID string) map[string]any {
	return map[string]any{"type": respModal, "data": map[string]any{
		"custom_id": customID, "title": "Причина отказа",
		"components": []map[string]any{{"type": 1, "components": []map[string]any{
			{"type": 4, "custom_id": "reason", "label": "Причина (её увидит участник)", "style": 2, "required": true, "min_length": 3, "max_length": 500},
		}}},
	}}
}
