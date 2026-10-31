#!/usr/bin/env bash
# Stokehold installer.
#
#   curl -fsSL https://dl.stokehold.online/install.sh -o install.sh
#   less install.sh            # read it first, then:
#   sudo bash install.sh
#
# Piping straight into bash works too, but the signature check below is what
# protects you, not the pipe. Options:
#   --channel stable|beta   release channel (default stable)
#   --version vX.Y.Z        install this exact version
#   --no-start              install but do not start the services
#   --local-dir DIR         use release files from DIR instead of downloading (testing)
#   --pubkey FILE           verify against this public key instead of the built-in one (testing)
set -euo pipefail

REPO="stokehold-gsm/stokehold"
CHANNEL="stable"
VERSION=""
NO_START=0
LOCAL_DIR=""
PUBKEY_FILE=""

# The release public key. Replaced when a release is cut; the installer refuses
# to run with the placeholder, so a development copy can never "verify" anything.
read -r -d '' BUILTIN_PUBKEY <<'PEM' || true
REPLACE_WITH_RELEASE_PUBLIC_KEY
PEM

say() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --channel) CHANNEL="${2:-}"; shift 2 ;;
    --version) VERSION="${2:-}"; shift 2 ;;
    --no-start) NO_START=1; shift ;;
    --local-dir) LOCAL_DIR="${2:-}"; shift 2 ;;
    --pubkey) PUBKEY_FILE="${2:-}"; shift 2 ;;
    -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
    *) die "unknown option $1" ;;
  esac
done
case "$CHANNEL" in stable|beta) ;; *) die "channel must be stable or beta" ;; esac

[ "$(id -u)" -eq 0 ] || die "run as root (sudo bash install.sh)"
[ "$(uname -s)" = "Linux" ] || die "Linux only"
command -v systemctl >/dev/null || die "systemd is required"
case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported CPU $(uname -m)" ;;
esac

for t in curl openssl sha256sum; do command -v "$t" >/dev/null || die "$t is required (apt install $t)"; done
# ed25519 verification of a raw message needs OpenSSL 3
ossl_major=$(openssl version | sed -n 's/^OpenSSL \([0-9]*\)\..*/\1/p')
[ "${ossl_major:-0}" -ge 3 ] || die "OpenSSL 3 or newer is required (Ubuntu 22.04+, Debian 12+)"
command -v tmux >/dev/null || say "note: tmux is not installed; LinuxGSM will ask for it when you add a game server"

WORK=$(mktemp -d)
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

# --- public key ---
if [ -n "$PUBKEY_FILE" ]; then
  cp "$PUBKEY_FILE" "$WORK/pub.pem"
else
  case "$BUILTIN_PUBKEY" in *REPLACE_WITH*) die "this copy has no release key built in; use the install.sh from a release, or pass --pubkey" ;; esac
  printf '%s\n' "$BUILTIN_PUBKEY" > "$WORK/pub.pem"
fi

# --- fetch ---
BIN_PANEL="stokehold-linux-$ARCH"
BIN_HELPER="stokehold-helper-linux-$ARCH"
if [ -n "$LOCAL_DIR" ]; then
  say "using release files from $LOCAL_DIR"
  for f in SHA256SUMS SHA256SUMS.sig "$BIN_PANEL" "$BIN_HELPER"; do
    [ -f "$LOCAL_DIR/$f" ] || die "$LOCAL_DIR/$f is missing"
    cp "$LOCAL_DIR/$f" "$WORK/$f"
  done
  TAG="local"
else
  if [ -z "$VERSION" ]; then
    if [ "$CHANNEL" = "stable" ]; then
      VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
    else
      VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=1" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
    fi
    [ -n "$VERSION" ] || die "could not find a $CHANNEL release"
  fi
  case "$VERSION" in v[0-9]*) ;; *) die "odd version string: $VERSION" ;; esac
  TAG="$VERSION"
  say "installing $TAG ($CHANNEL)"
  base="https://github.com/$REPO/releases/download/$TAG"
  for f in SHA256SUMS SHA256SUMS.sig "$BIN_PANEL" "$BIN_HELPER"; do
    curl -fsSL "$base/$f" -o "$WORK/$f" || die "download of $f failed"
  done
fi

# --- verify: signature first, then hashes. Anything wrong aborts before install. ---
say "verifying signature"
openssl pkeyutl -verify -pubin -inkey "$WORK/pub.pem" -rawin -in "$WORK/SHA256SUMS" -sigfile "$WORK/SHA256SUMS.sig" >/dev/null 2>&1 \
  || die "signature check FAILED, nothing was installed"
