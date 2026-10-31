// Package discordsetup lays out the project's Discord server (docs/discord.md).
// It talks to the REST API directly and is safe to run twice: anything that
// already exists by name is left alone.
package discordsetup

// Permission bits, from https://docs.discord.com/developers/topics/permissions
const (
	PermKickMembers    = 1 << 1
	PermManageChannels = 1 << 4
	PermAddReactions   = 1 << 6
	PermViewChannel    = 1 << 10
	PermSendMessages   = 1 << 11
	PermManageMessages = 1 << 13
	PermEmbedLinks     = 1 << 14
	PermReadHistory    = 1 << 16
	PermManageRoles    = 1 << 28
	PermManageWebhooks = 1 << 29
	PermModerate       = 1 << 40
	PermPinMessages    = 1 << 51
)

// InvitePerms is what the bot is invited with. A bot cannot hand out
// permissions it lacks, so the moderator bits are included on purpose.
const InvitePerms = PermKickMembers | PermManageChannels | PermViewChannel |
	PermSendMessages | PermManageMessages | PermEmbedLinks | PermReadHistory |
	PermManageRoles | PermManageWebhooks | PermModerate | PermPinMessages

type Role struct {
	Name        string
	Color       int
	Hoist       bool
	Mentionable bool
	Perms       int64
}

// Message is posted (and pinned) by the bot. {#key} inside any text turns
// into a mention of the channel with that Key.
type Message struct {
	Content string
	Embeds  []Embed
	Rows    [][]Button // buttons handled by cmd/stokehold-bot
}

type Embed struct {
	Title       string
	Description string
	Color       int
	Fields      []Field
	Footer      string
}

type Field struct {
	Name, Value string
	Inline      bool
}

type Button struct {
	Key   string // custom_id is "role:" + Key
	Label string
	Emoji string
}

type Channel struct {
	Key      string // short id used by {#key} placeholders
	Name     string
	Topic    string
	Voice    bool
	ReadOnly bool     // nobody but the bot and moderators can post
	OnlyFor  []string // role names allowed to see it; empty means everyone
	Messages []Message
}

type Category struct {
	Name     string
	Channels []Channel
}

// SelfRole links a button to a role. The bot only ever hands out roles that
// are listed here, never an arbitrary role id.
type SelfRole struct {
	Key      string
	RoleName string
}

type Plan struct {
	Roles      []Role
	Categories []Category
	SelfRoles  []SelfRole
	Legacy     []string // names of earlier layouts, removed with Options.RemoveLegacy
}

const (
	RoleFounder = "Founder"
	RoleMod     = "Moderator"
	RoleBeta    = "Beta tester"
	RolePro     = "Pro"
	RoleHosting = "Hosting"
	RoleFree    = "Free"
)

const (
	colorBrand = 0xe8451a
	colorInfo  = 0x3498db
	colorOK    = 0x2ecc71
	colorWarn  = 0xf1c40f
	colorGrey  = 0x95a5a6
)

