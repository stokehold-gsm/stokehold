package web

func init() {
	merge("ru", map[string]string{
		"monitor":     "Мониторинг",
		"mon_cpu":     "Процессор",
		"mon_mem":     "Память",
		"mon_disk":    "Диск",
		"mon_load":    "Нагрузка за минуту",
		"mon_state":   "Состояние",
		"mon_players": "Игроки",
		"mon_note":    "Замер раз в минуту, в памяти хранятся последние 60. После перезапуска панели график начинается заново. Игроки видны, только если LinuxGSM печатает их число.",
		"mon_none":    "Замеров пока нет: на этой системе нет /proc или панель только что запущена.",
	})
	merge("en", map[string]string{
		"monitor":     "Monitoring",
		"mon_cpu":     "CPU",
		"mon_mem":     "Memory",
		"mon_disk":    "Disk",
		"mon_load":    "Load, 1 minute",
		"mon_state":   "State",
		"mon_players": "Players",
		"mon_note":    "Sampled once a minute; the last 60 are kept in memory and the graph restarts with the panel. Players show only when LinuxGSM prints their count.",
		"mon_none":    "No samples yet: this system has no /proc or the panel has just started.",
	})
}
