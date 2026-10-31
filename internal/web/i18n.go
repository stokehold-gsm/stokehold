package web

import (
	"net/http"
	"strings"
)

var messages = map[string]map[string]string{
	"ru": {
		"app":              "Stokehold GSM",
		"login":            "Вход",
		"email":            "Почта",
		"password":         "Пароль",
		"code":             "Код 2FA (если включена)",
		"signin":           "Войти",
		"signout":          "Выйти",
		"bad_login":        "Неверная почта, пароль или код.",
		"locked":           "Слишком много попыток. Попробуйте позже.",
		"setup":            "Первая настройка",
		"setup_hint":       "Создайте владельца панели. Код настройки напечатан в журнале службы при запуске.",
		"setup_token":      "Код настройки",
		"create_owner":     "Создать владельца",
		"bad_setup":        "Неверный код настройки.",
		"weak_password":    "Пароль слишком короткий (минимум 10 символов).",
		"servers":          "Серверы",
		"no_servers":       "Серверов пока нет.",
		"name":             "Название",
		"game":             "Игра",
		"mode":             "Режим",
		"security":         "Безопасность",
		"twofa":            "Двухфакторная защита",
		"twofa_on":         "Включена",
		"twofa_off":        "Выключена",
		"twofa_secret":     "Секрет для приложения-аутентификатора",
		"twofa_uri":        "Ссылка otpauth",
		"twofa_confirm":    "Введите код из приложения, чтобы включить защиту",
		"twofa_enable":     "Включить",
		"twofa_bad":        "Код не подошёл.",
		"twofa_start":      "Начать настройку",
		"twofa_disable":    "Выключить",
		"role":             "Роль",
		"back":             "Назад",
		"language":         "Язык",
		"not_found":        "Страница не найдена.",
		"forbidden":        "Запрос отклонён.",
		"wrong_password":   "Неверный пароль.",
		"act_details":      "Статус",
		"act_start":        "Запустить",
		"act_stop":         "Остановить",
		"act_restart":      "Перезапустить",
		"act_backup":       "Бэкап",
		"act_check-update": "Проверить обновление",
		"add_server":       "Добавить сервер",
		"linux_user":       "Пользователь Linux",
		"user_hint":        "Для существующих серверов LinuxGSM владелец сначала вносит пользователя в /etc/stokehold/adopt.conf на сервере. Пользователи gs_* доступны сразу.",
		"find":             "Найти серверы",
		"add":              "Добавить",
		"find_failed":      "Не удалось найти серверы этого пользователя. Проверьте имя и список adopt.conf.",
		"nothing_found":    "У этого пользователя нет серверов LinuxGSM.",
		"server_exists":    "Сервер с таким названием уже есть.",
		"helper_down":      "Служебный процесс недоступен. Проверьте службу stokehold-helper.",
		"action_failed":    "Команда завершилась с ошибкой.",
		"result":           "Результат",
	},
	"en": {
		"app":              "Stokehold GSM",
		"login":            "Sign in",
		"email":            "Email",
		"password":         "Password",
		"code":             "2FA code (if enabled)",
		"signin":           "Sign in",
		"signout":          "Sign out",
		"bad_login":        "Wrong email, password or code.",
		"locked":           "Too many attempts. Try again later.",
		"setup":            "First-time setup",
		"setup_hint":       "Create the panel owner. The setup code was printed in the service log at startup.",
		"setup_token":      "Setup code",
		"create_owner":     "Create owner",
		"bad_setup":        "Wrong setup code.",
		"weak_password":    "Password is too short (10 characters minimum).",
		"servers":          "Servers",
		"no_servers":       "No servers yet.",
		"name":             "Name",
		"game":             "Game",
		"mode":             "Mode",
		"security":         "Security",
		"twofa":            "Two-factor authentication",
		"twofa_on":         "Enabled",
		"twofa_off":        "Disabled",
		"twofa_secret":     "Secret for your authenticator app",
		"twofa_uri":        "otpauth link",
		"twofa_confirm":    "Enter a code from the app to turn protection on",
		"twofa_enable":     "Enable",
		"twofa_bad":        "That code did not match.",
		"twofa_start":      "Start setup",
		"twofa_disable":    "Disable",
		"role":             "Role",
		"back":             "Back",
		"language":         "Language",
		"not_found":        "Page not found.",
		"forbidden":        "Request rejected.",
		"wrong_password":   "Wrong password.",
		"act_details":      "Status",
		"act_start":        "Start",
		"act_stop":         "Stop",
		"act_restart":      "Restart",
		"act_backup":       "Backup",
		"act_check-update": "Check for update",
		"add_server":       "Add server",
		"linux_user":       "Linux user",
		"user_hint":        "For existing LinuxGSM servers the owner first lists the user in /etc/stokehold/adopt.conf on the machine. gs_* users are available at once.",
		"find":             "Find servers",
		"add":              "Add",
		"find_failed":      "Could not find servers for that user. Check the name and adopt.conf.",
		"nothing_found":    "That user has no LinuxGSM servers.",
		"server_exists":    "A server with that name already exists.",
		"helper_down":      "The privileged helper is not reachable. Check the stokehold-helper service.",
		"action_failed":    "The command failed.",
		"result":           "Result",
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
