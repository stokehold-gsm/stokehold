// stokehold-license runs the licence server and manages licences from the shell.
//
//	stokehold-license online-key -root root.key -root-id root1 -kid online-2026-11 [-days 90] -out online.json   (offline machine)
//	stokehold-license serve  -db lic.db -online online.json [-listen 127.0.0.1:8090] [-admin-listen 127.0.0.1:8091] [-trust-proxy]
//	stokehold-license admin-add -db lic.db -name NAME -role owner|curator|operator    (prints a one-time password)
//	stokehold-license admin-reset-2fa -db lic.db -name NAME
//	stokehold-license admin-reset-password -db lic.db -name NAME
//	stokehold-license issue  -db lic.db -order FP-1001 [-plan pro|hosting] [-days 365] [-contact who] [-actor name]
//	stokehold-license list   -db lic.db
//	stokehold-license revoke|reset-binding -db lic.db -order FP-1001
//	stokehold-license extend -db lic.db -order FP-1001 -days 30
//	stokehold-license audit  -db lic.db
//
// The root private key stays offline. Only online.json (online key + its
// certificate) goes to the server.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/licsrv"
	"github.com/stokehold-gsm/stokehold/pkg/auth"
	"github.com/stokehold-gsm/stokehold/pkg/license"
	"github.com/stokehold-gsm/stokehold/pkg/sign"
)

type onlineFile struct {
	KeyPEM string       `json:"key_pem"`
	Cert   license.Cert `json:"cert"`
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: stokehold-license online-key|serve|issue|list|revoke|extend|reset-binding|audit (see the file header)")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	db := fs.String("db", "licenses.db", "licence database")
	order := fs.String("order", "", "order reference (Boosty or FunPay order id)")
	plan := fs.String("plan", "pro", "pro or hosting")
	days := fs.Int("days", 365, "validity or extension in days")
	contact := fs.String("contact", "", "buyer contact (nick or email), optional")
	actor := fs.String("actor", firstEnv("USER", "USERNAME"), "who is doing this, for the audit log")
	root := fs.String("root", "", "root private key PEM (online-key)")
	rootID := fs.String("root-id", "root1", "id of the root key (online-key)")
	kid := fs.String("kid", "", "id of the new online key (online-key)")
	out := fs.String("out", "online.json", "output file (online-key)")
	name := fs.String("name", "", "admin name (admin-add, admin-reset-2fa)")
	role := fs.String("role", "issuer", "owner, curator or operator (admin-add)")
	adminListen := fs.String("admin-listen", "127.0.0.1:8091", "admin interface address (serve)")
	secure := fs.Bool("secure", true, "mark admin cookies Secure; turn off only for local testing over http (serve)")
	online := fs.String("online", "online.json", "online key file (serve)")
	listen := fs.String("listen", "127.0.0.1:8090", "listen address (serve)")
	proxy := fs.Bool("trust-proxy", false, "use the last X-Forwarded-For entry as client address (serve)")
	fs.Parse(args)
	if *actor == "" {
		*actor = "unknown"
	}

	if cmd == "online-key" {
		onlineKey(*root, *rootID, *kid, *days, *out)
		return
	}
	st, err := licsrv.Open(*db)
	if err != nil {
		die(err)
	}
	defer st.Close()

	switch cmd {
	case "serve":
		serve(st, *online, *listen, *adminListen, *proxy, *secure)
	case "issue":
		key, err := st.Issue(*actor, *order, *plan, time.Now().AddDate(0, 0, *days), *contact)
		if err != nil {
			die(err)
		}
		fmt.Println("Licence key (shown once, only a hash is stored):")
		fmt.Println(key)
	case "list":
		ls, err := st.List()
		if err != nil {
			die(err)
		}
		for _, l := range ls {
			bound := "unbound"
			if l.InstancePub != "" {
				bound = "bound"
			}
			fmt.Printf("%-14s %-8s %-8s features=%-2d until=%s %s %s\n", l.OrderRef, l.Plan, l.Status, len(l.Features),
				time.Unix(l.ExpiresAt, 0).UTC().Format("2006-01-02"), bound, l.Contact)
		}
	case "revoke":
		must(st.Revoke(*actor, *order))
	case "extend":
		l, err := st.ByOrder(*order)
		if err != nil {
			die(err)
		}
		base := time.Unix(l.ExpiresAt, 0)
		if base.Before(time.Now()) {
			base = time.Now()
		}
		must(st.Extend(*actor, *order, base.AddDate(0, 0, *days)))
	case "admin-add":
		if *name == "" || !licsrv.ValidRole(*role) {
			die(fmt.Errorf("-name and -role owner|curator|operator are required"))
		}
		pw, _ := auth.NewToken()
		pw = pw[:20]
		must(st.AdminCreate(*name, pw, *role))
		st.AdminAudit(*actor, "admin.add", *name+" "+*role)
		fmt.Printf("Admin %q (%s) created. One-time password, shown once; sign-in then forces setting up two-factor:\n%s\n", *name, *role, pw)
	case "admin-reset-password":
		pw, _ := auth.NewToken()
		pw = pw[:20]
		must(st.AdminSetPassword(*name, pw))
		fmt.Printf("New one-time password for %q, shown once:\n%s\n", *name, pw)
	case "admin-reset-2fa":
		must(st.AdminResetTOTP(*name))
		fmt.Println("two-factor reset; the admin sets it up again at the next sign-in")
	case "reset-binding":
		must(st.ResetBinding(*actor, *order))
	case "audit":
		bad, err := st.VerifyAudit()
		if err != nil {
			die(err)
		}
		rows, _ := st.Audit(50)
		for i := len(rows) - 1; i >= 0; i-- {
			r := rows[i]
			fmt.Printf("%s  %-10s %-14s %s\n", time.Unix(r.TS, 0).UTC().Format("2006-01-02 15:04"), r.Actor, r.Action, r.Detail)
		}
		if bad != 0 {
			die(fmt.Errorf("audit chain is BROKEN at row %d", bad))
		}
		fmt.Println("audit chain intact")
	default:
		die(fmt.Errorf("unknown command %q", cmd))
	}
}

