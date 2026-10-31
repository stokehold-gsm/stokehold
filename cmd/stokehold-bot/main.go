// stokehold-bot serves Discord interactions (role buttons, moderation
// commands, shop tickets) over HTTP and copies the audit log into the events
// channel. Behind a TLS reverse proxy, see deploy/.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordbot"
	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

func main() {
	token := os.Getenv("DISCORD_BOT_TOKEN")
	key, err := hex.DecodeString(os.Getenv("DISCORD_PUBLIC_KEY"))
	if token == "" || err != nil || len(key) != ed25519.PublicKeySize {
		log.Fatal("need DISCORD_BOT_TOKEN and DISCORD_PUBLIC_KEY (64 hex chars)")
	}
	listen := os.Getenv("STOKEHOLD_BOT_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8081"
	}
	self := map[string]string{}
	for _, s := range discordsetup.Default().SelfRoles {
		self[s.Key] = s.RoleName
	}
	api := &discordsetup.Client{Base: discordsetup.DefaultBase, Token: token, HTTP: &http.Client{Timeout: 10 * time.Second}}
	h := &discordbot.Handler{PublicKey: key, API: api, SelfRoles: self}

	// Commands and tickets need the application id to answer after the first 3 seconds.
	if app := os.Getenv("DISCORD_APP_ID"); app != "" {
		warns, err := discordbot.OpenWarns(os.Getenv("STOKEHOLD_BOT_WARNS"))
		if err != nil {
			log.Fatalf("warnings file: %v", err)
		}
		h.AppID = app
		h.Mod = &discordbot.Mod{API: api, LogName: discordsetup.LogChannel, Warns: warns}
		h.Tickets = &discordbot.Tickets{Category: discordsetup.TicketCategory, Notify: discordsetup.TicketNotify, Orders: discordsetup.TicketOrders}
	}
	if guild := os.Getenv("DISCORD_GUILD_ID"); guild != "" {
		ev := &discordbot.Events{API: api, GuildID: guild, Channel: discordsetup.EventsChannel}
		go ev.Run(context.Background(), log.Printf)
	}

	mux := http.NewServeMux()
	mux.Handle("/discord/interactions", h)
	srv := &http.Server{
		Addr: listen, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second,
	}
	log.Printf("listening on %s", listen)
	log.Fatal(srv.ListenAndServe())
}
