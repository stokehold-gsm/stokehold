package discordsetup

// Pinned in the install channel. Keep in step with install/install.sh and docs/user/install.md.
var installRU = Message{Embeds: []Embed{{
	Title:       "Установка панели",
	Description: "Панель ставится на Linux-сервер тремя командами. Установщик сначала проверяет подпись релиза и контрольные суммы, и если что-то не сошлось, ничего не меняет. Подробнее на https://stokehold.ru.",
	Color:       colorBrand,
	Fields: []Field{
		{"Что нужно", "Ubuntu 22.04 или 24.04, Debian 12 или другой Linux с systemd и OpenSSL 3, процессор amd64 или arm64, доступ root. Нужны curl, openssl и sha256sum, обычно они уже есть. Для игровых серверов ещё tmux и то, что попросит LinuxGSM для выбранной игры.", false},
		{"Windows и macOS", "Напрямую панель на них не ставится. Возьмите VPS с Linux или на Windows включите WSL2 с systemd: `wsl --install -d Ubuntu-24.04`, затем в `/etc/wsl.conf` добавьте `[boot]` и `systemd=true` и перезапустите WSL командой `wsl --shutdown`.", false},
		{"Установка", "```\ncurl -fsSL https://dl.stokehold.online/install.sh -o install.sh\nless install.sh\nsudo bash install.sh\n```\nВторой шаг нужен, чтобы прочитать скрипт до запуска. Параметры: `--channel stable|beta`, `--version vX.Y.Z`, `--no-start`.", false},
		{"Первый вход", "Код для создания владельца лежит в журнале:\n```\njournalctl -u stokehold | grep \"setup code\"\n```\nПанель слушает только `127.0.0.1:8080`, поэтому наружу её открывает обратный прокси с HTTPS. Откройте адрес, введите код, создайте владельца и сразу включите 2FA на странице «Безопасность».", false},
		{"HTTPS через Caddy", "```\npanel.example.com {\n    reverse_proxy 127.0.0.1:8080\n}\n```\nВ `/etc/stokehold/stokehold.env` по умолчанию стоит `STOKEHOLD_FLAGS=-secure -trust-proxy`, это правильно для работы за прокси. Без прокси панель в интернет не выставляйте.", false},
		{"Обновление и удаление", "Обновление: ещё раз `sudo bash install.sh`. Если новая версия не запустилась, установщик вернёт прежнюю.\nУдаление:\n```\ncurl -fsSL https://dl.stokehold.online/uninstall.sh -o uninstall.sh\nsudo bash uninstall.sh\n```\nБез `--purge` данные панели остаются. Игровые серверы и их пользователей установщик не трогает никогда.", false},
		{"Не получилось?", "Напишите в {#help}: версия панели, ОС и фрагмент вывода установщика без паролей и токенов.", false},
	},
	Footer: footer,
}}}
