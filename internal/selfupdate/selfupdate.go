// Package selfupdate finds new panel releases and stages them for the installer.
// Nothing here is trusted on its own: a staged release is accepted only when
// the SHA256SUMS file carries a valid signature of the release key and every
// file matches its hash, the same checks install.sh makes.
package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/pkg/sign"
)

const (
	Repo       = "stokehold-gsm/stokehold"
	APIBase    = "https://api.github.com"
	ReleaseURL = "https://github.com/" + Repo + "/releases/download"

	maxListing = 2 << 20
	maxBinary  = 120 << 20
	maxSmall   = 1 << 20
)

// releasePEM is the public half of the release signing key, the same as in
// install/install.sh (a test keeps the two equal).
const releasePEM = `-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAzfFMvAcZIJcizf9FQgDOzB6T1xkrC/QQAHe9mMVoh3M=
-----END PUBLIC KEY-----
`

var tagRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$`)

// ValidTag reports whether s can be a release tag; everything that reaches a
// URL or a command line goes through it.
func ValidTag(s string) bool { return len(s) <= 40 && tagRE.MatchString(s) }

type Release struct {
	Tag        string    `json:"tag"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published"`
	Notes      string    `json:"notes"`
	URL        string    `json:"url"`
}

// Latest asks GitHub for the newest published release of the channel
// ("stable" skips pre-releases, "beta" takes whatever is newest).
func Latest(ctx context.Context, c *http.Client, api, channel string) (*Release, error) {
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+Repo+"/releases?per_page=15", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "stokehold-panel")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release list: HTTP %d", resp.StatusCode)
	}
	var list []struct {
		Tag        string    `json:"tag_name"`
		Draft      bool      `json:"draft"`
		Prerelease bool      `json:"prerelease"`
		Published  time.Time `json:"published_at"`
		Body       string    `json:"body"`
		URL        string    `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxListing)).Decode(&list); err != nil {
		return nil, err
	}
	var best *Release
	for _, r := range list {
		if r.Draft || !ValidTag(r.Tag) || (channel != "beta" && r.Prerelease) {
			continue
		}
		if best == nil || Newer(best.Tag, r.Tag) {
			best = &Release{Tag: r.Tag, Prerelease: r.Prerelease, Published: r.Published, Notes: shorten(r.Body, 1500), URL: r.URL}
		}
	}
	if best == nil {
		return nil, errors.New("no release found")
	}
	return best, nil
}

func shorten(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

type ver struct {
	n   [3]int
	pre string
}

func parse(s string) (ver, bool) {
	if !ValidTag(s) {
		return ver{}, false
	}
	core, pre, _ := strings.Cut(strings.TrimPrefix(s, "v"), "-")
	var v ver
	for i, p := range strings.Split(core, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return ver{}, false
		}
		v.n[i] = n
	}
	v.pre = pre
	return v, true
}

// Newer reports whether cand is a later release than cur. A "dev" or
// unparsable current version is never updated automatically.
func Newer(cur, cand string) bool {
	a, ok1 := parse(cur)
	b, ok2 := parse(cand)
	if !ok1 || !ok2 {
		return false
	}
	for i := range a.n {
		if a.n[i] != b.n[i] {
			return b.n[i] > a.n[i]
		}
	}
	switch {
	case a.pre == b.pre:
		return false
	case b.pre == "": // a release beats its own pre-release
		return true
	case a.pre == "":
		return false
	}
	return b.pre > a.pre
}

// Stage downloads a release into dir and verifies it. base is the URL that
// holds <tag>/<file>. On success dir holds SHA256SUMS, its signature,
// install.sh and the two binaries for arch, all verified.
func Stage(ctx context.Context, c *http.Client, base, tag, arch, dir string) error {
	if !ValidTag(tag) {
		return fmt.Errorf("odd version %q", tag)
	}
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported architecture %q", arch)
	}
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Minute}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	files := []struct {
		name string
		max  int64
	}{
		{"SHA256SUMS", maxSmall}, {"SHA256SUMS.sig", 4096}, {"install.sh", maxSmall},
		{"stokehold-linux-" + arch, maxBinary}, {"stokehold-helper-linux-" + arch, maxBinary},
	}
	for _, f := range files {
		if err := fetch(ctx, c, strings.TrimRight(base, "/")+"/"+tag+"/"+f.name, filepath.Join(dir, f.name), f.max); err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
	}
	return Verify(dir, arch)
}

// Verify checks the signature of SHA256SUMS and every staged file against it.
func Verify(dir, arch string) error {
	pub, err := sign.ParsePublic([]byte(releasePEM))
	if err != nil {
		return err
	}
	return verifyWith(pub, dir, arch)
}

func verifyWith(pub ed25519.PublicKey, dir, arch string) error {
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS.sig"))
	if err != nil {
		return err
	}
	if !sign.Verify(pub, sums, sig) {
		return errors.New("signature check failed")
	}
	want := map[string]string{}
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			want[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	for _, name := range []string{"install.sh", "stokehold-linux-" + arch, "stokehold-helper-linux-" + arch} {
		h, ok := want[name]
		if !ok {
			return fmt.Errorf("SHA256SUMS does not list %s", name)
		}
		got, err := fileHash(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got != h {
			return fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	return nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fetch(ctx context.Context, c *http.Client, url, dst string, max int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "stokehold-updater")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, max+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = errors.New("file too large")
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}
