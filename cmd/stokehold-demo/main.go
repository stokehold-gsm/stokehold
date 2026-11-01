// stokehold-demo serves the panel with made-up data and a pretend helper, so
// the interface can be looked at (and screenshotted) without a game server.
// Nothing it shows is real, nothing it does touches a machine. It is not part
// of the release.
//
//	stokehold-demo -listen 127.0.0.1:18080 -email demo@stokehold.ru -password ...
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/entitlement"
	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/metrics"
	"github.com/stokehold-gsm/stokehold/internal/store"
	"github.com/stokehold-gsm/stokehold/internal/web"
	"github.com/stokehold-gsm/stokehold/pkg/license"
)

type ent struct{}

func (ent) Status() entitlement.Status {
	return entitlement.Status{Configured: true, HasKey: true, Plan: "hosting", State: license.Active, ExpiresAt: time.Now().AddDate(0, 6, 0).Unix(),
		Features: license.PlanFeatures("hosting")}
}
func (ent) Activate(context.Context, string) error { return nil }
func (ent) Renew(context.Context) error            { return nil }

type fake struct {
	mu    sync.Mutex
	state map[string]string // linux user -> STARTED | STOPPED | STARTING
}

const detailsTmpl = `=====================================
Distro Details
=====================================
Distro:       Ubuntu 24.04 LTS
Arch:         x86_64
Kernel:       6.8.0-45-generic
Free Memory:  2.4GB
Free Disk:    41.2GB

=====================================
Server Resource
=====================================
CPU Used:  4.2%
Mem Used:  18.0%   720MB

=====================================
%GAME% Details
=====================================
Server name:       %NAME%
Server IP:         203.0.113.7:%PORT%
Server password:   NOT SET
Players:           %PLAYERS%
Status:            %STATUS%

=====================================
Ports
=====================================
DESCRIPTION  PORT  PROTOCOL  LISTEN
> Game       %PORT% udp       %LISTEN%
`

var games = map[string]struct{ name, port, players string }{
	"gs_fct": {"Factorio", "34197", "3/16"},
	"gs_mc":  {"Minecraft", "25565", "7/20"},
	"gs_scp": {"SCP: Secret Laboratory", "7777", "0/30"},
}

func (f *fake) run(user, cmd string) helper.Response {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := games[user]
	if !ok {
		return helper.Response{Err: "unknown account"}
	}
	switch cmd {
	case "start", "restart":
		f.state[user] = "STARTED"
		return helper.Response{OK: true, Out: "[ .... ] Starting " + g.name + ": \n[  OK  ] Starting " + g.name + ": Demo\n"}
	case "stop":
		f.state[user] = "STOPPED"
		return helper.Response{OK: true, Out: "[  OK  ] Stopping " + g.name + ": Demo\n"}
	case "backup":
		return helper.Response{OK: true, Out: "[  OK  ] Backup complete: demo-backup.tar.gz (1.2G)\n"}
	case "check-update":
		return helper.Response{OK: true, Out: "[  OK  ] Checking update: no update available\n* Local build: 1.1.109\n* Remote build: 1.1.109\n"}
	}
	st := f.state[user]
	listen, players := "YES", g.players
	if st == "STOPPED" {
		listen, players = "NO", "0/"+strings.Split(g.players, "/")[1]
	}
	out := strings.NewReplacer("%GAME%", g.name, "%NAME%", g.name+" demo", "%PORT%", g.port, "%PLAYERS%", players, "%STATUS%", st, "%LISTEN%", listen).Replace(detailsTmpl)
	return helper.Response{OK: true, Out: out}
}

var tree = map[string][]map[string]any{
	".":                             {{"n": "serverfiles", "t": "d", "s": 0, "m": 1762000000}, {"n": "lgsm", "t": "d", "s": 0, "m": 1762000000}, {"n": "log", "t": "d", "s": 0, "m": 1762000000}, {"n": "backups", "t": "d", "s": 0, "m": 1762000000}, {"n": ".config", "t": "d", "s": 0, "m": 1762000000}, {"n": "start.sh", "t": "f", "s": 312, "m": 1762000000}},
	".config":                       {{"n": "SCP Secret Laboratory", "t": "d", "s": 0, "m": 1762000000}},
	".config/SCP Secret Laboratory": {{"n": "config", "t": "d", "s": 0, "m": 1762000000}, {"n": "LocalAdminLogs", "t": "d", "s": 0, "m": 1762000000}, {"n": "config_gameplay.txt", "t": "f", "s": 18342, "m": 1762000000}},
	"serverfiles":                   {{"n": "server-settings.json", "t": "f", "s": 1432, "m": 1762000000}, {"n": "mods", "t": "d", "s": 0, "m": 1762000000}, {"n": "saves", "t": "d", "s": 0, "m": 1762000000}},
	"log":                           {{"n": "console", "t": "d", "s": 0, "m": 1762000000}, {"n": "script", "t": "d", "s": 0, "m": 1762000000}},
	"backups":                       {{"n": "demo-backup.tar.gz", "t": "f", "s": 1288490188, "m": 1762000000}},
}

const logText = "[ 12:00:01 ] Server started\n[ 12:00:04 ] Loading map...\n[ 12:00:09 ] Map loaded in 4.9 s\n[ 12:03:11 ] Player Alice joined\n[ 12:07:40 ] Player Bob joined\n[ 12:15:02 ] Autosave complete\n"

