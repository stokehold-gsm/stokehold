package lgsm

import "testing"

const sampleDetails = "\x1b[1m=====================================\x1b[0m\n" +
	"Distro Details\n" +
	"=====================================\n" +
	"Distro:       Ubuntu 22.04.3 LTS\n" +
	"Arch:         x86_64\n" +
	"Free Memory:  2.1GB\n" +
	"\n" +
	"=====================================\n" +
	"Server Resource\n" +
	"=====================================\n" +
	"CPU Used:  3.5%\n" +
	"Mem Used:  12.0%   520MB\n" +
	"\n" +
	"=====================================\n" +
	"Minecraft Server Details\n" +
	"=====================================\n" +
	"Server name:       LinuxGSM\n" +
	"Server IP:         203.0.113.7:25565\n" +
	"Server password:   hunter2\n" +
	"RCON password:     NOT SET\n" +
	"Players:           3/20\n" +
	"Status:            STARTED\n" +
	"\n" +
	"=====================================\n" +
	"Ports\n" +
	"=====================================\n" +
	"Change ports by editing the parameters in:\n" +
	"\n" +
	"DESCRIPTION  PORT  PROTOCOL  LISTEN\n" +
	"> Game       25565 tcp       YES\n" +
	"> Query      25565 udp       NO\n"

func TestParseDetailsGroupsRowsAndReadsStatePlayersPorts(t *testing.T) {
	d := ParseDetails(sampleDetails)
	if len(d.Sections) != 3 || d.Sections[0].Title != "Distro Details" || d.Sections[2].Title != "Minecraft Server Details" {
		t.Fatalf("sections: %+v", d.Sections)
	}
	if d.Status != "STARTED" || !d.HasPlayers || d.Players != 3 || d.Max != 20 {
		t.Fatalf("status/players: %+v", d)
	}
	if len(d.Ports) != 2 || d.Ports[0].Port != "25565" || !d.Ports[0].Listening || d.Ports[1].Listening {
		t.Fatalf("ports: %+v", d.Ports)
	}
	var pw, rcon Row
	for _, r := range d.Sections[2].Rows {
		switch r.K {
		case "Server password":
			pw = r
		case "RCON password":
			rcon = r
		}
	}
	if !pw.Secret || rcon.Secret {
		t.Errorf("a set password must be flagged secret, NOT SET must not: %+v %+v", pw, rcon)
	}
	if d.State() != "STARTED" {
		t.Errorf("state %q", d.State())
	}
}

func TestStateIsStartingUntilAGamePortListens(t *testing.T) {
	d := Details{Status: "STARTED", Ports: []Port{{Port: "25565"}, {Port: "25575"}}}
	if d.State() != "STARTING" {
		t.Errorf("a started server with no listening port is still booting, got %q", d.State())
	}
	d.Ports[1].Listening = true
	if d.State() != "STARTED" {
		t.Errorf("got %q", d.State())
	}
	if (Details{Status: "STOPPED"}).State() != "STOPPED" || (Details{}).State() != "" {
		t.Error("stopped and unknown")
	}
}

func TestParseDetailsToleratesGarbage(t *testing.T) {
	for _, s := range []string{"", "no sections here\nStatus: STARTED", "=====\n=====\n=====\n", "\x00\x01 garbage: \n"} {
		d := ParseDetails(s)
		_ = d.State()
	}
}
