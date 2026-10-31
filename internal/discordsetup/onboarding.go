package discordsetup

import (
	"context"
	"fmt"
)

type Choice struct {
	Title, Desc, Emoji string
	Role               string
}

type Prompt struct {
	Title    string
	Single   bool
	Required bool
	Choices  []Choice
}

// Roles and channels with these names are what a newcomer gets from onboarding.
const (
	RoleHe      = "Он"
	RoleShe     = "Она"
	RoleNeutral = "Без обращения"
)

var onboardingPrompts = []Prompt{
	{Title: "На каком языке вам удобнее?", Single: true, Required: true, Choices: []Choice{
		{"Русский", "Каналы и поддержка на русском", "🇷🇺", "🇷🇺 Русский"},
		{"English", "English channels and support", "🇬🇧", "🇬🇧 English"},
	}},
	{Title: "Что вам присылать?", Choices: []Choice{
		{"Релизы", "Упоминание, когда выходит новая версия", "🔔", "🔔 Releases"},
		{"Новости", "Редкие новости проекта", "📣", "📣 News"},
	}},
	{Title: "Как к вам обращаться?", Single: true, Choices: []Choice{
		{"Он", "Сотрудники магазина и модераторы будут писать в мужском роде", "", RoleHe},
		{"Она", "Сотрудники магазина и модераторы будут писать в женском роде", "", RoleShe},
		{"Без обращения", "Пишем без указания рода", "", RoleNeutral},
	}},
}

// Discord asks for at least 7 channels everyone can see, 5 of them writable.
var onboardingChannels = []string{"welcome", "rules", "roles", "faq", "news", "chat", "help", "bugs", "ideas", "start", "en-chat"}

// Onboarding sets the welcome questions of a community server.
// Enabling Community itself needs Administrator, so the owner does that by hand
// (docs/discord.md); this call only needs Manage Server.
func Onboarding(ctx context.Context, c *Client, guildID string, byKey, roleIDs map[string]string, logf func(string, ...any)) error {
	var cur struct {
		Prompts []struct{} `json:"prompts"`
	}
	if err := c.Do(ctx, "GET", "/guilds/"+guildID+"/onboarding", nil, &cur); err != nil {
		return err
	}
	if len(cur.Prompts) > 0 {
		logf("onboarding already has prompts, left as is")
		return nil
	}
	var prompts []map[string]any
	for _, p := range onboardingPrompts {
		var opts []map[string]any
		for _, ch := range p.Choices {
			id, ok := roleIDs[ch.Role]
			if !ok {
				return fmt.Errorf("onboarding: role %q does not exist", ch.Role)
			}
			o := map[string]any{"title": ch.Title, "description": ch.Desc, "role_ids": []string{id}, "channel_ids": []string{}}
			if ch.Emoji != "" {
				o["emoji_name"] = ch.Emoji
			}
			opts = append(opts, o)
		}
		prompts = append(prompts, map[string]any{
			"title": p.Title, "options": opts, "single_select": p.Single,
			"required": p.Required, "in_onboarding": true, "type": 0,
		})
	}
	var defaults []string
	for _, k := range onboardingChannels {
		if id := byKey[k]; id != "" {
			defaults = append(defaults, id)
		}
	}
	body := map[string]any{"prompts": prompts, "default_channel_ids": defaults, "enabled": true, "mode": 0}
	if err := c.Do(ctx, "PUT", "/guilds/"+guildID+"/onboarding", body, nil); err != nil {
		return err
	}
	logf("onboarding set up: %d questions", len(prompts))
	return nil
}
