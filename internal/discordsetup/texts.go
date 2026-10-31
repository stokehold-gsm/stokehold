package discordsetup

const footer = "Stokehold GSM"

var welcomeRU = Message{Embeds: []Embed{{
	Title: "Добро пожаловать в Stokehold GSM",
	Description: "Здесь делают и обсуждают панель управления игровыми серверами поверх LinuxGSM.\n\n" +
		"Релиза пока нет. Вопросы и баги пишите в {#help} и {#bugs}.",
	Color: colorBrand,
	Fields: []Field{
		{"С чего начать", "1. Прочитайте {#rules}\n2. Выберите роли в {#roles}\n3. Загляните в {#plans}, чтобы узнать, что готово и что будет", false},
		{"Кто это делает", "Один разработчик, который сам администрирует сервер Factorio. Сервер помогают вести модераторы. Бот сервера помечен как бот.", false},
		{"English", "English-speaking? Start in {#start}.", false},
	},
	Footer: footer,
}}}

var rolesRU = Message{
	Embeds: []Embed{{
		Title: "Выберите роли",
		Description: "Нажмите на кнопку, чтобы получить роль. Нажмите ещё раз, чтобы снять.\n\n" +
			"**Язык**: показывает, на каком языке вам удобнее общаться.\n" +
			"**Уведомления**: 🔔 Releases пингуется при выходе релизов, 📣 News при важных новостях.\n" +
			"Роли **Pro**, **Hosting** и **Beta tester** выдаются отдельно, их нельзя взять кнопкой.",
		Color:  colorInfo,
		Footer: footer,
	}},
	Rows: [][]Button{
		{{Key: "ru", Label: "Русский", Emoji: "🇷🇺"}, {Key: "en", Label: "English", Emoji: "🇬🇧"}},
		{{Key: "releases", Label: "Releases", Emoji: "🔔"}, {Key: "news", Label: "News", Emoji: "📣"}},
	},
}

var newsRU = Message{Embeds: []Embed{{
	Title:       "Новости проекта",
	Description: "Здесь выходят анонсы. Писать в канал могут только модераторы. Чтобы получать пинг о важном, возьмите роль 📣 News в {#roles}.",
	Color:       colorInfo,
	Footer:      footer,
}}}

var releasesRU = Message{Embeds: []Embed{{
	Title:       "Релизы",
	Description: "Публичных релизов пока нет. Когда они появятся, список изменений будет публиковаться здесь и в GitHub Releases. Роль 🔔 Releases в {#roles} даст пинг при выходе.",
	Color:       colorOK,
	Footer:      footer,
}}}

var faqRU = Message{Embeds: []Embed{{
	Title: "Частые вопросы",
	Color: colorInfo,
	Fields: []Field{
		{"Что это?", "Веб-панель для игровых серверов. Игровой движок: LinuxGSM.", false},
		{"Что уже готово?", "Прототипы отдельных модулей. Установщика и релиза пока нет.", false},
		{"Сколько стоит?", "Цены не объявлены. Ядро панели открыто (AGPL-3.0) и бесплатно, без лимита серверов. Платный тариф добавляет расширенные функции.", false},
		{"Нужен ли Discord для работы панели?", "Нет. Панель, лицензии и обновления от Discord не зависят.", false},
		{"Где задать вопрос?", "В {#help}. Ошибки в {#bugs}, идеи в {#ideas}.", false},
	},
	Footer: footer,
}}}

var plansRU = Message{Embeds: []Embed{{
	Title:       "Что делаем и что дальше",
	Description: "Сроков не обещаем.",
	Color:       colorBrand,
	Fields: []Field{
		{"Сделано (прототипы)", "• безопасная работа с файлами игровых серверов\n• проверка лицензионных токенов\n• управление LinuxGSM-серверами и подхват уже установленных\n• служебный процесс с узким набором операций", false},
		{"Первая версия", "• установка одной командой на Ubuntu и Debian\n• запуск, остановка, консоль, редактор конфигов\n• бэкапы по расписанию, в том числе во внешнее S3\n• обновление с откатом из снимка\n• пользователи, роли, двухфакторная защита\n• публичная страница статуса\n• русский и английский интерфейс", false},
		{"Позже", "• команды в Discord\n• менеджер модов\n• мониторинг ресурсов\n• публичный REST API\n• несколько машин и тариф для хостингов", false},
	},
	Footer: footer,
}}}

