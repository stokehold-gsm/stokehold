# Stokehold GSM

A web panel for game servers on top of [LinuxGSM](https://linuxgsm.com). One Go binary, SQLite, no Docker. It adopts LinuxGSM servers you already run instead of asking you to reinstall them.

**Status: prototype, no public release yet.** What is below is what works today, tested on Ubuntu 22.04.

## What works

- Start, stop, restart, status, update check and backup of a LinuxGSM server from the web UI.
- Find and add servers that are already installed (adopt mode). The panel can only touch Linux accounts the owner listed in `/etc/stokehold/adopt.conf`.
- Sign-in with argon2id passwords, TOTP two-factor, owner/admin/viewer roles, CSRF protection, a one-time setup code for the first account.
- Russian and English interface.
- An installer that verifies an ed25519 signature and SHA-256 sums before installing, rolls back a failed upgrade, and has an uninstaller that never touches game servers.
- A licence server (key issuing, signed short-lived tokens, hash-chained audit log).

Not yet: mod manager, scheduled backups and wipes, S3 backups, update rollback for games, public status page, Discord notifications, paid tiers.

## Why another panel

Pterodactyl needs PHP, MySQL, Redis and Docker plus a daemon per node. Crafty is Minecraft only. AMP is not sold in Russia. LinuxGSM covers 143 games but is a command line. The gap this fills: a small panel that runs on one VPS and sits on top of what you already have. Details and sources are in [docs/market.md](docs/market.md).

## Design in short

- The panel runs as an unprivileged user. Everything needing root goes through a small helper over a unix socket that accepts a fixed list of operations and re-validates every field ([docs/architecture.md](docs/architecture.md)).
- All file access to a game server directory goes through one module built on `os.Root`, with tests for `..`, absolute paths and symlink escapes.
- Licences are checked by a signed token chain, never by remote code; a licence failure can only downgrade to Free and never stops a running game server ([docs/security-licensing.md](docs/security-licensing.md)).

## Build and test

```
go test ./...
go build ./cmd/stokehold ./cmd/stokehold-helper
```

Releases are built by CI and signed by hand on the owner's machine, see [docs/release.md](docs/release.md). Tested helper and installer behaviour on a real host is recorded in [docs/prototype-findings.md](docs/prototype-findings.md).

## Layout

| Path | What |
|---|---|
| `cmd/stokehold` | the panel and a few dev commands |
| `cmd/stokehold-helper` | privileged helper |
| `cmd/stokehold-license` | licence server and issuing tool |
| `cmd/stokehold-sign` | release signing |
| `cmd/stokehold-bot`, `cmd/discord-setup` | community Discord tooling |
| `internal/` | packages |
| `install/` | installer and uninstaller |
| `docs/` | decisions, architecture, findings |
| `legal/`, `marketing/`, `site/` | drafts and the landing page |

## Notes

Written with an AI assistant; releases are signed by a human. Community: a Discord server (invite on the site). The Discord-free path always works: the panel, licences and updates do not depend on it.

The core is licensed under AGPL-3.0 ([LICENSE](LICENSE), [ADR 0005](docs/decisions/0005-open-core.md)). Paid Pro features are a separate closed module. The name and logo are not covered by the licence ([legal/trademark.md](legal/trademark.md)). See [CONTRIBUTING.md](CONTRIBUTING.md).
