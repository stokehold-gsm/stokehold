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

// Reports: any member can complain with /report or the message menu
// «Пожаловаться на сообщение». The complaint lands in the reports channel as one
// embed with buttons; staff take it, warn, resolve or reject it, and the embed
// follows the status. Every step is also written to the moderation log.

const (
	reportCommand = "report"
	// ReportMessageCommand is the name of the message context-menu command.
	ReportMessageCommand = "Пожаловаться на сообщение"

	reportsPerHour = 5
	reportGap      = 30 * time.Second
)

// ReportCommands are added to the registered command set. They are open to
// everybody, unlike the moderation commands.
func ReportCommands() []map[string]any {
	return []map[string]any{
		{"name": reportCommand, "description": "Пожаловаться на участника", "dm_permission": false, "options": []map[string]any{
			{"type": 6, "name": "user", "description": "На кого жалоба", "required": true},
			{"type": 3, "name": "reason", "description": "Что случилось и где", "required": true, "max_length": 400},
		}},
		{"name": ReportMessageCommand, "type": 3, "dm_permission": false},
	}
}

type Report struct {
	N            int       `json:"n"`
	Reporter     string    `json:"reporter"`
	ReporterName string    `json:"reporter_name"`
	Target       string    `json:"target"`
	TargetName   string    `json:"target_name"`
	Reason       string    `json:"reason"`
	Link         string    `json:"link,omitempty"`
	Excerpt      string    `json:"excerpt,omitempty"`
	Status       string    `json:"status"`
	By           string    `json:"by,omitempty"`
	At           time.Time `json:"at"`
	Staff        bool      `json:"staff,omitempty"` // the target is staff: Curator and above handle it
	Note         string    `json:"note,omitempty"`  // why it was rejected
	MsgID        string    `json:"msg_id,omitempty"`
	ChanID       string    `json:"chan_id,omitempty"`
	Late         int       `json:"late,omitempty"` // 1: not taken within 24 h was flagged, 2: not closed within 72 h
}

const (
	reportTake  = 24 * time.Hour
	reportClose = 72 * time.Hour
)

const (
	stNew      = "new"
	stTaken    = "taken"
	stWarned   = "warned"
	stDone     = "done"
	stRejected = "rejected"
)

var statusLook = map[string]struct {
	Label string
	Color int
}{
	stNew:      {"🟡 Новая", colYellow},
	stTaken:    {"🔵 В работе", colBlue},
	stWarned:   {"🟠 Предупреждение выдано", colOrange},
	stDone:     {"🟢 Решена", colGreen},
	stRejected: {"⚪ Отклонена", colGrey},
}

func (r Report) open() bool { return r.Status == stNew || r.Status == stTaken }

type reportState struct {
	Next  int             `json:"next"`
	Cases int             `json:"cases"`
	Items map[int]*Report `json:"items"`
}

// Reports keeps complaints in memory and in a small JSON file, like Warns.
type Reports struct {
	API     *discordsetup.Client
	Sink    *Sink
	Mod     *Mod   // warnings and the moderation log
	Channel string // channel name for new complaints
	// StaffChannel takes complaints about staff, which only Curator and Founder see; empty uses Channel.
	StaffChannel string
	Path         string // "" keeps everything in memory
	Now          func() time.Time

	mu     sync.Mutex
	st     reportState
	loaded bool
	recent map[string][]time.Time
}

func (r *Reports) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// load reads the file once; a missing file is a fresh start.
func (r *Reports) load() {
	if r.loaded {
		return
	}
	r.loaded = true
	r.st = reportState{Items: map[int]*Report{}}
	r.recent = map[string][]time.Time{}
	if b, err := os.ReadFile(r.Path); err == nil && len(b) > 0 {
		json.Unmarshal(b, &r.st)
		if r.st.Items == nil {
			r.st.Items = map[int]*Report{}
		}
	}
}

func (r *Reports) save() {
	if r.Path == "" {
		return
	}
	b, _ := json.Marshal(r.st)
	_ = os.WriteFile(r.Path, b, 0o600)
}

// NextCase numbers the moderation log entries («Дело #N»).
func (r *Reports) NextCase() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	r.st.Cases++
	r.save()
	return r.st.Cases
}

func (r *Reports) handles(name string) bool {
	return name == reportCommand || name == ReportMessageCommand
}

