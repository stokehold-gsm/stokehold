package web

// Strings of the licence page. Kept apart from i18n.go so the two files can be
// read on their own.
func init() {
	merge("ru", map[string]string{
		"license":            "Лицензия",
		"lic_plan":           "Тариф",
		"lic_free":           "Free (бесплатный)",
		"lic_pro":            "Pro",
		"lic_active":         "Действует",
		"lic_grace":          "Нет связи с сервером лицензий: платные функции работают в льготный период.",
		"lic_until":          "Действует до",
		"lic_limit":          "Лимит серверов",
		"lic_key":            "Лицензионный ключ",
		"lic_activate":       "Активировать",
		"lic_renew":          "Проверить сейчас",
		"lic_sent_title":     "Что отправляется на сервер лицензий",
		"lic_sent":           "Только при активации и раз в сутки: ключ, публичный ключ этого экземпляра, время и версия панели. IP-адрес виден серверу и хранится 30 дней. Бесплатный тариф ничего не отправляет. Игровые серверы не зависят от лицензии: если проверка не удалась, они продолжают работать.",
		"lic_not_configured": "Проверка лицензий на этой панели не настроена.",
		"lic_bad_key":        "Ключ введён неверно: проверьте символы.",
		"lic_invalid":        "Ключ не найден или запрос отклонён.",
		"lic_expired":        "Срок лицензии истёк.",
		"lic_revoked":        "Лицензия отозвана.",
		"lic_bound":          "Ключ уже привязан к другому экземпляру панели. Напишите владельцу проекта, чтобы перенести его.",
		"lic_unreachable":    "Сервер лицензий недоступен. Попробуйте позже, работа панели не нарушена.",
		"lic_other":          "Не удалось активировать лицензию.",
		"lic_ok":             "Готово.",
		"lic_last_error":     "Последняя ошибка проверки",
		"limit_reached":      "Достигнут лимит серверов вашего тарифа. Уже добавленные серверы продолжают работать. Тариф Pro снимает лимит.",
	})
	merge("en", map[string]string{
		"license":            "Licence",
		"lic_plan":           "Plan",
		"lic_free":           "Free",
		"lic_pro":            "Pro",
		"lic_active":         "Active",
		"lic_grace":          "The licence server is not reachable: paid features keep working during the grace period.",
		"lic_until":          "Valid until",
		"lic_limit":          "Server limit",
		"lic_key":            "Licence key",
		"lic_activate":       "Activate",
		"lic_renew":          "Check now",
		"lic_sent_title":     "What is sent to the licence server",
		"lic_sent":           "Only on activation and once a day: the key, this instance's public key, the time and the panel version. The server sees your IP address and keeps it for 30 days. The Free tier sends nothing. Game servers do not depend on the licence: if a check fails they keep running.",
		"lic_not_configured": "Licence checks are not configured on this panel.",
		"lic_bad_key":        "The key was typed wrong, check the characters.",
		"lic_invalid":        "Key not found or request refused.",
		"lic_expired":        "The licence has expired.",
		"lic_revoked":        "The licence was revoked.",
		"lic_bound":          "This key is already bound to another panel instance. Contact the project owner to move it.",
		"lic_unreachable":    "The licence server is not reachable. Try later; the panel keeps working.",
		"lic_other":          "Could not activate the licence.",
		"lic_ok":             "Done.",
		"lic_last_error":     "Last check error",
		"limit_reached":      "You reached the server limit of your plan. Servers already added keep running. Pro removes the limit.",
	})
}

func merge(lang string, add map[string]string) {
	for k, v := range add {
		messages[lang][k] = v
	}
}
