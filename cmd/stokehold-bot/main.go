// stokehold-bot serves Discord interactions (role buttons) over HTTP.
// Behind a TLS reverse proxy, see deploy/.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"time"

	"stokehold/internal/discordbot"
	"stokehold/internal/discordsetup"
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
	h := &discordbot.Handler{
		PublicKey: key,
		API:       &discordsetup.Client{Base: discordsetup.DefaultBase, Token: token, HTTP: &http.Client{Timeout: 3 * time.Second}},
		SelfRoles: self,
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
