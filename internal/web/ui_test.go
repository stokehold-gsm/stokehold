package web

import (
	"regexp"
	"strings"
	"testing"
)

func TestScriptsAreOnlyTheOwnFileAndTheCSPSaysSo(t *testing.T) {
	r := newRig(t)
	r.setup() // without an owner /login redirects to /setup
	rec := r.do("GET", "/login", nil, nil)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") || strings.Contains(csp, "script-src *") {
		t.Errorf("CSP: %q", csp)
	}
	body := rec.Body.String()
	scripts := regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1)
	if len(scripts) != 1 || !strings.Contains(scripts[0], `src="/static/app.js"`) {
		t.Errorf("a page may carry only the one external script, got %v", scripts)
	}
	if strings.Contains(body, " onclick=") || strings.Contains(body, "javascript:") {
		t.Error("inline handlers are blocked by the CSP and must not be used")
	}
	js := r.do("GET", "/static/app.js", nil, nil)
	if js.Code != 200 || !strings.Contains(js.Header().Get("Content-Type"), "javascript") {
		t.Errorf("app.js: %d %s", js.Code, js.Header().Get("Content-Type"))
	}
}

func TestLanguageSwitchWorksAsPlainLinks(t *testing.T) {
	r := newRig(t)
	r.setup()
	body := r.do("GET", "/login?lang=ru", nil, nil).Body.String()
	for _, want := range []string{`class="lang" data-lang="ru"`, `href="?lang=ru"`, `href="?lang=en"`, `class="thumb"`} {
		if !strings.Contains(body, want) {
			t.Errorf("language switch lacks %s", want)
		}
	}
}

func TestLicensePageLeadsWithTheKeyThenComparesPlans(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	r.s.Entitlement = pro()
	body := r.do("GET", "/license?lang=en", nil, nil).Body.String()
	key, table := strings.Index(body, `name="key"`), strings.Index(body, `class="plans"`)
	if key < 0 || table < 0 || key > table {
		t.Fatalf("the key form must come first (key %d, plans %d)", key, table)
	}
	for _, want := range []string{"Free", "Pro", "Hosting", "REST API", "Several machines", "S3 backups", "soon", "Pros", "Cons"} {
		if !strings.Contains(body, want) {
			t.Errorf("plan comparison lacks %q", want)
		}
	}
	if !regexp.MustCompile(`<th class="cur">Pro<`).MatchString(body) {
		t.Error("the current plan's column must be highlighted")
	}
	for lang, banned := range map[string]string{"ru": "30 дней", "en": "30 days"} {
		if strings.Contains(r.do("GET", "/license?lang="+lang, nil, nil).Body.String(), banned) {
			t.Errorf("%s: the retention sentence must be gone", lang)
		}
	}
}

func TestPlanTableFollowsThePlans(t *testing.T) {
	cell := func(label string) []planCell {
		for _, row := range planTable("en", "free") {
			if row.Label == label {
				return row.Cells
			}
		}
		t.Fatalf("no row %q", label)
		return nil
	}
	api := cell("REST API")
	if api[0].Class != "n" || api[1].Class != "y" || api[2].Class != "y" {
		t.Errorf("API is Pro and Hosting only: %+v", api)
	}
	nodes := cell("Several machines")
	if nodes[1].Class != "n" || nodes[2].Class != "y" {
		t.Errorf("several machines are Hosting only: %+v", nodes)
	}
	if s3 := cell("S3 backups"); s3[0].Class != "n" || s3[1].Class != "soon" || s3[2].Class != "soon" {
		t.Errorf("S3 backups are planned: %+v", s3)
	}
	if !cell("Config editor")[0].Cur {
		t.Error("Free is the current column when no plan is active")
	}
}