// throttle allows a few complaints per hour from one member and keeps a pause
// between two of them, so the channel cannot be flooded.
func (r *Reports) throttle(user string) bool {
	now := r.now()
	var keep []time.Time
	for _, t := range r.recent[user] {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	if len(keep) >= reportsPerHour || (len(keep) > 0 && now.Sub(keep[len(keep)-1]) < reportGap) {
		r.recent[user] = keep
		return false
	}
	r.recent[user] = append(keep, now)
	return true
}

func (r *Reports) run(ctx context.Context, in interaction) string {
	rep := Report{Reporter: in.Member.User.ID, ReporterName: in.Member.User.Username, At: r.now(), Status: stNew}
	var bot bool
	if in.Data.Name == ReportMessageCommand {
		m := in.Data.Resolved.Messages[in.Data.TargetID]
		rep.Target, rep.TargetName, bot = m.Author.ID, m.Author.Username, m.Author.Bot
		rep.Reason = "Жалоба на сообщение"
		rep.Excerpt = clip(m.Content, 500)
		rep.Link = "https://discord.com/channels/" + in.GuildID + "/" + in.ChannelID + "/" + in.Data.TargetID
	} else {
		rep.Target, rep.Reason = opt(in.Data.Options, "user"), strings.TrimSpace(opt(in.Data.Options, "reason"))
		u := in.Data.Resolved.Users[rep.Target]
		rep.TargetName, bot = u.Username, u.Bot
	}
	switch {
	case rep.Target == "" || rep.Reason == "":
		return "Не удалось понять, на кого и за что жалоба."
	case rep.Target == rep.Reporter:
		return "Пожаловаться на себя нельзя."
	case bot:
		return "На ботов не жалуются: напишите модераторам об их хозяине."
	}
	r.mu.Lock()
	r.load()
	if !r.throttle(rep.Reporter) {
		r.mu.Unlock()
		return fmt.Sprintf("Слишком часто: не больше %d жалоб в час и не чаще раза в полминуты.", reportsPerHour)
	}
	r.mu.Unlock()
	rep.Staff = r.Mod.isStaff(ctx, in.GuildID, rep.Target)
	r.mu.Lock()
	r.st.Next++
	rep.N = r.st.Next
	r.st.Items[rep.N] = &rep
	r.save()
	r.mu.Unlock()

	// Complaints about staff go to the curators' channel and ping only them.
	channel, pingRole, to := r.Channel, discordsetup.RoleMod, "модераторам"
	if rep.Staff && r.StaffChannel != "" {
		channel, pingRole, to = r.StaffChannel, discordsetup.RoleCurator, "кураторам"
	}
	var roles []struct{ ID, Name string }
	r.API.Do(ctx, "GET", "/guilds/"+in.GuildID+"/roles", nil, &roles)
	body := map[string]any{"embeds": []map[string]any{r.embed(rep)}, "components": buttons(rep)}
	for _, ro := range roles {
		if ro.Name == pingRole {
			body["content"] = "<@&" + ro.ID + ">"
			body["allowed_mentions"] = map[string]any{"roles": []string{ro.ID}}
		}
	}
	chans := r.Sink.channelID(ctx, channel)
	if chans == "" {
		return "Канал жалоб ещё не настроен, напишите модераторам напрямую."
	}
	var posted struct {
		ID string `json:"id"`
	}
	if err := r.API.Do(ctx, "POST", "/channels/"+chans+"/messages", body, &posted); err != nil {
		return "Не удалось отправить жалобу, попробуйте позже."
	}
	r.mu.Lock()
	rep.MsgID, rep.ChanID = posted.ID, chans
	r.save()
	r.mu.Unlock()
	r.Mod.logEmbed(ctx, in.GuildID, embed(fmt.Sprintf("📣 Новая жалоба #%d", rep.N), colYellow, r.now(),
		field("От", who(rep.Reporter, rep.ReporterName), true), field("На", who(rep.Target, rep.TargetName), true), field("Причина", rep.Reason, false)))
	return fmt.Sprintf("Жалоба #%d отправлена %s. Спасибо.", rep.N, to)
}

// Sweep flags open complaints that outlived their targets: red card, a ping,
// once per level. The bot calls it every few minutes.
func (r *Reports) Sweep(ctx context.Context, guild string) {
	r.mu.Lock()
	r.load()
	var late []Report
	for _, rep := range r.st.Items {
		age := r.now().Sub(rep.At)
		switch {
		case rep.open() && age > reportClose && rep.Late < 2:
			rep.Late = 2
		case rep.Status == stNew && age > reportTake && rep.Late < 1:
			rep.Late = 1
		default:
			continue
		}
		late = append(late, *rep)
	}
	if len(late) > 0 {
		r.save()
	}
	r.mu.Unlock()
	for _, rep := range late {
		if rep.MsgID == "" {
			continue
		}
		r.API.Do(ctx, "PATCH", "/channels/"+rep.ChanID+"/messages/"+rep.MsgID, map[string]any{"embeds": []map[string]any{r.embed(rep)}, "components": buttons(rep)}, nil)
		what := "не взята в работу за 24 часа"
		if rep.Late == 2 {
			what = "не закрыта за 72 часа"
		}
		role := discordsetup.RoleMod
		if rep.Staff {
			role = discordsetup.RoleCurator
		}
		var roles []struct{ ID, Name string }
		r.API.Do(ctx, "GET", "/guilds/"+guild+"/roles", nil, &roles)
		body := map[string]any{"content": fmt.Sprintf("⏰ Жалоба #%d %s.", rep.N, what), "allowed_mentions": map[string]any{"parse": []string{}}}
		for _, ro := range roles {
			if ro.Name == role {
				body["content"] = "<@&" + ro.ID + "> " + body["content"].(string)
				body["allowed_mentions"] = map[string]any{"roles": []string{ro.ID}}
			}
		}
		r.API.Do(ctx, "POST", "/channels/"+rep.ChanID+"/messages", body, nil)
	}
}

func (r *Reports) embed(rep Report) map[string]any {
	look := statusLook[rep.Status]
	f := []map[string]any{
		field("Пожаловался", who(rep.Reporter, rep.ReporterName), true),
		field("На кого", who(rep.Target, rep.TargetName), true),
		field("Причина", rep.Reason, false),
	}
	if rep.Excerpt != "" {
		f = append(f, field("Сообщение", rep.Excerpt, false))
	}
	if rep.Link != "" {
		f = append(f, field("Ссылка", "[Перейти к сообщению]("+rep.Link+")", false))
	}
	if rep.By != "" {
		f = append(f, field("Обработал", "<@"+rep.By+">", true))
	}
	if rep.Note != "" {
		f = append(f, field("Причина отказа", rep.Note, false))
	}
	color := look.Color
	e := embed(fmt.Sprintf("Жалоба #%d · %s", rep.N, look.Label), color, rep.At, f...)
	if rep.open() {
		text := "Срок: взять в работу в течение 24 ч, закрыть в течение 72 ч"
		if rep.Late > 0 {
			text, e["color"] = "⏰ Просрочена. "+text, colRed
		}
		if rep.Staff {
			text += ". Жалоба на сотрудника: решают кураторы"
		}
		e["footer"] = map[string]any{"text": text}
	}
	return e
}

func buttons(rep Report) []map[string]any {
	if !rep.open() {
		return []map[string]any{}
	}
	btn := func(style int, label, act string) map[string]any {
		return map[string]any{"type": 2, "style": style, "label": label, "custom_id": "rep:" + act + ":" + strconv.Itoa(rep.N)}
	}
	row := []map[string]any{}
	if rep.Status == stNew {
		row = append(row, btn(1, "Взять в работу", "take"))
	}
	row = append(row, btn(4, "Предупредить", "warn"), btn(3, "Решено", "done"), btn(2, "Отклонить", "reject"))
	return []map[string]any{{"type": 1, "components": row}}
}

// rank is the staff rank of a member by role ids, 0 for everybody else.
func (m *Mod) rank(ctx context.Context, guild string, roleIDs []string) int {
	var roles []struct{ ID, Name string }
	if m.API.Do(ctx, "GET", "/guilds/"+guild+"/roles", nil, &roles) != nil {
		return 0
	}
	names := map[string]string{}
	for _, r := range roles {
		names[r.ID] = r.Name
	}
	rank := 0
	for _, id := range roleIDs {
		if !shopRole[names[id]] {
			rank = max(rank, roleRank[names[id]])
		}
	}
	return rank
}

// press handles the four buttons under a complaint.
func (r *Reports) press(ctx context.Context, in interaction) string {
	parts := strings.Split(in.Data.CustomID, ":")
	if len(parts) != 3 {
		return "Эта кнопка не поддерживается."
	}
	n, _ := strconv.Atoi(parts[2])
	act := parts[1]
	rank := r.Mod.rank(ctx, in.GuildID, in.Member.Roles)
	if rank < rankHelper {
		return "Жалобы разбирает персонал."
	}
	r.mu.Lock()
	r.load()
	rep := r.st.Items[n]
	r.mu.Unlock()
	if rep == nil {
		return "Такой жалобы нет."
	}
	if !rep.open() {
		return "Жалоба уже закрыта: " + statusLook[rep.Status].Label + "."
	}
	if rep.Staff && rank < rankCurator {
		return "Жалобы на сотрудников разбирают кураторы."
	}
	by := in.Member.User.ID
	reply := ""
	next := ""
	note := strings.TrimSpace(modalValue(in, "reason"))
	switch act {
	case "take":
		next, reply = stTaken, fmt.Sprintf("Жалоба #%d за вами.", n)
	case "done":
		next, reply = stDone, fmt.Sprintf("Жалоба #%d закрыта как решённая.", n)
	case "reject", "rejectm":
		if note == "" {
			return "Нужна причина отказа: её увидит автор жалобы."
		}
		next, reply = stRejected, fmt.Sprintf("Жалоба #%d отклонена.", n)
	case "warn":
		if rank < rankMod {
			return "Предупреждения выдают модераторы и выше."
		}
		var member struct {
			Roles []string `json:"roles"`
		}
		if r.API.Do(ctx, "GET", "/guilds/"+in.GuildID+"/members/"+rep.Target, nil, &member) != nil {
			return "Участника уже нет на сервере: закройте жалобу кнопкой «Решено»."
		}
		if t := r.Mod.rank(ctx, in.GuildID, member.Roles); t > 0 && t >= rank {
			return "Это сотрудник. Решения о персонале принимают кураторы."
		}
		reason := fmt.Sprintf("жалоба #%d: %s", n, rep.Reason)
		at := r.now()
		total := r.Mod.Warns.Add(rep.Target, Warning{At: at, By: by, Reason: reason})
		recent := r.Mod.Warns.Count(rep.Target, r.now().AddDate(0, 0, -30))
		r.Mod.punished(ctx, in.GuildID, r.Mod.nextCase(), "⚠️ Предупреждение", fmt.Sprintf("всего %d, за 30 дней %d", total, recent),
			Case{Kind: kindWarn, Target: rep.Target, By: by, Reason: reason, At: at})
		next, reply = stWarned, fmt.Sprintf("Предупреждение выдано (за 30 дней: %d).", recent)
	default:
		return "Эта кнопка не поддерживается."
	}
	r.mu.Lock()
	rep.Status, rep.By, rep.Note = next, by, note
	r.save()
	snapshot := *rep
	r.mu.Unlock()
	// The reporter hears the outcome, never what the punishment was.
	if next != stTaken {
		text := fmt.Sprintf("Жалоба #%d рассмотрена, меры приняты. Спасибо, что сообщили.", n)
		if next == stRejected {
			text = fmt.Sprintf("Жалоба #%d рассмотрена и отклонена. Причина: %s", n, note)
		}
		dmUser(ctx, r.API, snapshot.Reporter, text)
	}
	if err := r.API.Do(ctx, "PATCH", "/channels/"+in.ChannelID+"/messages/"+in.Message.ID, map[string]any{
		"embeds": []map[string]any{r.embed(snapshot)}, "components": buttons(snapshot),
	}, nil); err != nil {
		reply += " Сообщение с жалобой обновить не удалось."
	}
	if next != stTaken {
		r.Mod.logEmbed(ctx, in.GuildID, embed(fmt.Sprintf("📣 Жалоба #%d · %s", n, statusLook[next].Label), statusLook[next].Color, r.now(),
			field("Кто", "<@"+by+">", true), field("На кого", who(snapshot.Target, snapshot.TargetName), true), field("Причина", snapshot.Reason, false)))
	}
	return reply
}

// dmUser tells a member about a decision; closed DMs are normal and ignored.
func dmUser(ctx context.Context, api *discordsetup.Client, user, text string) {
	var ch struct{ ID string }
	if api.Do(ctx, "POST", "/users/@me/channels", map[string]any{"recipient_id": user}, &ch) != nil || ch.ID == "" {
		return
	}
	api.Do(ctx, "POST", "/channels/"+ch.ID+"/messages", map[string]any{"content": text, "allowed_mentions": map[string]any{"parse": []string{}}}, nil)
}

// isStaff reports whether the user holds any staff role, shop roles included.
func (m *Mod) isStaff(ctx context.Context, guild, user string) bool {
	var member struct {
		Roles []string `json:"roles"`
	}
	var roles []struct{ ID, Name string }
	if m.API.Do(ctx, "GET", "/guilds/"+guild+"/members/"+user, nil, &member) != nil ||
		m.API.Do(ctx, "GET", "/guilds/"+guild+"/roles", nil, &roles) != nil {
		return false
	}
	names := map[string]string{}
	for _, ro := range roles {
		names[ro.ID] = ro.Name
	}
	for _, id := range member.Roles {
		if roleRank[names[id]] > 0 {
			return true
		}
	}
	return false
}