func (f *fake) call(r helper.Request) (helper.Response, error) {
	switch r.Op {
	case "run":
		return f.run(r.User, r.Cmd), nil
	case "discover":
		return helper.Response{OK: true, Out: "fctrserver\n"}, nil
	case "list-dir":
		b, _ := json.Marshal(map[string]any{"list": tree[strings.Trim(r.Path, "/")], "more": false})
		return helper.Response{OK: true, Out: string(b)}, nil
	case "stat":
		return helper.Response{OK: true, Out: `{"n":"x","t":"f","s":` + "400" + `,"m":1762000000}`}, nil
	case "get":
		return helper.Response{OK: true, Out: base64.StdEncoding.EncodeToString([]byte(logText))}, nil
	case "http-get-json":
		if strings.Contains(r.URL, "modrinth") {
			return helper.Response{OK: true, Out: `{"hits":[{"project_id":"AANobbMI","title":"Sodium","description":"Modern rendering engine and optimization mod","author":"jellysquid3","downloads":50000000},{"project_id":"P7dR8mSH","title":"Fabric API","description":"Core library for Fabric mods","author":"modmuss50","downloads":90000000}]}`}, nil
		}
		return helper.Response{OK: true, Out: `{"latest":{"release":"1.21.4","snapshot":"25w02a"},"versions":[{"id":"25w02a","type":"snapshot"},{"id":"1.21.4","type":"release"},{"id":"1.21.3","type":"release"},{"id":"1.20.6","type":"release"}]}`}, nil
	case "files":
		return helper.Response{OK: true, Out: "serverfiles/server-settings.json\n"}, nil
	case "update-status":
		return helper.Response{OK: true, Out: "idle\n"}, nil
	}
	return helper.Response{OK: true, Out: "demo: nothing was changed"}, nil
}

func main() {
	listen := flag.String("listen", "127.0.0.1:18080", "listen address")
	email := flag.String("email", "demo@stokehold.ru", "demo owner email")
	password := flag.String("password", "", "demo owner password")
	secure := flag.Bool("secure", false, "mark cookies Secure (behind HTTPS)")
	proxy := flag.Bool("trust-proxy", false, "trust X-Forwarded-For")
	flag.Parse()
	if *password == "" {
		log.Fatal("-password is required")
	}
	dir, err := os.MkdirTemp("", "stokehold-demo")
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "demo.db"))
	if err != nil {
		log.Fatal(err)
	}
	if _, err := st.CreateUser(*email, *password, store.RoleOwner); err != nil {
		log.Fatal(err)
	}
	for _, s := range []store.Server{
		{Name: "Factorio: сообщество", Game: "fctrserver", LinuxUser: "gs_fct", Dir: "/home/gs_fct", Mode: "adopted"},
		{Name: "Minecraft Survival", Game: "mcserver", LinuxUser: "gs_mc", Dir: "/home/gs_mc", Mode: "managed"},
		{Name: "SCP: Secret Laboratory", Game: "scpslserver", LinuxUser: "gs_scp", Dir: "/home/gs_scp", Mode: "managed"},
	} {
		if _, err := st.AddServer(s); err != nil {
			log.Fatal(err)
		}
	}
	f := &fake{state: map[string]string{"gs_fct": "STARTED", "gs_mc": "STARTING", "gs_scp": "STOPPED"}}
	p := web.New(st, *secure, *proxy, "")
	p.Entitlement = ent{}
	p.Helper = f.call
	p.Version = "v0.2.0"
	p.Metrics = metrics.New(60)
	go p.Metrics.Run(make(chan struct{}), 10*time.Second)
	seedHistory(p)
	p.RefreshStatus()
	go p.StatusLoop(context.Background(), 20*time.Second)
	log.Printf("demo on http://%s as %s", *listen, *email)
	log.Fatal((&http.Server{Addr: *listen, Handler: p.Handler(), ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}

// seedHistory draws a believable day so the charts and sparklines have something to show.
func seedHistory(p *web.Server) {
	now := time.Now()
	type shape struct {
		peak, max int
		cpu, mem  float64
		down      int
	}
	shapes := map[int64]shape{1: {14, 16, 18, 1400, 300}, 2: {16, 20, 35, 3100, 1300}, 3: {22, 30, 12, 900, 1430}}
	for id, sh := range shapes {
		for i := 0; i < 1440; i++ {
			t := now.Add(time.Duration(i-1440) * time.Minute)
			h := float64(t.Hour()) + float64(t.Minute())/60
			// evenings are busy, nights are quiet
			load := 0.5 + 0.5*math.Sin((h-14)/24*2*math.Pi)
			players := int(float64(sh.peak)*load*load + 0.5*math.Sin(float64(i)/7))
			if players < 0 {
				players = 0
			}
			state := "started"
			if i >= sh.down && i < sh.down+25 {
				state, players = "stopped", 0
			}
			if i == sh.down+25 {
				state = "starting"
			}
			if id == 3 && i >= 1430 {
				state, players = "stopped", 0
			}
			cpu, mem := sh.cpu*(0.4+load)+3*math.Sin(float64(i)/5), sh.mem*(0.8+0.3*load)
			if state != "started" {
				cpu, mem = 0, 0
			}
			p.AddHistory(id, web.Sample{T: t, State: state, Players: players, Max: sh.max, HasPlayers: true, CPU: math.Max(cpu, 0), Mem: mem})
		}
	}
}
