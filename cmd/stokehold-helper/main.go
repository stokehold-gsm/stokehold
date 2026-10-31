//go:build linux

package main

import (
	"flag"
	"log"

	"stokehold/internal/helper"
)

func main() {
	sock := flag.String("socket", "/run/stokehold/helper.sock", "unix socket path")
	uid := flag.Uint("uid", 0, "uid of the panel user allowed to connect (root is always allowed)")
	adopt := flag.String("adopt-file", "/etc/stokehold/adopt.conf", "root-owned list of existing Linux accounts the panel may manage, one per line")
	flag.Parse()
	log.Fatal(helper.Serve(*sock, uint32(*uid), helper.ExecRunner, helper.AdoptedFromFile(*adopt)))
}
