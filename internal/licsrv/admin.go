package licsrv

import (
	"crypto/subtle"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/pkg/auth"
	"github.com/stokehold-gsm/stokehold/pkg/license"
	"github.com/stokehold-gsm/stokehold/pkg/ratelimit"
)

// AdminUI is the web interface for the owner and for helpers who issue keys
// (ADR 0003). It sits on its own address, behind the same kind of TLS proxy
// as the panel, ideally on a separate host name with an IP allow-list.
type AdminUI struct {
	Store      *Store
	Secure     bool
	TrustProxy bool
	Now        func() time.Time

	tmpl    *template.Template
	limiter *ratelimit.Limiter
}

type apage struct {
	Admin    *Admin
	CSRF     string
	Error    string
	Info     string
	Licenses []License
	Key      string // a freshly issued key, shown exactly once
	Order    string
	Secret   string
	URI      string
	Audit    []AuditRow
	AuditBad int64
	Quota    int
	Owner    bool
	Curator  bool
	Staff    []Staff
	NewName  string
	NewRole  string
	NewPass  string
	Now      time.Time
}

func NewAdminUI(st *Store, secure, trustProxy bool) *AdminUI {
	u := &AdminUI{Store: st, Secure: secure, TrustProxy: trustProxy, Now: time.Now, limiter: ratelimit.New(10, time.Minute)}
	u.tmpl = template.Must(template.New("").Funcs(template.FuncMap{
		"date": func(ts int64) string { return time.Unix(ts, 0).UTC().Format("2006-01-02") },
		"role": func(r string) string {
			return map[string]string{RoleOwner: "владелец", RoleCurator: "куратор шопа", RoleOperator: "оператор шопа"}[r]
		},
	}).Parse(adminTemplates))
	return u
}

const adminCookie = "sh_admin"

func (u *AdminUI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) { u.render(w, "login", &apage{}, 200) })
	mux.HandleFunc("POST /login", u.login)
	mux.HandleFunc("POST /logout", u.auth(u.logout, false))
	mux.HandleFunc("GET /{$}", u.auth(u.list, true))
	mux.HandleFunc("GET /issue", u.auth(u.issueForm, true))
	mux.HandleFunc("POST /issue", u.auth(u.issue, true))
	mux.HandleFunc("POST /license/{action}", u.auth(u.change, true))
	mux.HandleFunc("GET /audit", u.auth(u.auditPage, true))
	mux.HandleFunc("GET /staff", u.auth(u.staffPage, true))
	mux.HandleFunc("POST /staff", u.auth(u.staffAdd, true))
	mux.HandleFunc("POST /staff/reset-2fa", u.auth(u.staffReset2FA, true))
	mux.HandleFunc("GET /security", u.auth(u.security, false))
	mux.HandleFunc("POST /security/start", u.auth(u.twofaStart, false))
	mux.HandleFunc("POST /security/enable", u.auth(u.twofaEnable, false))
	return u.protect(mux)
}

func (u *AdminUI) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		if r.Method == http.MethodPost && !sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		p, err := url.Parse(o)
		return err == nil && p.Host == r.Host
	}
	if f := r.Header.Get("Sec-Fetch-Site"); f != "" {
		return f == "same-origin" || f == "none"
	}
	return true
}

type ahandler func(w http.ResponseWriter, r *http.Request, p *apage)

// auth resolves the session and checks CSRF on posts. With needTOTP, an admin
// who has not turned on two-factor yet is sent to the security page first.
func (u *AdminUI) auth(h ahandler, needTOTP bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(adminCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		a, csrf, err := u.Store.AdminBySession(c.Value)
		if err != nil {
			u.clear(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost && subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(csrf)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if needTOTP && !a.TOTPEnabled {
			http.Redirect(w, r, "/security", http.StatusSeeOther)
			return
		}
		h(w, r, &apage{Admin: a, CSRF: csrf, Owner: a.Role == RoleOwner, Curator: a.Role == RoleCurator, Now: u.Now()})
	}
}

func (u *AdminUI) render(w http.ResponseWriter, name string, p *apage, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := u.tmpl.ExecuteTemplate(w, name, p); err != nil {
		log.Printf("admin render %s: %v", name, err)
	}
}

func (u *AdminUI) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: token, Path: "/", HttpOnly: true, Secure: u.Secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(AdminSessionTTL.Seconds())})
}

