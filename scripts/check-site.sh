#!/usr/bin/env bash
# Run before deploying site/: fails while placeholders are still in the pages.
set -euo pipefail
cd "$(dirname "$0")/../site"
if grep -rnE 'DISCORD_INVITE_URL|GITHUB_URL|MARKET_URL|EULA_URL|PRIVACY_URL|OFFER_URL|\[решение ещё не принято\]|\[not decided yet\]' .; then
  echo "site still has placeholders, fix them before deploying" >&2
  exit 1
fi
echo "site is clean"
