# Лицензии зависимостей и чужого кода

Проверено 2026-10-31 по репозиториям и страницам проектов (ссылки в docs/market.md).

| Что | Лицензия | Как используем | Совместимость |
|---|---|---|---|
| LinuxGSM | MIT | Внешний процесс (устанавливается на хосте пользователя). В репозитории встроены снимки `lgsm/data/<distro>.csv` со списками зависимостей (internal/helper/deps) | MIT-совместимо с любой лицензией нашего кода, требуется сохранить уведомление об авторских правах (ниже) |
| Pterodactyl (MIT), Pelican (AGPL-3.0), PufferPanel (Apache-2.0), Crafty (GPL) | | Только изучены как конкуренты, код не копируется | Не применимо. Код Pelican (AGPL) и Crafty (GPL) в проект не переносим |
| Go standard library | BSD-3-Clause | Основа всех модулей | ок |
| modernc.org/sqlite (и его зависимости libc, mathutil, memory) | BSD-3-Clause | Встроенная БД панели без CGO | ок, сверить тексты лицензий транзитивных зависимостей перед релизом (`go-licenses`) |
| golang.org/x/crypto, golang.org/x/sys | BSD-3-Clause | argon2id, системные вызовы | ок |

## Уведомление для встроенных данных LinuxGSM
Файлы `internal/helper/deps/*.csv` взяты из GameServerManagers/LinuxGSM (MIT License, Copyright (c) Daniel Gibbs и участники). Обновлять снимки при каждом релизе.

## Лицензия самого проекта
AGPL-3.0 для ядра (файл LICENSE), Pro-модуль закрыт. Решение в docs/decisions/0005-open-core.md. Зависимости с лицензиями MIT, BSD и ISC совместимы с AGPL.