func Default() Plan {
	return Plan{
		Roles: []Role{
			{Name: RoleFounder, Color: colorBrand, Hoist: true},
			{Name: RoleMod, Color: colorWarn, Hoist: true, Mentionable: true, Perms: PermManageMessages | PermKickMembers | PermModerate},
			{Name: RoleHosting, Color: 0x9b59b6, Hoist: true},
			{Name: RolePro, Color: 0xff8a1f, Hoist: true},
			{Name: RoleBeta, Color: colorInfo, Hoist: true, Mentionable: true},
			{Name: RoleFree, Color: colorGrey},
			{Name: "🇷🇺 Русский", Color: 0x5b8def},
			{Name: "🇬🇧 English", Color: 0xe74c3c},
			{Name: "🔔 Releases", Color: colorOK, Mentionable: true},
			{Name: "📣 News", Color: colorOK, Mentionable: true},
			{Name: "Factorio", Color: 0xe67e22},
			{Name: "Minecraft", Color: 0x2ecc71},
			{Name: "Rust", Color: 0xc0392b},
			{Name: "Valheim", Color: 0x1abc9c},
			{Name: "CS2", Color: 0xf39c12},
		},
		SelfRoles: []SelfRole{
			{"ru", "🇷🇺 Русский"}, {"en", "🇬🇧 English"},
			{"releases", "🔔 Releases"}, {"news", "📣 News"},
			{"factorio", "Factorio"}, {"minecraft", "Minecraft"}, {"rust", "Rust"},
			{"valheim", "Valheim"}, {"cs2", "CS2"},
		},
		Categories: []Category{
			{Name: "📌 ИНФОРМАЦИЯ", Channels: []Channel{
				{Key: "welcome", Name: "👋・приветствие", Topic: "С чего начать", ReadOnly: true, Messages: []Message{welcomeRU}},
				{Key: "rules", Name: "📜・правила", Topic: "Правила сервера", ReadOnly: true, Messages: []Message{rulesRU}},
				{Key: "roles", Name: "🎭・роли", Topic: "Выберите роли кнопками", ReadOnly: true, Messages: []Message{rolesRU}},
				{Key: "news", Name: "📢・новости", Topic: "Новости проекта", ReadOnly: true, Messages: []Message{newsRU}},
				{Key: "releases", Name: "🚀・релизы", Topic: "Релизы и список изменений", ReadOnly: true, Messages: []Message{releasesRU}},
				{Key: "faq", Name: "❓・faq", Topic: "Частые вопросы", ReadOnly: true, Messages: []Message{faqRU}},
				{Key: "plans", Name: "🗺・планы", Topic: "Что делаем и что дальше", ReadOnly: true, Messages: []Message{plansRU}},
			}},
			{Name: "💬 СООБЩЕСТВО", Channels: []Channel{
				{Key: "chat", Name: "💬・общение", Topic: "Разговоры обо всём, что связано с игровыми серверами"},
				{Key: "games", Name: "🎮・игры", Topic: "Какие игры и серверы вы держите"},
				{Key: "showcase", Name: "🖼・серверы-участников", Topic: "Покажите свой сервер: игра, версия, адрес по желанию"},
			}},
			{Name: "🛠 ПОДДЕРЖКА", Channels: []Channel{
				{Key: "help", Name: "🆘・помощь", Topic: "Установка и настройка. Укажите версию панели и ОС", Messages: []Message{helpRU}},
				{Key: "bugs", Name: "🐞・баги", Topic: "Сообщения об ошибках", Messages: []Message{bugsRU}},
				{Key: "ideas", Name: "💡・идеи", Topic: "Предложения и пожелания", Messages: []Message{ideasRU}},
				{Key: "guides", Name: "📚・гайды", Topic: "Гайды по играм: Factorio, Minecraft, Rust и другим"},
			}},
			{Name: "🌍 ENGLISH", Channels: []Channel{
				{Key: "start", Name: "👋・start-here", Topic: "Welcome, rules and FAQ in English", ReadOnly: true, Messages: []Message{startEN}},
				{Key: "en-chat", Name: "💬・chat", Topic: "General chat"},
				{Key: "en-help", Name: "🆘・help", Topic: "Install and setup help. Include panel version and OS", Messages: []Message{helpEN}},
				{Key: "en-bugs", Name: "🐞・bugs", Topic: "Bug reports"},
				{Key: "en-ideas", Name: "💡・ideas", Topic: "Ideas and feature requests"},
				{Key: "en-guides", Name: "📚・guides", Topic: "Per-game guides: Factorio, Minecraft, Rust and others"},
			}},
			{Name: "⭐ ПОДДЕРЖАВШИЕ", Channels: []Channel{
				{Key: "beta", Name: "🧪・бета-тест", Topic: "Тестовые сборки и обратная связь", OnlyFor: []string{RoleBeta}},
				{Key: "pro", Name: "💎・pro-зал", Topic: "Pro и Hosting", OnlyFor: []string{RolePro, RoleHosting}},
			}},
			{Name: "🔊 ГОЛОСОВЫЕ", Channels: []Channel{
				{Key: "v1", Name: "🔊 Общий", Voice: true},
				{Key: "v2", Name: "🛠 Помощь голосом", Voice: true},
				{Key: "v3", Name: "🎮 Играем", Voice: true},
			}},
			{Name: "🛡 ПЕРСОНАЛ", Channels: []Channel{
				{Key: "mods", Name: "🛡・модераторы", Topic: "Только для модераторов", OnlyFor: []string{RoleMod}, Messages: []Message{modsRU}},
				{Key: "log", Name: "📋・журнал", Topic: "Журнал модерации", OnlyFor: []string{RoleMod}},
				{Key: "botlog", Name: "🤖・бот", Topic: "Служебные сообщения бота", OnlyFor: []string{RoleMod}},
			}},
		},
		Legacy: []string{
			"INFO", "RU", "EN", "COMMUNITY", "SUPPORTERS", "STAFF",
			"welcome", "rules", "announcements", "releases", "faq",
			"ru-general", "ru-support", "ru-bugs", "ru-ideas", "ru-guides",
			"en-general", "en-support", "en-bugs", "en-ideas", "en-guides",
			"servers-showcase", "pro-lounge", "beta", "mod-chat", "mod-log",
		},
	}
}
