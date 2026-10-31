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
	Name    string
	Color   int
	Hoist   bool
	Perms   int64
	Comment string
}

type Channel struct {
	Name     string
	Topic    string
	ReadOnly bool     // nobody but the bot and moderators can post
	OnlyFor  []string // role names allowed to see it; empty means everyone
	Pins     []string // posted and pinned by the bot when the channel has no pins yet
}

type Category struct {
	Name     string
	Channels []Channel
}

type Plan struct {
	Roles      []Role
	Categories []Category
}

const (
	RoleMod     = "Moderator"
	RoleBeta    = "Beta tester"
	RolePro     = "Pro"
	RoleHosting = "Hosting"
	RoleFree    = "Free"
)

func Default() Plan {
	return Plan{
		Roles: []Role{
			{Name: RoleMod, Color: 0xe8451a, Hoist: true, Perms: PermManageMessages | PermKickMembers | PermModerate},
			{Name: RoleHosting, Color: 0x9b59b6, Hoist: true},
			{Name: RolePro, Color: 0xff8a1f, Hoist: true},
			{Name: RoleBeta, Color: 0x3498db},
			{Name: RoleFree, Color: 0x95a5a6},
		},
		Categories: []Category{
			{Name: "INFO", Channels: []Channel{
				{Name: "welcome", Topic: "Start here / Начните отсюда", ReadOnly: true, Pins: []string{welcomeRU, welcomeEN}},
				{Name: "rules", Topic: "Rules / Правила", ReadOnly: true, Pins: []string{rulesRU, rulesEN}},
				{Name: "announcements", Topic: "News / Новости", ReadOnly: true},
				{Name: "releases", Topic: "Release notes / Релизы", ReadOnly: true},
				{Name: "faq", Topic: "Common questions / Частые вопросы", ReadOnly: true, Pins: []string{faqRU, faqEN}},
			}},
			{Name: "RU", Channels: []Channel{
				{Name: "ru-general", Topic: "Общение"},
				{Name: "ru-support", Topic: "Помощь по установке и настройке. Версию панели и ОС указывайте сразу"},
				{Name: "ru-bugs", Topic: "Баги: что сделали, что ожидали, что получили, логи"},
				{Name: "ru-ideas", Topic: "Идеи и пожелания"},
				{Name: "ru-guides", Topic: "Гайды по играм: Factorio, Minecraft, Rust и другим"},
			}},
			{Name: "EN", Channels: []Channel{
				{Name: "en-general", Topic: "General chat"},
				{Name: "en-support", Topic: "Install and setup help. Include panel version and OS"},
				{Name: "en-bugs", Topic: "Bugs: steps, expected, actual, logs"},
				{Name: "en-ideas", Topic: "Ideas and feature requests"},
				{Name: "en-guides", Topic: "Per-game guides: Factorio, Minecraft, Rust and others"},
			}},
			{Name: "COMMUNITY", Channels: []Channel{
				{Name: "servers-showcase", Topic: "Your servers / Ваши серверы"},
			}},
			{Name: "SUPPORTERS", Channels: []Channel{
				{Name: "pro-lounge", Topic: "Pro and Hosting", OnlyFor: []string{RolePro, RoleHosting}},
				{Name: "beta", Topic: "Pre-release builds / Тестовые сборки", OnlyFor: []string{RoleBeta}},
			}},
			{Name: "STAFF", Channels: []Channel{
				{Name: "mod-chat", Topic: "Moderators only", OnlyFor: []string{RoleMod}},
				{Name: "mod-log", Topic: "Moderation log", OnlyFor: []string{RoleMod}},
			}},
		},
	}
}
