package discordsetup

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type role struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Tags     struct {
		BotID string `json:"bot_id"`
	} `json:"tags"`
}

type channel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     int    `json:"type"`
	ParentID string `json:"parent_id"`
}

type overwrite struct {
	ID    string `json:"id"`
	Type  int    `json:"type"` // 0 role, 1 member
	Allow string `json:"allow"`
	Deny  string `json:"deny"`
}

type Options struct {
	CleanDefaults bool // remove the empty channels Discord creates with a new server
	RemoveLegacy  bool // remove channels, categories and roles of earlier layouts (Plan.Legacy, Plan.LegacyRoles)
	Repost        bool // replace the bot's pinned messages with the current texts
	Onboarding    bool // fill in the onboarding questions (server must already be a community server)
	Log           func(format string, args ...any)
}

func perm(v int64) string { return strconv.FormatInt(v, 10) }

// Overwrites builds the permission overwrites for one channel.
// Exported for tests; guildID doubles as the @everyone role id.
func Overwrites(ch Channel, guildID, botID string, roleIDs map[string]string) []overwrite {
	const botAllow = PermViewChannel | PermSendMessages | PermReadHistory | PermPinMessages | PermEmbedLinks
	ow := []overwrite{{ID: botID, Type: 1, Allow: perm(botAllow), Deny: "0"}}
	switch {
	case len(ch.OnlyFor) > 0:
		ow = append(ow, overwrite{ID: guildID, Allow: "0", Deny: perm(PermViewChannel)})
		for _, name := range ch.OnlyFor {
			if id, ok := roleIDs[name]; ok {
				ow = append(ow, overwrite{ID: id, Allow: perm(staffAllow(ch, name)), Deny: "0"})
			}
		}
	case ch.ReadOnly:
		ow = append(ow, overwrite{ID: guildID, Allow: "0", Deny: perm(PermSendMessages | PermAddReactions)})
		if id, ok := roleIDs[RoleMod]; ok {
			ow = append(ow, overwrite{ID: id, Allow: perm(PermSendMessages), Deny: "0"})
		}
	}
	return ow
}

