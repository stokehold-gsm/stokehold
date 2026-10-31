//go:build !pro

package web

import (
	"strings"
	"testing"
)

func TestFreeEditionHasNoProPages(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	for _, p := range []string{"/nodes", "/tokens", "/integrations", "/status", "/api/v1/servers", "/api/v1/metrics"} {
		if rec := r.do("GET", p, nil, nil); rec.Code != 404 && rec.Code != 405 {
			t.Errorf("%s answered %d in the Free edition", p, rec.Code)
		}
	}
	body := r.do("GET", "/monitor", nil, nil).Body.String()
	if !strings.Contains(body, "/monitor") || strings.Contains(body, `href="/tokens"`) || strings.Contains(body, `href="/nodes"`) {
		t.Fatalf("navigation does not match the Free edition:\n%s", body)
	}
}
