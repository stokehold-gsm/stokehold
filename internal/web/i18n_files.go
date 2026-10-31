package web

func init() {
	merge("ru", map[string]string{
		"files":        "Файлы",
		"files_hint":   "Здесь можно править конфигурационные файлы сервера (только в каталогах serverfiles и lgsm/config-lgsm, только текстовые файлы). Перед сохранением панель делает копию предыдущей версии рядом с файлом (.stokehold-bak). Изменения вступают в силу после перезапуска сервера. В конфигах бывают пароли, не публикуйте их.",
		"files_empty":  "Редактируемых файлов не найдено.",
		"file_edit":    "Изменить",
		"file_save":    "Сохранить",
		"file_saved":   "Файл сохранён. Чтобы изменения заработали, перезапустите сервер.",
		"file_failed":  "Не удалось прочитать или сохранить файл.",
		"file_too_big": "Файл слишком большой (не более 256 КБ).",
		"file_path":    "Файл",
	})
	merge("en", map[string]string{
		"files":        "Files",
		"files_hint":   "Edit the server's config files here (only inside serverfiles and lgsm/config-lgsm, text files only). Before saving, the panel keeps the previous version next to the file (.stokehold-bak). Changes take effect after a server restart. Configs can hold passwords, do not publish them.",
		"files_empty":  "No editable files found.",
		"file_edit":    "Edit",
		"file_save":    "Save",
		"file_saved":   "File saved. Restart the server for the change to take effect.",
		"file_failed":  "Could not read or save the file.",
		"file_too_big": "The file is too large (256 KB at most).",
		"file_path":    "File",
	})
}
