package discordsetup

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type role struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
				ow = append(ow, overwrite{ID: id, Allow: perm(PermViewChannel | PermSendMessages | PermReadHistory), Deny: "0"})
			}
		}
	case ch.ReadOnly:
		ow = append(ow, overwrite{ID: guildID, Allow: "0", Deny: perm(PermSendMessages)})
		if id, ok := roleIDs[RoleMod]; ok {
			ow = append(ow, overwrite{ID: id, Allow: perm(PermSendMessages), Deny: "0"})
		}
	}
	return ow
}

// Apply creates what is missing. It never edits or deletes existing things
// except the explicit CleanDefaults case.
func Apply(ctx context.Context, c *Client, guildID string, p Plan, o Options) error {
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, "GET", "/users/@me", nil, &me); err != nil {
		return err
	}

	var roles []role
	if err := c.do(ctx, "GET", "/guilds/"+guildID+"/roles", nil, &roles); err != nil {
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
		body := map[string]any{"name": r.Name, "color": r.Color, "hoist": r.Hoist, "permissions": perm(r.Perms)}
		if err := c.do(ctx, "POST", "/guilds/"+guildID+"/roles", body, &made); err != nil {
			return err
		}
		roleIDs[r.Name] = made.ID
		logf("role created: %s", r.Name)
	}

	var chans []channel
	if err := c.do(ctx, "GET", "/guilds/"+guildID+"/channels", nil, &chans); err != nil {
		return err
	}
	if o.CleanDefaults {
		for _, ch := range chans {
			if isDefault(ch) {
				if err := c.do(ctx, "DELETE", "/channels/"+ch.ID, nil, nil); err != nil {
					return err
				}
				logf("default removed: %s", ch.Name)
			}
		}
		if err := c.do(ctx, "GET", "/guilds/"+guildID+"/channels", nil, &chans); err != nil {
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
		err := c.do(ctx, "POST", "/guilds/"+guildID+"/channels", body, &made)
		return made.ID, err
	}

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
			id := find(ch.Name, 0, catID)
			if id == "" {
				var err error
				id, err = create(map[string]any{
					"name": ch.Name, "type": 0, "parent_id": catID, "topic": ch.Topic,
					"permission_overwrites": Overwrites(ch, guildID, me.ID, roleIDs),
				})
				if err != nil {
					return err
				}
				logf("channel created: %s/%s", cat.Name, ch.Name)
			}
			if err := pin(ctx, c, id, ch.Pins, logf); err != nil {
				return err
			}
		}
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

// pin posts the texts only when the channel has no pins, so reruns stay quiet.
func pin(ctx context.Context, c *Client, channelID string, texts []string, logf func(string, ...any)) error {
	if len(texts) == 0 {
		return nil
	}
	var pins struct {
		Items []any `json:"items"`
	}
	if err := c.do(ctx, "GET", "/channels/"+channelID+"/messages/pins", nil, &pins); err != nil {
		return err
	}
	if len(pins.Items) > 0 {
		return nil
	}
	for _, t := range texts {
		var msg struct {
			ID string `json:"id"`
		}
		if err := c.do(ctx, "POST", "/channels/"+channelID+"/messages", map[string]any{"content": t}, &msg); err != nil {
			return err
		}
		if err := c.do(ctx, "PUT", fmt.Sprintf("/channels/%s/messages/pins/%s", channelID, msg.ID), nil, nil); err != nil {
			return err
		}
	}
	logf("pinned %d message(s) in %s", len(texts), channelID)
	return nil
}
