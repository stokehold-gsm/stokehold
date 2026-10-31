//go:build linux

package helper

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	maxRequest = 8 << 10
	opTimeout  = 10 * time.Minute // game installs are slow
)

func ExecRunner(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "DEBIAN_FRONTEND=noninteractive", "LANG=C.UTF-8"}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Serve accepts one JSON request per connection from root or allowUID only.
func Serve(sock string, allowUID uint32, run Runner, adopted Adopted) error {
	os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	// Owned by the panel user so only it (and root) can even connect; the
	// SO_PEERCRED check below is the second lock.
	if err := os.Chown(sock, int(allowUID), -1); err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		return err
	}
	var mu sync.Mutex // one admin, no need for parallel useradd/apt
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer c.Close()
			uid, err := peerUID(c.(*net.UnixConn))
			if err != nil || (uid != 0 && uid != allowUID) {
				log.Printf("rejected connection uid=%d err=%v", uid, err)
				return
			}
			var req Request
			c.SetReadDeadline(time.Now().Add(10 * time.Second))
			if err := json.NewDecoder(io.LimitReader(c, maxRequest)).Decode(&req); err != nil {
				json.NewEncoder(c).Encode(Response{Err: "bad json"})
				return
			}
			mu.Lock()
			resp := HandleWith(req, run, adopted)
			mu.Unlock()
			log.Printf("uid=%d op=%s user=%s ok=%v", uid, req.Op, req.User, resp.OK)
			c.SetWriteDeadline(time.Now().Add(10 * time.Second))
			json.NewEncoder(c).Encode(resp)
		}()
	}
}

func peerUID(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if cerr != nil {
		return 0, cerr
	}
	return cred.Uid, nil
}
