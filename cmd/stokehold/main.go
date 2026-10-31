package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"stokehold/internal/auth"
	"stokehold/internal/helper"
	"stokehold/internal/lgsm"
	"stokehold/internal/store"
	"stokehold/internal/web"
)

const usage = `usage:
  stokehold serve [-db panel.db] [-listen 127.0.0.1:8080] [-secure] [-trust-proxy]
  stokehold scan <home>
  stokehold call <socket> <json>`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "scan":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		scan(os.Args[2])
	case "call":
		if len(os.Args) != 4 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		call(os.Args[2], os.Args[3])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "panel.db", "SQLite database path")
	listen := fs.String("listen", "127.0.0.1:8080", "listen address (put a TLS proxy in front)")
	secure := fs.Bool("secure", false, "mark cookies Secure (use when served over HTTPS)")
	proxy := fs.Bool("trust-proxy", false, "use the last X-Forwarded-For entry as the client address")
	fs.Parse(args)

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	// While there is no owner, /setup needs a one-time code that only someone
	// with access to this log can read.
	setupToken := ""
	if n, _ := st.CountUsers(); n == 0 {
		setupToken, _ = auth.NewToken()
		log.Printf("no users yet: open /setup and enter the setup code %s", setupToken)
	}
	go func() {
		for range time.Tick(time.Hour) {
			st.PurgeExpired()
		}
	}()

	srv := &http.Server{
		Addr:              *listen,
		Handler:           web.New(st, *secure, *proxy, setupToken).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("listening on %s", *listen)
	log.Fatal(srv.ListenAndServe())
}

func scan(home string) {
	servers, err := lgsm.Discover(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, s := range servers {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		out, _ := lgsm.Run(ctx, s, "details")
		cancel()
		st, err := lgsm.ParseStatus(out)
		if err != nil {
			st = "unknown"
		}
		fmt.Printf("%s %s %s\n", s.Name, s.Dir, st)
	}
}

func call(socket, body string) {
	var req helper.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	resp, err := helper.Call(socket, req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(resp)
}
