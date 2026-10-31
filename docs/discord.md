# Discord-сервер проекта

Единственная площадка сообщества (решение владельца). Панель, лицензии и обновления от Discord не зависят; критичные уведомления дублируются в панели и в GitHub Releases.

Сервер: «Stokehold GSM», guild id в CLAUDE.md.

## Структура (создаёт `cmd/discord-setup`)
| Категория | Каналы | Доступ |
|---|---|---|
| INFO | welcome, rules, announcements, releases, faq | читать всем, писать только модераторам и боту |
| RU | ru-general, ru-support, ru-bugs, ru-ideas, ru-guides | всем |
| EN | en-general, en-support, en-bugs, en-ideas, en-guides | всем |
| COMMUNITY | servers-showcase | всем |
| SUPPORTERS | pro-lounge (Pro, Hosting), beta (Beta tester) | по ролям |
| STAFF | mod-chat, mod-log | только Moderator |

Роли: Moderator, Hosting, Pro, Beta tester, Free. Роли Pro/Hosting/Free выдаёт бот по лицензии (фаза 4), пока вручную.

В закреплённых сообщениях welcome, rules и faq тексты на RU и EN из `internal/discordsetup/texts.go`. В них нет утверждений о пользователях, тестах или сроках, которых нет.

## Бот
Приложение `Stokehold`, в профиле и ответах честно назван ботом. Права при приглашении: Kick Members, Manage Channels, View Channels, Send Messages, Manage Messages, Embed Links, Read Message History, Manage Roles, Manage Webhooks, Moderate Members, Pin Messages (число `2252900130712594`). Модераторские права входят в набор намеренно: бот не может выдать роли права, которых у него самого нет.

Токен бота только в `.env` (`DISCORD_BOT_TOKEN`), в git и чат не попадает. Сброс токена в Developer Portal требует подтверждения владельца (пароль или 2FA).

## Запуск
```
discord-setup -invite-for <APP_ID>              # ссылка приглашения бота
DISCORD_BOT_TOKEN=... discord-setup -guild <ID> -clean-defaults
```
Повторный запуск ничего не дублирует. Существующие каналы и роли не изменяются. `-clean-defaults` удаляет только стандартные пустые каналы нового сервера («основной», «Текстовые каналы» и т.п.).

## Модерация
Правила в #rules. Автомодерацию Discord (фильтр спама и ссылок) включает владелец в настройках сервера: Moderation → AutoMod. Бот-антиспам пока не пишем.

## Не сделано
Выдача ролей по лицензии, публикация релизов из GitHub, тикеты и FAQ-ответы бота (фазы 4-5).
