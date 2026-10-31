package web

func init() {
	merge("ru", map[string]string{
		"schedules":         "Расписание",
		"sch_add":           "Добавить",
		"sch_cmd":           "Действие",
		"sch_kind":          "Как часто",
		"sch_daily":         "Каждый день в указанное время (UTC)",
		"sch_interval":      "Каждые N часов",
		"sch_time":          "Время, ЧЧ:ММ (UTC)",
		"sch_hours":         "Часов между запусками (1-168)",
		"sch_when_daily":    "Каждый день в %02d:%02d UTC",
		"sch_when_interval": "Каждые %d ч",
		"sch_last":          "Последний запуск",
		"sch_never":         "ещё не запускалось",
		"sch_invalid":       "Проверьте действие и время.",
		"sch_note":          "Бэкап LinuxGSM останавливает игровой сервер. Если панель была выключена в назначенное время, действие выполнится один раз после запуска.",
		"sch_empty":         "Расписаний пока нет.",
		"sch_delete":        "Удалить",
		"act_check-update":  "Проверить обновление",
	})
	merge("en", map[string]string{
		"schedules":         "Schedule",
		"sch_add":           "Add",
		"sch_cmd":           "Action",
		"sch_kind":          "How often",
		"sch_daily":         "Every day at a set time (UTC)",
		"sch_interval":      "Every N hours",
		"sch_time":          "Time, HH:MM (UTC)",
		"sch_hours":         "Hours between runs (1-168)",
		"sch_when_daily":    "Every day at %02d:%02d UTC",
		"sch_when_interval": "Every %d h",
		"sch_last":          "Last run",
		"sch_never":         "not run yet",
		"sch_invalid":       "Check the action and the time.",
		"sch_note":          "A LinuxGSM backup stops the game server while it runs, so pick a quiet hour. If the panel was off at the scheduled time the action runs once after it starts.",
		"sch_empty":         "No schedules yet.",
		"sch_delete":        "Delete",
		"act_check-update":  "Check for update",
	})
}
