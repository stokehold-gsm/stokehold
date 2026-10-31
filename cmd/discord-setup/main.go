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

	"github.com/stokehold-gsm/stokehold/internal/discordbot"
	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

func main() {
	guild := flag.String("guild", "", "guild (server) id")
	clean := flag.Bool("clean-defaults", false, "remove the empty default channels of a new server")
	legacy := flag.Bool("remove-legacy", false, "remove channels and categories of earlier layouts")
	repost := flag.Bool("repost", false, "replace the bot's pinned messages with the current texts")
	onboarding := flag.Bool("onboarding", false, "set up onboarding questions (community must be enabled first)")
	automod := flag.Bool("automod", false, "create the AutoMod rules (bot needs Manage Server)")
	founder := flag.String("founder", "", "user id that gets the Founder role")
	commands := flag.String("register-commands", "", "register the moderation slash commands for this application id and exit")
	invite := flag.String("invite-for", "", "print the bot invite URL for this application id and exit")
	flag.Parse()

	if *commands != "" {
		token, g := os.Getenv("DISCORD_BOT_TOKEN"), *guild
		if token == "" || g == "" {
			log.Fatal("need DISCORD_BOT_TOKEN and -guild")
		}
		c := &discordsetup.Client{Base: discordsetup.DefaultBase, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
		if err := c.Do(context.Background(), "PUT", "/applications/"+*commands+"/guilds/"+g+"/commands", discordbot.Commands(), nil); err != nil {
			log.Fatal(err)
		}
		log.Print("commands registered")
		return
	}
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
		RemoveLegacy:  *legacy,
		Repost:        *repost,
		Onboarding:    *onboarding,
		AutoMod:       *automod,
		FounderID:     *founder,
		Log:           func(f string, a ...any) { log.Printf(f, a...) },
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Print("done")
}
