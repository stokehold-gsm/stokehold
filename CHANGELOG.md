# Changelog

Format follows Keep a Changelog. Versions follow semver once there is a first release.

## Unreleased

- Prototypes: safe file access for game servers, signed licence tokens, LinuxGSM driver, privileged helper with a fixed set of operations.
- Discord server layout tool and role-button bot.
- Panel core: SQLite store, setup code, sign-in with TOTP, sessions, CSRF, RU/EN interface.
- Installer with signature and checksum verification, rollback on failed upgrade, uninstaller.
- Licence server: key format with checksum, hashed key storage, one licence per order, token issuing with a root-certified online key, replay protection, hash-chained audit log.
- Server management in the panel: find and add LinuxGSM servers (adopt mode through a root-owned list), start/stop/restart/backup/status through the helper, viewer role is read-only.
