// discord-setup lays out the project Discord server.
//
//	DISCORD_BOT_TOKEN=... discord-setup -guild 1234 [-clean-defaults]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"stokehold/internal/discordsetup"
)

func main() {
	guild := flag.String("guild", "", "guild (server) id")
	clean := flag.Bool("clean-defaults", false, "remove the empty default channels of a new server")
	invite := flag.String("invite-for", "", "print the bot invite URL for this application id and exit")
	flag.Parse()

	if *invite != "" {
		fmt.Printf("https://discord.com/oauth2/authorize?client_id=%s&scope=bot&permissions=%d\n", *invite, discordsetup.InvitePerms)
		return
	}
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" || *guild == "" {
		log.Fatal("need DISCORD_BOT_TOKEN in the environment and -guild")
	}
	c := &discordsetup.Client{Base: discordsetup.DefaultBase, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
	err := discordsetup.Apply(context.Background(), c, *guild, discordsetup.Default(), discordsetup.Options{
		CleanDefaults: *clean,
		Log:           func(f string, a ...any) { log.Printf(f, a...) },
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Print("done")
}
