# Changelog

Format follows Keep a Changelog. Versions follow semver once there is a first release.

## Unreleased

- Prototypes: safe file access for game servers, signed licence tokens, LinuxGSM driver, privileged helper with a fixed set of operations.
- Discord server layout tool and role-button bot.
- Panel core: SQLite store, setup code, sign-in with TOTP, sessions, CSRF, RU/EN interface.
- Installer with signature and checksum verification, rollback on failed upgrade, uninstaller.
- Licence server: key format with checksum, hashed key storage, one licence per order, token issuing with a root-certified online key, replay protection, hash-chained audit log.
- Licence server admin interface: owner and issuer roles, mandatory two-factor, one key per order, daily limit for issuers, hash-chained audit view.
- Schedules: backups, restarts and update checks daily at a UTC time or every N hours, with catch-up after downtime and the last result shown.
- Users page for owners (create with one-time password, reset password and 2FA, delete), change-own-password.
- Licensing in the panel: licence page, activation, daily renewal, grace period, Free tier limited to one server (existing servers are never stopped).
- Server management in the panel: find and add LinuxGSM servers (adopt mode through a root-owned list), start/stop/restart/backup/status through the helper, viewer role is read-only.
