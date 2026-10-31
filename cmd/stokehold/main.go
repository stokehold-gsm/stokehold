package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"stokehold/internal/lgsm"
)

// Dev tool for now: stokehold scan <home>
func main() {
	if len(os.Args) != 3 || os.Args[1] != "scan" {
		fmt.Fprintln(os.Stderr, "usage: stokehold scan <home>")
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
