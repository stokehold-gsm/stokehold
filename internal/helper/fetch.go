package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// FetchHosts are the only hosts the helper will download from for the panel:
// game version and mod distribution points. The helper runs as root, so this
// list is deliberately short and fixed in code.
var FetchHosts = map[string]bool{
	"piston-data.mojang.com": true, "launchermeta.mojang.com": true,
	"api.papermc.io": true, "fill-data.papermc.io": true, "repo.papermc.io": true,
	"hangar.papermc.io": true, "hangarcdn.papermc.io": true,
	"meta.fabricmc.net": true, "maven.fabricmc.net": true,
	"maven.minecraftforge.net": true, "files.minecraftforge.net": true,
	"maven.neoforged.net": true,
	"api.modrinth.com":    true, "cdn.modrinth.com": true,
	"api.purpurmc.io": true,
}

var maxFetch int64 = 512 << 20 // a var so tests can lower it

const (
	maxJSON      = 4 << 20
	fetchTimeout = 15 * time.Minute
	jsonTimeout  = 30 * time.Second
	maxRedirects = 5
)

// fetchTransport is replaced in tests; nil means the hardened default.
var fetchTransport http.RoundTripper

func fetchURLOK(raw string) error {
	if len(raw) == 0 || len(raw) > 2048 {
		return errors.New("url")
	}
	u, err := url.Parse(raw)
	if err != nil || !hostAllowed(u) {
		return errors.New("url not allowed")
	}
	return nil
}

func hostAllowed(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && FetchHosts[u.Hostname()] && (u.Port() == "" || u.Port() == "443" || fetchTransport != nil)
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}

// defaultTransport refuses to connect to anything but public addresses, so a
// hostname that was repointed inwards cannot turn the helper into a proxy to
// the machine's own services. No proxy variables, no cookies.
func defaultTransport() http.RoundTripper {
	d := &net.Dialer{Timeout: 15 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !publicIP(net.ParseIP(host)) {
			return errors.New("address not allowed")
		}
		return nil
	}}
	return &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConns: 0, DisableKeepAlives: true}
}

func fetchClient(timeout time.Duration) *http.Client {
	rt := fetchTransport
	if rt == nil {
		rt = defaultTransport()
	}
	return &http.Client{Transport: rt, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		if !hostAllowed(req.URL) {
			return fmt.Errorf("redirect to %s refused", req.URL.Host)
		}
		return nil
	}}
}

func fetchGet(raw string, timeout time.Duration, max int64) (io.ReadCloser, error) {
	if err := fetchURLOK(raw); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("User-Agent", "Stokehold-helper")
	resp, err := fetchClient(timeout).Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("server answered %d", resp.StatusCode)
	}
	if resp.ContentLength > max {
		resp.Body.Close()
		cancel()
		return nil, errors.New("file too large")
	}
	return cancelCloser{resp.Body, cancel}, nil
}

type cancelCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelCloser) Close() error { err := c.ReadCloser.Close(); c.cancel(); return err }

// fetchOpen returns the body of an allowed download; Fetch caps what it reads.
func fetchOpen(raw string) (io.Reader, func(), error) {
	body, err := fetchGet(raw, fetchTimeout, maxFetch)
	if err != nil {
		return nil, nil, err
	}
	return body, func() { body.Close() }, nil
}

func fetchJSON(raw string) (string, error) {
	body, err := fetchGet(raw, jsonTimeout, maxJSON)
	if err != nil {
		return "", err
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, maxJSON+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxJSON {
		return "", errors.New("answer too large")
	}
	return string(b), nil
}