func must(err error) {
	if err != nil {
		die(err)
	}
}

// onlineKey runs on the machine that holds the root key.
func onlineKey(rootPath, rootID, kid string, days int, outPath string) {
	if rootPath == "" || kid == "" {
		die(fmt.Errorf("-root and -kid are required"))
	}
	rb, err := os.ReadFile(rootPath)
	must(err)
	rootKey, err := sign.ParsePrivate(rb)
	must(err)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	must(err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	must(err)
	cert := license.IssueCert(rootID, rootKey, kid, pub, time.Now().AddDate(0, 0, days))
	b, _ := json.MarshalIndent(onlineFile{KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), Cert: cert}, "", "  ")
	if _, err := os.Stat(outPath); err == nil {
		die(fmt.Errorf("%s exists, refusing to overwrite", outPath))
	}
	must(os.WriteFile(outPath, b, 0o600))
	fmt.Printf("wrote %s: online key %q, certified by %q until %s\n", outPath, kid, rootID, time.Unix(cert.NotAfter, 0).UTC().Format("2006-01-02"))
}

func serve(st *licsrv.Store, onlinePath, listen, adminListen string, proxy, secure bool) {
	b, err := os.ReadFile(onlinePath)
	must(err)
	var f onlineFile
	must(json.Unmarshal(b, &f))
	key, err := sign.ParsePrivate([]byte(f.KeyPEM))
	must(err)
	if time.Now().Unix() > f.Cert.NotAfter {
		die(fmt.Errorf("the online key certificate expired on %s; make a new one with online-key", time.Unix(f.Cert.NotAfter, 0).UTC().Format("2006-01-02")))
	}
	s := licsrv.NewServer(st, key, f.Cert)
	s.TrustProxy = proxy
	srv := &http.Server{
		Addr: listen, Handler: s.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ui := licsrv.NewAdminUI(st, secure, proxy)
	if st.AdminCount() == 0 {
		log.Print("no admin users yet: run  stokehold-license admin-add -name NAME -role owner")
	}
	admin := &http.Server{Addr: adminListen, Handler: ui.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	go func() { log.Fatal(admin.ListenAndServe()) }()
	log.Printf("admin interface on %s", adminListen)
	log.Printf("licence server on %s, online key %q valid until %s", listen, f.Cert.KID, time.Unix(f.Cert.NotAfter, 0).UTC().Format("2006-01-02"))
	log.Fatal(srv.ListenAndServe())
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