func (u *AdminUI) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Path: "/", HttpOnly: true, Secure: u.Secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func (u *AdminUI) clientIP(r *http.Request) string {
	if u.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (u *AdminUI) login(w http.ResponseWriter, r *http.Request) {
	p := &apage{}
	if !u.limiter.Allow(u.clientIP(r), u.Now()) {
		p.Error = "Слишком много попыток. Попробуйте позже."
		u.render(w, "login", p, http.StatusTooManyRequests)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	a, err := u.Store.AdminLogin(name, r.PostFormValue("password"))
	if err == nil && a.TOTPEnabled {
		step, ok := auth.VerifyTOTP(a.TOTPSecret, r.PostFormValue("code"), u.Now())
		if !ok || !u.Store.AdminUseTOTPStep(a.ID, step) {
			err = ErrNotFound
		}
	}
	if err != nil {
		u.Store.AdminAudit("-", "login.fail", name)
		p.Error = "Неверное имя, пароль или код."
		if errors.Is(err, ErrLocked) {
			p.Error = "Учётная запись временно заблокирована."
		}
		u.render(w, "login", p, http.StatusUnauthorized)
		return
	}
	token, _, err := u.Store.AdminSession(a.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	u.setCookie(w, token)
	u.Store.AdminAudit(a.Name, "login.ok", "")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (u *AdminUI) logout(w http.ResponseWriter, r *http.Request, p *apage) {
	if c, err := r.Cookie(adminCookie); err == nil {
		u.Store.AdminDeleteSession(c.Value)
	}
	u.clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (u *AdminUI) list(w http.ResponseWriter, r *http.Request, p *apage) {
	var all []License
	if p.Owner || p.Curator {
		all, _ = u.Store.List()
		p.Staff, _ = u.Store.StaffList()
	} else {
		all, _ = u.Store.ListIssuedBy(p.Admin.Name)
		p.Quota = u.quotaLeft(p)
	}
	if len(all) > 200 {
		all = all[:200]
	}
	p.Licenses = all
	u.render(w, "list", p, http.StatusOK)
}

func (u *AdminUI) quotaLeft(p *apage) int {
	if p.Owner {
		return -1
	}
	return IssuerDailyMax - u.Store.IssuedSince(p.Admin.Name, u.Now().Add(-24*time.Hour))
}

func (u *AdminUI) issueForm(w http.ResponseWriter, r *http.Request, p *apage) {
	if p.Curator {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p.Quota = u.quotaLeft(p)
	u.render(w, "issue", p, http.StatusOK)
}

// issue creates a licence for one order. The key is shown once on the result
// page and is not stored anywhere in readable form.
func (u *AdminUI) issue(w http.ResponseWriter, r *http.Request, p *apage) {
	if p.Curator {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	order := strings.TrimSpace(r.PostFormValue("order"))
	days, e2 := strconv.Atoi(r.PostFormValue("days"))
	plan := r.PostFormValue("plan")
	fail := func(msg string, status int) {
		p.Error, p.Order, p.Quota = msg, order, u.quotaLeft(p)
		u.render(w, "issue", p, status)
	}
	if order == "" || len(order) > 80 || e2 != nil || !license.KnownPlan(plan) || days < 1 || days > 3660 {
		fail("Проверьте поля: номер заказа, тариф и срок в днях (1-3660).", http.StatusBadRequest)
		return
	}
	key, err := u.Store.IssueAs(p.Admin, order, plan, days, strings.TrimSpace(r.PostFormValue("contact")))
	switch {
	case errors.Is(err, ErrExists):
		fail("На этот заказ ключ уже выдан. Второй ключ на один заказ выдать нельзя.", http.StatusConflict)
		return
	case errors.Is(err, ErrQuota):
		fail("Суточный лимит выдачи исчерпан. Обратитесь к владельцу.", http.StatusTooManyRequests)
		return
	case err != nil:
		fail("Не удалось выдать ключ.", http.StatusInternalServerError)
		return
	}
	p.Key, p.Order = key, order
	u.render(w, "issued", p, http.StatusOK)
}

// change is for support: the curator may extend a licence or free it from its
// instance, only the owner may revoke it.
func (u *AdminUI) change(w http.ResponseWriter, r *http.Request, p *apage) {
	if !p.Owner && !(p.Curator && r.PathValue("action") != "revoke") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	order := strings.TrimSpace(r.PostFormValue("order"))
	var err error
	switch r.PathValue("action") {
	case "revoke":
		err = u.Store.Revoke(p.Admin.Name, order)
	case "reset":
		err = u.Store.ResetBinding(p.Admin.Name, order)
	case "extend":
		days, e := strconv.Atoi(r.PostFormValue("days"))
		if e != nil || days < 1 || days > 3660 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var l *License
		if l, err = u.Store.ByOrder(order); err == nil {
			base := time.Unix(l.ExpiresAt, 0)
			if base.Before(u.Now()) {
				base = u.Now()
			}
			err = u.Store.Extend(p.Admin.Name, order, base.AddDate(0, 0, days))
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (u *AdminUI) auditPage(w http.ResponseWriter, r *http.Request, p *apage) {
	if !p.Owner && !p.Curator {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p.Audit, _ = u.Store.Audit(200)
	p.AuditBad, _ = u.Store.VerifyAudit()
	u.render(w, "audit", p, http.StatusOK)
}

func (u *AdminUI) security(w http.ResponseWriter, r *http.Request, p *apage) {
	if !p.Admin.TOTPEnabled && p.Admin.TOTPSecret != "" {
		p.Secret = p.Admin.TOTPSecret
		p.URI = auth.TOTPURI(p.Secret, p.Admin.Name, "Stokehold licences")
	}
	u.render(w, "security", p, http.StatusOK)
}

func (u *AdminUI) twofaStart(w http.ResponseWriter, r *http.Request, p *apage) {
	if !p.Admin.TOTPEnabled {
		u.Store.AdminSetTOTP(p.Admin.ID, auth.NewTOTPSecret(), false)
	}
	http.Redirect(w, r, "/security", http.StatusSeeOther)
}

func (u *AdminUI) twofaEnable(w http.ResponseWriter, r *http.Request, p *apage) {
	if step, ok := auth.VerifyTOTP(p.Admin.TOTPSecret, r.PostFormValue("code"), u.Now()); ok && p.Admin.TOTPSecret != "" && u.Store.AdminUseTOTPStep(p.Admin.ID, step) {
		u.Store.AdminSetTOTP(p.Admin.ID, p.Admin.TOTPSecret, true)
		u.Store.AdminAudit(p.Admin.Name, "2fa.enable", "")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	p.Error, p.Secret = "Код не подошёл.", p.Admin.TOTPSecret
	p.URI = auth.TOTPURI(p.Secret, p.Admin.Name, "Stokehold licences")
	u.render(w, "security", p, http.StatusBadRequest)
}

const adminTemplates = `
{{define "head"}}<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Stokehold: лицензии</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:860px;margin:0 auto;padding:16px;color:#1d2127;background:#f6f7f9}
@media(prefers-color-scheme:dark){body{color:#e6e8eb;background:#14161c}.c{background:#1c1f27!important;border-color:#2c313c!important}input,select{color:inherit}}
nav{display:flex;gap:14px;align-items:center;margin-bottom:14px}nav a{color:inherit}.c{background:#fff;border:1px solid #dde1e6;border-radius:10px;padding:16px;margin-bottom:14px}
table{width:100%;border-collapse:collapse;font-size:.92rem}td,th{text-align:left;padding:6px;border-bottom:1px solid #8884}
input,select{width:100%;padding:8px;border:1px solid #8886;border-radius:6px;background:transparent;font:inherit}label{display:block;margin:10px 0 3px;font-size:.88rem;opacity:.8}
button{padding:8px 14px;border:0;border-radius:6px;background:#e8451a;color:#fff;font:inherit;cursor:pointer;margin-top:12px}button.l{background:none;color:inherit;text-decoration:underline;margin:0;padding:0}
.e{color:#c0392b}.k{font:1.15rem ui-monospace,monospace;word-break:break-all;padding:12px;border:2px dashed #e8451a;border-radius:8px}form.i{display:inline}</style></head><body>
<nav><b>Stokehold: лицензии</b>{{if .Admin}}<a href="/">{{if or .Owner .Curator}}Лицензии{{else}}Мои выдачи{{end}}</a>{{if not .Curator}}<a href="/issue">Выдать</a>{{end}}{{if or .Owner .Curator}}<a href="/audit">Журнал</a>{{end}}{{if .Owner}}<a href="/staff">Сотрудники</a>{{end}}<a href="/security">Безопасность</a>
<span>{{.Admin.Name}} ({{role .Admin.Role}})</span>
<form class="i" method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="l" type="submit">Выйти</button></form>{{end}}</nav>{{end}}
{{define "foot"}}</body></html>{{end}}

{{define "login"}}{{template "head" .}}<div class="c"><h2>Вход</h2>{{if .Error}}<p class="e">{{.Error}}</p>{{end}}
<form method="post" action="/login"><label for="n">Имя</label><input id="n" name="name" required autofocus>
<label for="p">Пароль</label><input id="p" name="password" type="password" required>
<label for="c">Код 2FA</label><input id="c" name="code" inputmode="numeric" maxlength="6" autocomplete="one-time-code">
<button type="submit">Войти</button></form></div>{{template "foot" .}}{{end}}

{{define "list"}}{{template "head" .}}
{{if or .Owner .Curator}}<div class="c"><h2>Сотрудники шопа</h2>
<table><tr><th>Имя</th><th>Роль</th><th>2FA</th><th>Выдано за 24 ч</th><th>Выдано всего</th></tr>
{{range .Staff}}<tr><td>{{.Name}}</td><td>{{role .Role}}</td><td>{{if .TOTPEnabled}}да{{else}}нет{{end}}</td><td>{{.Issued24h}}</td><td>{{.IssuedTotal}}</td></tr>{{end}}</table></div>
{{else}}<div class="c"><h2>Ваш лимит</h2>{{if ge .Quota 0}}<p>Осталось выдач за 24 часа: {{.Quota}} из 20.</p>{{end}}<p><a href="/issue">Выдать ключ на оплаченный заказ</a></p></div>{{end}}
<div class="c"><h2>{{if or .Owner .Curator}}Лицензии{{else}}Мои выдачи{{end}}</h2>
<table><tr><th>Заказ</th><th>Тариф</th><th>Функций</th><th>До</th><th>Статус</th><th>Привязка</th><th>Контакт</th>{{if or .Owner .Curator}}<th></th>{{end}}</tr>
{{range $l := .Licenses}}<tr><td>{{$l.OrderRef}}</td><td>{{$l.Plan}}</td><td>{{len $l.Features}}</td><td>{{date $l.ExpiresAt}}</td><td>{{$l.Status}}</td><td>{{if $l.InstancePub}}да{{else}}нет{{end}}</td><td>{{$l.Contact}}</td>
{{if or $.Owner $.Curator}}<td>{{if eq $l.Status "active"}}
{{if $.Owner}}<form class="i" method="post" action="/license/revoke"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="order" value="{{$l.OrderRef}}"><button class="l" type="submit">отозвать</button></form>{{end}}
<form class="i" method="post" action="/license/reset"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="order" value="{{$l.OrderRef}}"><button class="l" type="submit">сбросить привязку</button></form>
<form class="i" method="post" action="/license/extend"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="order" value="{{$l.OrderRef}}"><input type="hidden" name="days" value="30"><button class="l" type="submit">+30 дн.</button></form>{{end}}</td>{{end}}</tr>
{{else}}<tr><td colspan="8">Лицензий пока нет.</td></tr>{{end}}</table></div>{{template "foot" .}}{{end}}

{{define "staff"}}{{template "head" .}}<div class="c"><h2>Сотрудники шопа</h2>
<p>Оператор выдаёт ключи на оплаченные заказы (до 20 за сутки) и видит только свои выдачи. Куратор видит всех и все выдачи, продлевает, сбрасывает привязку и читает журнал, но ключи не выдаёт и не отзывает. Отзыв только у владельца. Каждому при первом входе придётся включить 2FA.</p>
{{if .NewPass}}<p>Создан <b>{{.NewName}}</b> ({{role .NewRole}}). Одноразовый пароль, показан один раз:</p><p class="k">{{.NewPass}}</p><p>Передайте его лично, не в публичном канале.</p>{{end}}
<table><tr><th>Имя</th><th>Роль</th><th>2FA</th><th>За 24 ч</th><th>Всего</th><th></th></tr>
{{range $m := .Staff}}<tr><td>{{$m.Name}}</td><td>{{role $m.Role}}</td><td>{{if $m.TOTPEnabled}}да{{else}}нет{{end}}</td><td>{{$m.Issued24h}}</td><td>{{$m.IssuedTotal}}</td>
<td>{{if ne $m.Role "owner"}}<form class="i" method="post" action="/staff/reset-2fa"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="name" value="{{$m.Name}}"><button class="l" type="submit">сбросить 2FA</button></form>{{end}}</td></tr>{{end}}</table></div>
<div class="c"><h2>Добавить</h2>{{if .Error}}<p class="e">{{.Error}}</p>{{end}}
<form method="post" action="/staff"><input type="hidden" name="csrf" value="{{.CSRF}}">
<label for="n">Имя для входа</label><input id="n" name="name" value="{{.NewName}}" required maxlength="32">
<label for="r">Роль</label><select id="r" name="role"><option value="operator">Оператор шопа</option><option value="curator">Куратор шопа</option></select>
<button type="submit">Создать</button></form></div>{{template "foot" .}}{{end}}

{{define "issue"}}{{template "head" .}}<div class="c"><h2>Выдать лицензию</h2>
{{if ge .Quota 0}}<p>Осталось выдач за 24 часа: {{.Quota}}.</p>{{end}}
<p>Перед выдачей проверьте, что заказ оплачен. Один заказ даёт один ключ.</p>
{{if .Error}}<p class="e">{{.Error}}</p>{{end}}
<form method="post" action="/issue"><input type="hidden" name="csrf" value="{{.CSRF}}">
<label for="o">Номер заказа (Boosty или FunPay)</label><input id="o" name="order" value="{{.Order}}" required maxlength="80">
<label for="s">Тариф</label><select id="s" name="plan"><option value="pro">Pro</option><option value="hosting">Hosting</option></select>
<label for="d">Срок, дней</label><input id="d" name="days" value="365" required inputmode="numeric">
<label for="k">Контакт покупателя (ник или почта, по желанию)</label><input id="k" name="contact" maxlength="120">
<button type="submit">Выдать</button></form></div>{{template "foot" .}}{{end}}

{{define "issued"}}{{template "head" .}}<div class="c"><h2>Ключ для заказа {{.Order}}</h2>
<p class="k">{{.Key}}</p><p>Скопируйте и отправьте покупателю. <b>Ключ показывается один раз</b> и нигде не хранится в читаемом виде. Не отправляйте его в публичные каналы.</p>
<p><a href="/">К списку</a></p></div>{{template "foot" .}}{{end}}

{{define "audit"}}{{template "head" .}}<div class="c"><h2>Журнал</h2>
{{if .AuditBad}}<p class="e">Цепочка журнала нарушена на записи {{.AuditBad}}: журнал изменяли.</p>{{else}}<p>Цепочка журнала цела.</p>{{end}}
<table><tr><th>Время (UTC)</th><th>Кто</th><th>Действие</th><th>Детали</th></tr>
{{range .Audit}}<tr><td>{{date .TS}}</td><td>{{.Actor}}</td><td>{{.Action}}</td><td>{{.Detail}}</td></tr>{{end}}</table></div>{{template "foot" .}}{{end}}

{{define "security"}}{{template "head" .}}<div class="c"><h2>Двухфакторная защита</h2>
{{if .Error}}<p class="e">{{.Error}}</p>{{end}}
{{if .Admin.TOTPEnabled}}<p>Включена. Отключить её может только владелец из командной строки.</p>
{{else if .Secret}}<p>Секрет для приложения-аутентификатора:</p><p class="k">{{.Secret}}</p><p>Ссылка otpauth: <code style="word-break:break-all">{{.URI}}</code></p>
<form method="post" action="/security/enable"><input type="hidden" name="csrf" value="{{.CSRF}}"><label for="c">Код из приложения</label><input id="c" name="code" inputmode="numeric" maxlength="6" required><button type="submit">Включить</button></form>
{{else}}<p>Без двухфакторной защиты работа с лицензиями закрыта. Включите её.</p>
<form method="post" action="/security/start"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Начать настройку</button></form>{{end}}
</div>{{template "foot" .}}{{end}}
`

func (u *AdminUI) staffPage(w http.ResponseWriter, r *http.Request, p *apage) {
	if !p.Owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p.Staff, _ = u.Store.StaffList()
	u.render(w, "staff", p, http.StatusOK)
}

// staffAdd creates an operator or curator with a one-time password that is
// shown once; their first sign-in forces two-factor.
func (u *AdminUI) staffAdd(w http.ResponseWriter, r *http.Request, p *apage) {
	if !p.Owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name, role := strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("role")
	fail := func(msg string, status int) {
		p.Error, p.NewName, p.NewRole = msg, name, role
		p.Staff, _ = u.Store.StaffList()
		u.render(w, "staff", p, status)
	}
	if !validStaffName.MatchString(name) || (role != RoleOperator && role != RoleCurator) {
		fail("Имя: 2-32 символа, буквы, цифры, точка, дефис, подчёркивание. Роль: оператор или куратор.", http.StatusBadRequest)
		return
	}
	pw, _ := auth.NewToken()
	pw = pw[:20]
	switch err := u.Store.AdminCreate(name, pw, role); {
	case errors.Is(err, ErrAdminExists):
		fail("Сотрудник с таким именем уже есть.", http.StatusConflict)
		return
	case err != nil:
		fail("Не удалось создать сотрудника.", http.StatusInternalServerError)
		return
	}
	u.Store.AdminAudit(p.Admin.Name, "admin.add", name+" "+role)
	p.NewName, p.NewRole, p.NewPass = name, role, pw
	p.Staff, _ = u.Store.StaffList()
	u.render(w, "staff", p, http.StatusOK)
}

// staffReset2FA is for a colleague who lost their authenticator.
func (u *AdminUI) staffReset2FA(w http.ResponseWriter, r *http.Request, p *apage) {
	if !p.Owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if name := strings.TrimSpace(r.PostFormValue("name")); name != p.Admin.Name && u.Store.AdminResetTOTP(name) != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/staff", http.StatusSeeOther)
}

var validStaffName = regexp.MustCompile(`^[A-Za-z0-9._-]{2,32}$`)
