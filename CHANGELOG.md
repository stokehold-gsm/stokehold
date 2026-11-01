# Changelog

Format follows Keep a Changelog. Versions follow semver once there is a first release.

## Unreleased

- Discord: report flow follows the spec: a rejection asks for a reason in a window, the reporter gets a short DM summary, complaints about staff go to the curators channel, 24 h and 72 h targets are shown on the card and overdue cards are flagged.
- Discord: /appeal for warnings, timeouts and kicks within 14 days, one per case, decided by a curator who did not issue the punishment; accepting cancels the punishment. Punished members get the case number by DM.
- Discord: an install channel with the panel installation steps.
- Discord: detailed logs as embeds from a gateway connection (messages with before and after, members, voice, invites, threads, AutoMod), bot commands and audit events as embeds, numbered moderation cases.
- Discord: complaint system: /report, a message menu command, and a complaint card with take, warn, resolve and reject buttons.

## 0.2.1 - 2026-11-01

- Installer: when the server only has a private address (behind NAT, at home or in some clouds), the setup link now uses the public address the internet sees and also prints the local one, with a note about opening or forwarding the port.

## 0.2.0 - 2026-11-01

- New interface: full-width layout, light/dark/auto theme, animated RU/EN switch, styled game list with search, server list with state badges and action buttons, step-by-step install progress.
- Server page: live state (started, starting, stopped), address, players, resources and ports parsed from LinuxGSM details, logs tail, auto-refresh.
- File manager: browse, create, edit, download, upload, rename and delete inside a server directory, all confined to the server home.
- Updates page and automatic updates: the root helper verifies the signed release and runs the installer, which rolls back by itself. Off by default.
- Owner settings: require two-factor for everyone, session lifetime, default language.
- Licence page: the key on top, a Free / Pro / Hosting comparison table.
- Pro edition: Minecraft version and core switching, Modrinth plugin and mod search and install, quick settings for Minecraft and Factorio.
- Fix: form posts no longer fail with "forbidden" in Chromium browsers.

## 0.1.2 - 2026-10-31

- One command to a working panel: the installer opens HTTPS on port 8443 with a self-signed certificate (new `-tls-listen` flag), opens the port in ufw when it is active, and prints a link with the one-time setup code and the certificate fingerprint. No reverse proxy or tunnel is needed for the first run. The old default `-secure -trust-proxy` is upgraded in place; the forwarded-for header is never trusted on the direct HTTPS listener.

## 0.1.1 - 2026-10-31

- The paid features (REST API, several machines, mod manager, Discord webhook and panel commands, public status page) moved to the closed Pro edition (ADR 0007). The open build has monitoring, schedules, the config editor and everything else; v0.1.0 was withdrawn because it still contained the Pro code.
- Discord bot: detailed embed logs through the gateway (messages, members, voice, server, AutoMod), report system with buttons and a numbered case log.
- Licence admin: shop operator and curator roles, a staff page, password reset from the command line.
- Site: logo and favicon.

## 0.1.0 - 2026-10-31 (withdrawn)

- Monitoring (CPU, memory, disk, load, server states and players), REST API with tokens, several machines through stokehold-agent, Factorio mod list, Discord webhook and bot commands, public status page. Those beyond monitoring are Pro-only and live in a closed repository (ADR 0007).
- Fix: schedules never ran in the service because the scheduler was given the helper before it was set.
- Prototypes: safe file access for game servers, signed licence tokens, LinuxGSM driver, privileged helper with a fixed set of operations.
- Discord server layout tool and role-button bot.
- Panel core: SQLite store, setup code, sign-in with TOTP, sessions, CSRF, RU/EN interface.
- Installer with signature and checksum verification, rollback on failed upgrade, uninstaller.
- Licence server: key format with checksum, hashed key storage, one licence per order, token issuing with a root-certified online key, replay protection, hash-chained audit log.
- Licence server admin interface: owner and issuer roles, mandatory two-factor, one key per order, daily limit for issuers, hash-chained audit view.
- Install a new game server from the LinuxGSM catalogue (background job with a live log); config editor with safe replace, backup of the previous version, and path/symlink protection.
- Schedules: backups, restarts and update checks daily at a UTC time or every N hours, with catch-up after downtime and the last result shown.
- Users page for owners (create with one-time password, reset password and 2FA, delete), change-own-password.
- Licensing in the panel: licence page, activation, daily renewal, grace period, licence tokens carry named features (open core, no server limit).
- Server management in the panel: find and add LinuxGSM servers (adopt mode through a root-owned list), start/stop/restart/backup/status through the helper, viewer role is read-only.
