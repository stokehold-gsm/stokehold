package web

func init() {
	merge("ru", map[string]string{
		"install_server":   "Установить новый сервер",
		"install_hint":     "Панель создаст отдельного пользователя gs_..., поставит пакеты, которые LinuxGSM указывает для этой игры, и установит сервер. Скачивание большой игры может занять много минут.",
		"install_game":     "Игра",
		"install_name":     "Название сервера",
		"install_start":    "Установить",
		"install_bad_game": "Такой игры нет в каталоге LinuxGSM.",
		"install_bad_name": "Название слишком длинное (до 60 знаков).",
		"install_busy":     "Уже идёт другая установка. Дождитесь её окончания.",
		"job_title":        "Установка",
		"job_running":      "Идёт установка. Страница обновляется сама.",
		"job_done":         "Готово.",
		"job_failed":       "Установка не удалась. Причина в журнале ниже; исправив её, запустите установку заново с другим названием.",
	})
	merge("en", map[string]string{
		"install_server":   "Install a new server",
		"install_hint":     "The panel creates a separate gs_... user, installs the packages LinuxGSM lists for the game, and installs the server. Downloading a large game can take many minutes.",
		"install_game":     "Game",
		"install_name":     "Server name",
		"install_start":    "Install",
		"install_bad_game": "That game is not in the LinuxGSM catalogue.",
		"install_bad_name": "The name is too long (60 characters at most).",
		"install_busy":     "Another install is already running. Wait for it to finish.",
		"job_title":        "Install",
		"job_running":      "Installing. This page refreshes by itself.",
		"job_done":         "Done.",
		"job_failed":       "The install failed. The reason is in the log below; after fixing it, start again with a different name.",
	})
}
