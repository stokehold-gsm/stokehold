package discordsetup

import "context"

type autoRule struct {
	Name    string
	Trigger int
	Meta    map[string]any
	Timeout int // seconds, 0 for none
	Message string
}

// Staff are exempt so moderators can quote what they are removing.
var autoRules = []autoRule{
	{"Спам", 3, nil, 0, "Сообщение похоже на спам."},
	{"Массовые упоминания", 5, map[string]any{"mention_total_limit": 6, "mention_raw_enabled": true}, 300, "Слишком много упоминаний в одном сообщении."},
	{"Скам с нитро и подарками", 1, map[string]any{"keyword_filter": []string{
		"free nitro", "discord-nitro", "discordgift", "discord.gift/*", "бесплатный нитро", "дарю нитро",
	}}, 600, "Похоже на мошенническую ссылку. Если это ошибка, напишите модератору."},
}

// AutoMod creates the missing rules, matched by name. Rules that exist stay
// as they are, so the owner can tune them in Discord.
func AutoMod(ctx context.Context, c *Client, guildID, alertChannel string, exempt []string, logf func(string, ...any)) error {
	var have []struct {
		Name string `json:"name"`
	}
	if err := c.Do(ctx, "GET", "/guilds/"+guildID+"/auto-moderation/rules", nil, &have); err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, r := range have {
		exists[r.Name] = true
	}
	for _, r := range autoRules {
		if exists[r.Name] {
			continue
		}
		actions := []map[string]any{{"type": 1, "metadata": map[string]any{"custom_message": r.Message}}}
		if alertChannel != "" {
			actions = append(actions, map[string]any{"type": 2, "metadata": map[string]any{"channel_id": alertChannel}})
		}
		if r.Timeout > 0 {
			actions = append(actions, map[string]any{"type": 3, "metadata": map[string]any{"duration_seconds": r.Timeout}})
		}
		body := map[string]any{
			"name": r.Name, "event_type": 1, "trigger_type": r.Trigger, "actions": actions,
			"enabled": true, "exempt_roles": exempt,
		}
		if r.Meta != nil {
			body["trigger_metadata"] = r.Meta
		}
		if err := c.Do(ctx, "POST", "/guilds/"+guildID+"/auto-moderation/rules", body, nil); err != nil {
			return err
		}
		logf("automod rule created: %s", r.Name)
	}
	return nil
}