// Apply creates what is missing. It never edits existing things and deletes
// only what the options explicitly ask for.
func Apply(ctx context.Context, c *Client, guildID string, p Plan, o Options) error {
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := c.Do(ctx, "GET", "/users/@me", nil, &me); err != nil {
		return err
	}

	var roles []role
	if err := c.Do(ctx, "GET", "/guilds/"+guildID+"/roles", nil, &roles); err != nil {
		return err
	}
	roleIDs := map[string]string{}
	for _, r := range roles {
		roleIDs[r.Name] = r.ID
	}
	for _, r := range p.Roles {
		if _, ok := roleIDs[r.Name]; ok {
			continue
		}
		var made role
		body := map[string]any{"name": r.Name, "color": r.Color, "hoist": r.Hoist, "mentionable": r.Mentionable, "permissions": perm(r.Perms)}
		if err := c.Do(ctx, "POST", "/guilds/"+guildID+"/roles", body, &made); err != nil {
			return err
		}
		roleIDs[r.Name] = made.ID
		logf("role created: %s", r.Name)
	}
	if o.RemoveLegacy {
		for _, name := range p.LegacyRoles {
			if id, ok := roleIDs[name]; ok {
				if err := c.Do(ctx, "DELETE", "/guilds/"+guildID+"/roles/"+id, nil, nil); err != nil {
					return err
				}
				delete(roleIDs, name)
				logf("role removed: %s", name)
			}
		}
	}
	if err := orderRoles(ctx, c, guildID, me.ID, roles, roleIDs, p.Roles, logf); err != nil {
		return err
	}

	var chans []channel
	if err := c.Do(ctx, "GET", "/guilds/"+guildID+"/channels", nil, &chans); err != nil {
		return err
	}
	if o.CleanDefaults || o.RemoveLegacy {
		legacy := map[string]bool{}
		for _, n := range p.Legacy {
			legacy[strings.ToLower(n)] = true
		}
		for _, ch := range chans {
			if (o.CleanDefaults && isDefault(ch)) || (o.RemoveLegacy && legacy[strings.ToLower(ch.Name)]) {
				if err := c.Do(ctx, "DELETE", "/channels/"+ch.ID, nil, nil); err != nil {
					return err
				}
				logf("removed: %s", ch.Name)
			}
		}
		if err := c.Do(ctx, "GET", "/guilds/"+guildID+"/channels", nil, &chans); err != nil {
			return err
		}
	}

	find := func(name string, typ int, parent string) string {
		for _, ch := range chans {
			if ch.Type == typ && strings.EqualFold(ch.Name, name) && ch.ParentID == parent {
				return ch.ID
			}
		}
		return ""
	}
	create := func(body map[string]any) (string, error) {
		var made channel
		err := c.Do(ctx, "POST", "/guilds/"+guildID+"/channels", body, &made)
		return made.ID, err
	}

	type placed struct {
		id string
		ch Channel
	}
	var todo []placed
	byKey := map[string]string{}
	for _, cat := range p.Categories {
		catID := find(cat.Name, 4, "")
		if catID == "" {
			var err error
			if catID, err = create(map[string]any{"name": cat.Name, "type": 4}); err != nil {
				return err
			}
			logf("category created: %s", cat.Name)
		}
		for _, ch := range cat.Channels {
			typ := 0
			if ch.Voice {
				typ = 2
			}
			id := find(ch.Name, typ, catID)
			if id == "" {
				body := map[string]any{
					"name": ch.Name, "type": typ, "parent_id": catID,
					"permission_overwrites": Overwrites(ch, guildID, me.ID, roleIDs),
				}
				if !ch.Voice {
					body["topic"] = ch.Topic
				}
				var err error
				if id, err = create(body); err != nil {
					return err
				}
				logf("channel created: %s / %s", cat.Name, ch.Name)
			} else if len(ch.OnlyFor) > 0 || ch.ReadOnly {
				// staff roles change over time, so existing restricted channels are brought up to date
				for _, ow := range Overwrites(ch, guildID, me.ID, roleIDs) {
					body := map[string]any{"allow": ow.Allow, "deny": ow.Deny, "type": ow.Type}
					if err := c.Do(ctx, "PUT", "/channels/"+id+"/permissions/"+ow.ID, body, nil); err != nil {
						return err
					}
				}
			}
			byKey[ch.Key] = id
			todo = append(todo, placed{id, ch})
		}
	}
	// Messages go out after every channel exists so {#key} can resolve.
	for _, t := range todo {
		if len(t.ch.Messages) == 0 {
			continue
		}
		if err := post(ctx, c, me.ID, t.id, t.ch.Messages, byKey, o.Repost, logf); err != nil {
			return err
		}
	}
	if o.Onboarding {
		return Onboarding(ctx, c, guildID, byKey, roleIDs, logf)
	}
	return nil
}

func isDefault(ch channel) bool {
	switch ch.Type {
	case 4:
		return ch.Name == "Текстовые каналы" || ch.Name == "Голосовые каналы" || ch.Name == "Text Channels" || ch.Name == "Voice Channels"
	case 0:
		return ch.Name == "основной" || ch.Name == "general"
	case 2:
		return ch.Name == "Основной" || ch.Name == "General"
	}
	return false
}

func resolve(s string, byKey map[string]string) string {
	for k, id := range byKey {
		s = strings.ReplaceAll(s, "{#"+k+"}", "<#"+id+">")
	}
	return s
}

