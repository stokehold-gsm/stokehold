package web

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"github.com/stokehold-gsm/stokehold/internal/helper"
)

const detailsOut = "=====\nGame Server Details\n=====\nServer IP:  203.0.113.7:34197\nServer password:  hunter2\nPlayers:  2/10\nStatus:  STARTED\n\n=====\nPorts\n=====\nDESCRIPTION PORT PROTOCOL LISTEN\n> Game       34197 udp       YES\n"

func srvRig(t *testing.T) (*rig, int64) {
	t.Helper()
	r := newRig(t)
	r.setup()
	r.login("")
	id := r.addServerRow()
	r.s.Helper = func(req helper.Request) (helper.Response, error) {
		switch req.Op {
		case "run":
			return helper.Response{OK: true, Out: detailsOut}, nil
		case "stat":
			return helper.Response{OK: true, Out: `{"n":"x","t":"f","s":23}`}, nil
		case "get":
			return helper.Response{OK: true, Out: base64.StdEncoding.EncodeToString([]byte("[ 12:00 ] server started\n\x1b[31mred\x1b[0m line\n"))}, nil
		}
		return helper.Response{}, nil
	}
	return r, id
}

func TestServerPageShowsParsedStateAddressPortsAndMasksSecrets(t *testing.T) {
	r, id := srvRig(t)
	page := r.do("GET", "/servers/"+strconv.FormatInt(id, 10), nil, nil).Body.String()
	for _, want := range []string{"st-started", "203.0.113.7:34197", "34197", "Game Server Details", "2/10"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	// the password appears only in the raw-output spoiler, never as a fact
	if strings.Count(page, "hunter2") != 1 || !strings.Contains(page, "<details class=\"card raw\">") {
		t.Errorf("a password must be masked in the facts (seen %d times)", strings.Count(page, "hunter2"))
	}
}

func TestServerPageLogsTailIsCleanedAndOnlyForControllers(t *testing.T) {
	r, id := srvRig(t)
	logs := r.do("GET", "/servers/"+strconv.FormatInt(id, 10)+"?tab=logs", nil, nil).Body.String()
	if !strings.Contains(logs, "server started") || strings.Contains(logs, "\x1b[") || !strings.Contains(logs, "red line") {
		t.Fatalf("the log must show cleaned text:\n%s", logs)
	}
	tok := r.csrf()
	code, pw := r.createUser(tok, "view@example.com", "viewer")
	if code != 200 && code != 303 {
		t.Fatalf("create viewer: %d", code)
	}
	r.loginAs("view@example.com", pw)
	page := r.do("GET", "/servers/"+strconv.FormatInt(id, 10)+"?tab=logs", nil, nil).Body.String()
	if strings.Contains(page, "server started") || strings.Contains(page, `cmd" value="stop`) {
		t.Error("a viewer must see neither logs nor control buttons")
	}
}

func TestServerPageUnknownServerIs404(t *testing.T) {
	r, _ := srvRig(t)
	if rec := r.do("GET", "/servers/9999", nil, nil); rec.Code != 404 {
		t.Errorf("%d", rec.Code)
	}
}
