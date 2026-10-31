package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"stokehold/internal/helper"
	"stokehold/internal/lgsm"
)

// Dev tool for now:
//
//	stokehold scan <home>
//	stokehold call <socket> '<json request>'
func main() {
	if len(os.Args) == 4 && os.Args[1] == "call" {
		var req helper.Request
		if err := json.Unmarshal([]byte(os.Args[3]), &req); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		resp, err := helper.Call(os.Args[2], req)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		json.NewEncoder(os.Stdout).Encode(resp)
		return
	}
	if len(os.Args) != 3 || os.Args[1] != "scan" {
		fmt.Fprintln(os.Stderr, "usage: stokehold scan <home> | call <socket> <json>")
		os.Exit(2)
	}
	servers, err := lgsm.Discover(os.Args[2])
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
