package discordbot

import (
	"context"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

// Sink posts embeds into log channels found by name, so nothing needs channel
// ids in configuration. A channel created after the bot started is found on the
// next miss, looked up at most once a minute.
type Sink struct {
	API     *discordsetup.Client
	GuildID string
	Now     func() time.Time // tests only

	mu   sync.Mutex
	ids  map[string]string
	last time.Time
}

func (s *Sink) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sink) channelID(ctx context.Context, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id := s.ids[name]; id != "" {
		return id
	}
	if s.ids != nil && s.now().Sub(s.last) < time.Minute {
		return ""
	}
	s.last = s.now()
	var chans []struct{ ID, Name string }
	if s.API.Do(ctx, "GET", "/guilds/"+s.GuildID+"/channels", nil, &chans) != nil {
		return ""
	}
	s.ids = map[string]string{}
	for _, c := range chans {
		s.ids[c.Name] = c.ID
	}
	return s.ids[name]
}

type errNoChannel string

func (e errNoChannel) Error() string { return "channel " + string(e) + " not found" }

// Embeds posts up to ten embeds per message; mentions in them never ping.
func (s *Sink) Embeds(ctx context.Context, channel string, embeds ...map[string]any) error {
	id := s.channelID(ctx, channel)
	if id == "" {
		return errNoChannel(channel)
	}
	for len(embeds) > 0 {
		n := min(len(embeds), 10)
		body := map[string]any{"embeds": embeds[:n], "allowed_mentions": map[string]any{"parse": []string{}}}
		if err := s.API.Do(ctx, "POST", "/channels/"+id+"/messages", body, nil); err != nil {
			return err
		}
		embeds = embeds[n:]
	}
	return nil
}

// field builds one embed field; Discord rejects empty and over-long values.
func field(name, value string, inline bool) map[string]any {
	if value == "" {
		value = "—"
	}
	return map[string]any{"name": name, "value": clip(value, 1000), "inline": inline}
}

// clip cuts on a rune boundary.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func embed(title string, color int, at time.Time, fields ...map[string]any) map[string]any {
	return map[string]any{"title": title, "color": color, "fields": fields, "timestamp": at.UTC().Format(time.RFC3339)}
}

const (
	colGreen  = 0x2ecc71
	colRed    = 0xe74c3c
	colOrange = 0xe67e22
	colYellow = 0xf1c40f
	colBlue   = 0x3498db
	colPurple = 0x9b59b6
	colGrey   = 0x95a5a6
)
