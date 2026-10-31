package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/entitlement"
	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/lgsm"
	"github.com/stokehold-gsm/stokehold/internal/metrics"
	"github.com/stokehold-gsm/stokehold/internal/sched"
	"github.com/stokehold-gsm/stokehold/internal/store"
	"github.com/stokehold-gsm/stokehold/internal/web"
	"github.com/stokehold-gsm/stokehold/pkg/auth"
	"github.com/stokehold-gsm/stokehold/pkg/license"
)

var version = "dev" // set by -ldflags "-X main.version=..." at release

const defaultLicenseURL = "https://api.stokehold.online"

const usage = `usage:
  stokehold version
  stokehold serve [-db panel.db] [-listen 127.0.0.1:8080] [-helper /run/stokehold/helper.sock] [-license-url URL] [-secure] [-trust-proxy]
  stokehold scan <home>
  stokehold call <socket> <json>`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
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
	licURL := fs.String("license-url", "", "licence server URL (default: the project server once a root key is built in)")
	rootPub := fs.String("root-pub", "", "extra trusted root public key PEM (testing); the id is the file name without .pem")
	helperSock := fs.String("helper", "/run/stokehold/helper.sock", "privileged helper socket")
	proxy := fs.Bool("trust-proxy", false, "use the last X-Forwarded-For entry as the client address (plain listener only)")
	tlsListen := fs.String("tls-listen", "", "also serve HTTPS directly on this address with a self-signed certificate, e.g. :8443")
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

	roots := entitlement.EmbeddedRoots()
	if *rootPub != "" {
		b, err := os.ReadFile(*rootPub)
		if err != nil {
			log.Fatal(err)
		}
		pub, err := entitlement.ParsePublicPEM(b)
		if err != nil {
			log.Fatal(err)
		}
		roots[strings.TrimSuffix(filepath.Base(*rootPub), ".pem")] = pub
	}
	if *licURL == "" && len(roots) > 0 {
		*licURL = defaultLicenseURL
	}
	mgr := &entitlement.Manager{S: st, V: &license.Verifier{Roots: roots}, URL: *licURL, Panel: version}
	if *licURL != "" {
		go mgr.RenewLoop(context.Background(), 24*time.Hour)
	} else {
		log.Print("no licence root key is built in: the panel runs as Free only")
	}
	panel := web.New(st, *secure, *proxy, setupToken)
	panel.Entitlement = mgr
	panel.Version = version
	panel.DataDir = filepath.Dir(*dbPath)
	panel.TLSAddr = *tlsListen
	panel.Helper = func(r helper.Request) (helper.Response, error) { return helper.Call(*helperSock, r) }
	panel.Metrics = metrics.New(60)
	stop := make(chan struct{})
	go panel.Metrics.Run(stop, time.Minute)
	// The helper must be set before the loops start: they copy it.
	runner := &sched.Runner{S: st, Helper: panel.HelperOn}
	go runner.Loop(context.Background(), 30*time.Second)
	go panel.StatusLoop(context.Background(), time.Minute)
	go panel.UpdateLoop(context.Background(), time.Hour)
	srv := &http.Server{
		Addr:              *listen,
		Handler:           panel.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if *tlsListen != "" {
		cert, fp, err := web.SelfSignedCert(filepath.Join(filepath.Dir(*dbPath), "tls"), time.Now())
		if err != nil {
			log.Fatalf("tls certificate: %v", err)
		}
		log.Printf("https listening on %s, certificate sha256 %s", *tlsListen, fp)
		ts := &http.Server{
			Addr:              *tlsListen,
			Handler:           srv.Handler,
			TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			ReadHeaderTimeout: srv.ReadHeaderTimeout,
			ReadTimeout:       srv.ReadTimeout,
			WriteTimeout:      srv.WriteTimeout,
			IdleTimeout:       srv.IdleTimeout,
		}
		go func() { log.Printf("https: %v", ts.ListenAndServeTLS("", "")) }()
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
