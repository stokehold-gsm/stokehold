package web

import (
	"net/http"
	"strings"
)

var messages = map[string]map[string]string{
	"ru": {
		"app":            "Stokehold GSM",
		"login":          "Вход",
		"email":          "Почта",
		"password":       "Пароль",
		"code":           "Код 2FA (если включена)",
		"signin":         "Войти",
		"signout":        "Выйти",
		"bad_login":      "Неверная почта, пароль или код.",
		"locked":         "Слишком много попыток. Попробуйте позже.",
		"setup":          "Первая настройка",
		"setup_hint":     "Создайте владельца панели. Код настройки напечатан в журнале службы при запуске.",
		"setup_token":    "Код настройки",
		"create_owner":   "Создать владельца",
		"bad_setup":      "Неверный код настройки.",
		"weak_password":  "Пароль слишком короткий (минимум 10 символов).",
		"servers":        "Серверы",
		"no_servers":     "Серверов пока нет. Подхват существующих серверов и установка новых появятся в следующих версиях.",
		"name":           "Название",
		"game":           "Игра",
		"mode":           "Режим",
		"security":       "Безопасность",
		"twofa":          "Двухфакторная защита",
		"twofa_on":       "Включена.",
		"twofa_off":      "Выключена. Рекомендуем включить.",
		"twofa_secret":   "Секрет для приложения-аутентификатора",
		"twofa_uri":      "Ссылка otpauth",
		"twofa_confirm":  "Введите код из приложения, чтобы включить защиту",
		"twofa_enable":   "Включить",
		"twofa_bad":      "Код не подошёл.",
		"twofa_start":    "Начать настройку",
		"twofa_disable":  "Выключить",
		"role":           "Роль",
		"back":           "Назад",
		"language":       "Язык",
		"not_found":      "Страница не найдена.",
		"forbidden":      "Запрос отклонён.",
		"wrong_password": "Неверный пароль.",
	},
	"en": {
		"app":            "Stokehold GSM",
		"login":          "Sign in",
		"email":          "Email",
		"password":       "Password",
		"code":           "2FA code (if enabled)",
		"signin":         "Sign in",
		"signout":        "Sign out",
		"bad_login":      "Wrong email, password or code.",
		"locked":         "Too many attempts. Try again later.",
		"setup":          "First-time setup",
		"setup_hint":     "Create the panel owner. The setup code was printed in the service log at startup.",
		"setup_token":    "Setup code",
		"create_owner":   "Create owner",
		"bad_setup":      "Wrong setup code.",
		"weak_password":  "Password is too short (10 characters minimum).",
		"servers":        "Servers",
		"no_servers":     "No servers yet. Adopting existing servers and installing new ones will come in later versions.",
		"name":           "Name",
		"game":           "Game",
		"mode":           "Mode",
		"security":       "Security",
		"twofa":          "Two-factor authentication",
		"twofa_on":       "Enabled.",
		"twofa_off":      "Disabled. We recommend enabling it.",
		"twofa_secret":   "Secret for your authenticator app",
		"twofa_uri":      "otpauth link",
		"twofa_confirm":  "Enter a code from the app to turn protection on",
		"twofa_enable":   "Enable",
		"twofa_bad":      "That code did not match.",
		"twofa_start":    "Start setup",
		"twofa_disable":  "Disable",
		"role":           "Role",
		"back":           "Back",
		"language":       "Language",
		"not_found":      "Page not found.",
		"forbidden":      "Request rejected.",
		"wrong_password": "Wrong password.",
	},
}

// lang picks the UI language: ?lang= wins (and is remembered), then the
// cookie, then Accept-Language. Anything unknown falls back to English.
func lang(w http.ResponseWriter, r *http.Request) string {
	if l := r.URL.Query().Get("lang"); l == "ru" || l == "en" {
		http.SetCookie(w, &http.Cookie{Name: "lang", Value: l, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode})
		return l
	}
	if c, err := r.Cookie("lang"); err == nil && (c.Value == "ru" || c.Value == "en") {
		return c.Value
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language"))), "ru") {
		return "ru"
	}
	return "en"
}

func tr(l, key string) string {
	if v, ok := messages[l][key]; ok {
		return v
	}
	return messages["en"][key]
}