var helpRU = Message{Embeds: []Embed{{
	Title: "Как задать вопрос",
	Description: "Чем больше деталей, тем быстрее ответ:\n" +
		"• версия панели и ОС\n• игра и версия игры\n• что делали и что получили\n• фрагмент лога (без паролей и токенов)",
	Color:  colorInfo,
	Footer: footer,
}}}

var bugsRU = Message{Embeds: []Embed{{
	Title: "Как сообщить об ошибке",
	Description: "Опишите по шагам:\n" +
		"1. что вы сделали\n2. что ожидали\n3. что получили\n4. версия панели, ОС, игра\n5. лог\n\n" +
		"Не публикуйте пароли, токены и лицензионные ключи.",
	Color:  colorWarn,
	Footer: footer,
}}}

var ideasRU = Message{Embeds: []Embed{{
	Title:       "Как предложить идею",
	Description: "Опишите задачу, а не готовую функцию.",
	Color:       colorOK,
	Footer:      footer,
}}}

var startEN = Message{Embeds: []Embed{
	{
		Title: "Welcome to Stokehold GSM",
		Description: "This is the community for Stokehold, a web panel for game servers built on top of LinuxGSM. " +
			"It is built by one developer who runs a Factorio server and wanted something simpler than a shell.\n\n" +
			"The project is under development and there is no public release yet. You can follow progress, ask questions and suggest ideas here.",
		Color: colorBrand,
		Fields: []Field{
			{"Get help", "{#en-help}", true},
			{"Report a bug", "{#en-bugs}", true},
			{"Suggest an idea", "{#en-ideas}", true},
			{"Roles", "Pick language and notification roles in {#roles}.", false},
		},
		Footer: footer,
	},
	{
		Title: "Rules",
		Color: colorWarn,
		Description: "1. Be respectful. No insults, harassment or discrimination.\n" +
			"2. Use the right channel.\n" +
			"3. For bugs include panel version, OS, steps and logs. Never post passwords, tokens or licence keys.\n" +
			"4. No advertising, spam, pirated software or cheats.\n" +
			"5. Moderators may delete messages and restrict access. Message a moderator privately about disputes.",
		Footer: footer,
	},
	{
		Title: "FAQ",
		Color: colorInfo,
		Fields: []Field{
			{"What is ready?", "Prototypes of individual modules. No installer or release yet.", false},
			{"How much does it cost?", "Prices are not announced. The core is open source (AGPL-3.0) and free with no server limit. A paid tier adds extra features.", false},
			{"Do I need Discord to run the panel?", "No. The panel, licences and updates do not depend on Discord.", false},
		},
		Footer: footer,
	},
}}

var helpEN = Message{Embeds: []Embed{{
	Title: "How to ask for help",
	Description: "More detail gets a faster answer:\n" +
		"• panel version and OS\n• game and game version\n• what you did and what happened\n• a log excerpt (no passwords or tokens)",
	Color:  colorInfo,
	Footer: footer,
}}}

var modsRU = Message{Embeds: []Embed{{
	Title: "Памятка модератора",
	Description: "• Сначала просьба, потом удаление, потом ограничение.\n" +
		"• Команды бота: `/warn`, `/timeout`, `/untimeout`, `/kick`, `/purge`, `/warnings`. Бан и разбан только у кураторов. Бот сам пишет всё в {#log}.\n" +
		"• Порядок и сроки: {#staff}.\n" +
		"• Не публикуйте личные данные участников.\n" +
		"• Сомнительные случаи обсуждайте здесь до решения.\n" +
		"• Лицензионные ключи участников и платежи здесь не обсуждаются: это ветка шопа.",
	Color:  colorWarn,
	Footer: footer,
}}}
