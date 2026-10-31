# Changelog

Format follows Keep a Changelog. Versions follow semver once there is a first release.

## Unreleased

- Discord: detailed logs as embeds from a gateway connection (messages with before and after, members, voice, invites, threads, AutoMod), bot commands and audit events as embeds, numbered moderation cases.
- Discord: complaint system: /report, a message menu command, and a complaint card with take, warn, resolve and reject buttons.

## 0.1.0 - 2026-10-31

- Monitoring (CPU, memory, disk, load, server states and players), REST API with tokens, several machines through stokehold-agent, Factorio mod list, Discord webhook and bot commands, public status page. Features beyond monitoring are licence-gated (ADR 0006).
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