say "verifying checksums"
( cd "$WORK" && grep -E " \*?($BIN_PANEL|$BIN_HELPER)\$" SHA256SUMS | sha256sum -c - >/dev/null ) \
  || die "checksum mismatch, nothing was installed"
[ "$(cd "$WORK" && grep -cE " \*?($BIN_PANEL|$BIN_HELPER)\$" SHA256SUMS)" -eq 2 ] || die "SHA256SUMS does not list both binaries"

# --- install (with rollback) ---
PREV=/usr/local/lib/stokehold/previous
mkdir -p "$PREV"
UPGRADE=0
if [ -x /usr/local/bin/stokehold ]; then
  UPGRADE=1
  cp -p /usr/local/bin/stokehold "$PREV/stokehold"
  [ -x /usr/local/bin/stokehold-helper ] && cp -p /usr/local/bin/stokehold-helper "$PREV/stokehold-helper"
fi

id stokehold >/dev/null 2>&1 || useradd --system --home-dir /var/lib/stokehold --shell /usr/sbin/nologin stokehold
install -d -m 750 -o stokehold -g stokehold /var/lib/stokehold
install -d -m 755 /etc/stokehold
[ -f /etc/stokehold/stokehold.env ] || printf 'STOKEHOLD_FLAGS=-secure -trust-proxy\n' > /etc/stokehold/stokehold.env

# Services are stopped around the swap so a running binary is never overwritten in place.
systemctl stop stokehold.service stokehold-helper.service 2>/dev/null || true
install -m 755 "$WORK/$BIN_PANEL" /usr/local/bin/stokehold
install -m 755 "$WORK/$BIN_HELPER" /usr/local/bin/stokehold-helper

PANEL_UID=$(id -u stokehold)
cat > /etc/systemd/system/stokehold-helper.service <<UNIT
[Unit]
Description=Stokehold privileged helper (fixed set of operations)
After=network-online.target

[Service]
ExecStart=/usr/local/bin/stokehold-helper -socket /run/stokehold/helper.sock -uid $PANEL_UID
RuntimeDirectory=stokehold
RuntimeDirectoryMode=0755
NoNewPrivileges=no
ProtectHome=no
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
UNIT

cat > /etc/systemd/system/stokehold.service <<'UNIT'
[Unit]
Description=Stokehold panel
After=network-online.target stokehold-helper.service
Wants=stokehold-helper.service

[Service]
User=stokehold
Group=stokehold
EnvironmentFile=/etc/stokehold/stokehold.env
ExecStart=/usr/local/bin/stokehold serve -db /var/lib/stokehold/panel.db -listen 127.0.0.1:8080 $STOKEHOLD_FLAGS
ReadWritePaths=/var/lib/stokehold
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
LockPersonality=yes
MemoryDenyWriteExecute=yes
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable stokehold-helper.service stokehold.service >/dev/null 2>&1

healthy() {
  for _ in $(seq 1 20); do
    curl -fsS -m 2 http://127.0.0.1:8080/health >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

if [ "$NO_START" -eq 1 ]; then
  say "installed, not started (--no-start)"
  exit 0
fi

systemctl restart stokehold-helper.service stokehold.service
if ! healthy; then
  if [ "$UPGRADE" -eq 1 ] && [ -x "$PREV/stokehold" ]; then
    say "the new version did not come up, rolling back"
    systemctl stop stokehold.service stokehold-helper.service || true
    install -m 755 "$PREV/stokehold" /usr/local/bin/stokehold
    [ -x "$PREV/stokehold-helper" ] && install -m 755 "$PREV/stokehold-helper" /usr/local/bin/stokehold-helper
    # the broken version already burned systemd's restart budget, so clear it first
    systemctl reset-failed stokehold.service stokehold-helper.service 2>/dev/null || true
    systemctl restart stokehold-helper.service stokehold.service || true
    if healthy; then
      die "upgrade failed; the previous version is running again. See: journalctl -u stokehold"
    fi
    die "upgrade failed and the previous version did not start either. See: journalctl -u stokehold"
  fi
  die "the panel did not start; see: journalctl -u stokehold"
fi

cat <<DONE

Stokehold $TAG is running on 127.0.0.1:8080 (not reachable from outside yet).

Next:
  1. Put an HTTPS reverse proxy in front, for example Caddy:
       panel.example.com {
           reverse_proxy 127.0.0.1:8080
       }
     For a quick look over plain HTTP, remove "-secure" in /etc/stokehold/stokehold.env
     and run: systemctl restart stokehold
  2. Create the owner account. The one-time setup code is in the log:
       journalctl -u stokehold | grep "setup code"
  3. Turn on two-factor authentication on the Security page.

Uninstall: sudo bash uninstall.sh   (your game servers are never touched)
DONE