// payload renders a Message into the JSON Discord expects.
func payload(m Message, byKey map[string]string) map[string]any {
	out := map[string]any{}
	if m.Content != "" {
		out["content"] = resolve(m.Content, byKey)
	}
	var embeds []map[string]any
	for _, e := range m.Embeds {
		em := map[string]any{"color": e.Color}
		if e.Title != "" {
			em["title"] = e.Title
		}
		if e.Description != "" {
			em["description"] = resolve(e.Description, byKey)
		}
		if e.Footer != "" {
			em["footer"] = map[string]string{"text": e.Footer}
		}
		var fields []map[string]any
		for _, f := range e.Fields {
			fields = append(fields, map[string]any{"name": f.Name, "value": resolve(f.Value, byKey), "inline": f.Inline})
		}
		if len(fields) > 0 {
			em["fields"] = fields
		}
		embeds = append(embeds, em)
	}
	if len(embeds) > 0 {
		out["embeds"] = embeds
	}
	var rows []map[string]any
	for _, row := range m.Rows {
		var comps []map[string]any
		for _, b := range row {
			id, style := "role:"+b.Key, 2
			if b.ID != "" {
				id, style = b.ID, 1
			}
			c := map[string]any{"type": 2, "style": style, "label": b.Label, "custom_id": id}
			if b.Emoji != "" {
				c["emoji"] = map[string]string{"name": b.Emoji}
			}
			comps = append(comps, c)
		}
		rows = append(rows, map[string]any{"type": 1, "components": comps})
	}
	if len(rows) > 0 {
		out["components"] = rows
	}
	return out
}

// staffAllow is what a listed role may do in a restricted channel. In a
// read-only staff channel only the founder writes.
func staffAllow(ch Channel, role string) int64 {
	p := int64(PermViewChannel | PermReadHistory)
	if !ch.ReadOnly || role == RoleFounder {
		p |= PermSendMessages
	}
	return p
}

// orderRoles puts the plan's roles in hierarchy order, directly under the
// bot's own role. A bot cannot move roles above itself, so if it does not
// sit high enough the order is left as it is.
func orderRoles(ctx context.Context, c *Client, guildID, botID string, roles []role, ids map[string]string, plan []Role, logf func(string, ...any)) error {
	top := 0
	for _, r := range roles {
		if r.Tags.BotID == botID {
			top = r.Position - 1
		}
	}
	var body []map[string]any
	for i, r := range plan {
		if id, ok := ids[r.Name]; ok {
			body = append(body, map[string]any{"id": id, "position": top - i})
		}
	}
	if top < len(plan) || len(body) == 0 {
		logf("role order left as is: the bot role is not above the %d staff and tier roles", len(plan))
		return nil
	}
	return c.Do(ctx, "PATCH", "/guilds/"+guildID+"/roles", body, nil)
}

type pinned struct {
	Items []struct {
		Message struct {
			ID     string `json:"id"`
			Author struct {
				ID string `json:"id"`
			} `json:"author"`
		} `json:"message"`
	} `json:"items"`
}

// post sends the messages only when the channel has no pins, so reruns stay
// quiet. With repost it first removes the bot's own pinned messages.
func post(ctx context.Context, c *Client, botID, channelID string, msgs []Message, byKey map[string]string, repost bool, logf func(string, ...any)) error {
	var pins pinned
	if err := c.Do(ctx, "GET", "/channels/"+channelID+"/messages/pins", nil, &pins); err != nil {
		return err
	}
	if len(pins.Items) > 0 && !repost {
		return nil
	}
	for _, it := range pins.Items {
		if it.Message.Author.ID == botID {
			if err := c.Do(ctx, "DELETE", "/channels/"+channelID+"/messages/"+it.Message.ID, nil, nil); err != nil {
				return err
			}
		}
	}
	for _, m := range msgs {
		var msg struct {
			ID string `json:"id"`
		}
		if err := c.Do(ctx, "POST", "/channels/"+channelID+"/messages", payload(m, byKey), &msg); err != nil {
			return err
		}
		if err := c.Do(ctx, "PUT", fmt.Sprintf("/channels/%s/messages/pins/%s", channelID, msg.ID), nil, nil); err != nil {
			return err
		}
	}
	logf("posted %d message(s) in %s", len(msgs), channelID)
	return nil
}
