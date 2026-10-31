package web

func init() {
	merge("ru", map[string]string{
		"st_starting":        "Запускается",
		"srv_address":        "Адрес для подключения",
		"srv_sections":       "Разделы сервера",
		"srv_overview":       "Обзор",
		"srv_logs":           "Логи",
		"srv_details_failed": "Не удалось получить данные от LinuxGSM. Показано последнее известное состояние.",
		"srv_ports":          "Порты",
		"srv_port_name":      "Назначение",
		"srv_port":           "Порт",
		"srv_proto":          "Протокол",
		"srv_listen":         "Слушает",
		"srv_hidden":         "скрыто",
		"srv_raw":            "Полный вывод LinuxGSM",
		"srv_log_console":    "Консоль",
		"srv_log_script":     "Скрипт",
		"srv_log_download":   "Скачать",
		"srv_log_missing":    "Лога пока нет:",
		"srv_log_empty":      "Лог пуст.",
	})
	merge("en", map[string]string{
		"st_starting":        "Starting",
		"srv_address":        "Address to connect",
		"srv_sections":       "Server sections",
		"srv_overview":       "Overview",
		"srv_logs":           "Logs",
		"srv_details_failed": "Could not get data from LinuxGSM. The last known state is shown.",
		"srv_ports":          "Ports",
		"srv_port_name":      "Purpose",
		"srv_port":           "Port",
		"srv_proto":          "Protocol",
		"srv_listen":         "Listening",
		"srv_hidden":         "hidden",
		"srv_raw":            "Full LinuxGSM output",
		"srv_log_console":    "Console",
		"srv_log_script":     "Script",
		"srv_log_download":   "Download",
		"srv_log_missing":    "No log yet:",
		"srv_log_empty":      "The log is empty.",
	})
}
