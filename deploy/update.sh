#!/usr/bin/env bash
# Plexodus automatic updater.
#
# install.sh puts this at /usr/local/sbin/plexodus-update and runs it from a
# systemd timer every 15 minutes (plexodus-update.timer). setup.sh's no-sudo
# install runs it from cron as `update.sh --home ~/plexodus`.
#
# Each run: if the "latest" release has a different binary from the one
# installed, and nobody is watching anything, install it and restart
# Plexodus. While a stream is open it waits for the next run instead.
#
# Turn it off in Settings > Updates, or put PLEXODUS_AUTO_UPDATE=off in
# /etc/plexodus.env. `sudo plexodus-update --now` updates straight away,
# even with streams open or automatic updates off.
set -uo pipefail

HOME_DIR=""
FORCE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --home) HOME_DIR="${2:?--home needs a folder}"; shift 2 ;;
    --now) FORCE=1; shift ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

if [ -n "$HOME_DIR" ]; then
  BIN="$HOME_DIR/plexodus"
  DATA="$HOME_DIR/data"
  ENVFILE="$HOME_DIR/settings.env"
else
  if [ "$(id -u)" -ne 0 ]; then
    echo "Run as root: sudo plexodus-update" >&2
    exit 1
  fi
  BIN=/usr/local/bin/plexodus
  DATA=/var/lib/plexodus
  ENVFILE=/etc/plexodus.env
fi

envval() { [ -r "$ENVFILE" ] && sed -n "s/^$1=[\"']\{0,1\}\([^\"']*\)[\"']\{0,1\}\$/\1/p" "$ENVFILE" | tail -n1; }
REPO="$(envval PLEXODUS_REPO)"
REPO="${REPO:-sfxhewitt/plexodus}"
BASE="${PLEXODUS_RELEASE_URL:-https://github.com/$REPO/releases/download/latest}"

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

[ -d "$DATA" ] || { echo "No data folder at $DATA; is Plexodus installed?" >&2; exit 1; }

# One run at a time (the timer, the "Update now" button and a manual run).
# Everything this starts gets `9>&-` so a restarted server doesn't inherit
# the lock and hold it for ever.
if command -v flock >/dev/null; then
  exec 9>"$DATA/.update.lock"
  flock -w 120 9 || { echo "Another update is still running."; exit 0; }
fi

# "Update now" from Settings.
if [ -e "$DATA/update-now" ]; then
  rm -f "$DATA/update-now"
  FORCE=1
fi

now() { date +%s; }
field() { sed -n "s/.*\"$2\":\"\{0,1\}\([^,\"}]*\).*/\1/p" "$1" 2>/dev/null | head -n1; }
current_version() { "$BIN" -version 2>/dev/null || echo unknown; }

RESULT="$DATA/update-result.json"
LAST_UPDATED="$(field "$RESULT" updatedAt)"
# result STATE MESSAGE: what Settings > Updates shows.
result() {
  local tmp="$RESULT.tmp"
  printf '{"state":"%s","message":"%s","checkedAt":%s,"updatedAt":%s,"version":"%s"}\n' \
    "$1" "$2" "$(now)" "${LAST_UPDATED:-0}" "$(current_version)" >"$tmp" && chmod 644 "$tmp" && mv -f "$tmp" "$RESULT"
  echo "$1: $2"
}

STATUS="$DATA/update-status.json"
case "$(envval PLEXODUS_AUTO_UPDATE | tr 'A-Z' 'a-z')" in
  off | 0 | false | no)
    if [ "$FORCE" -eq 0 ]; then result off "Automatic updates are off in $ENVFILE."; exit 0; fi ;;
esac
if [ "$FORCE" -eq 0 ] && [ "$(field "$STATUS" autoUpdate)" = false ]; then
  result off "Automatic updates are off in Settings."
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

if ! curl -fsSL --max-time 60 "$BASE/SHA256SUMS" -o "$TMP/SHA256SUMS"; then
  result failed "Couldn't reach GitHub to check for a new version. Trying again later."
  exit 1
fi
WANT="$(grep " plexodus-linux-$ARCH\$" "$TMP/SHA256SUMS" | cut -d' ' -f1)"
HAVE="$(sha256sum "$BIN" 2>/dev/null | cut -d' ' -f1)"
if [ -z "$WANT" ]; then
  result failed "The latest release has no plexodus-linux-$ARCH yet. Trying again later."
  exit 1
fi

# Never restart in the middle of a film. The server refreshes this file
# every 15 s; if it's old the server isn't running, so nobody is watching.
busy() {
  [ "$FORCE" -eq 0 ] && [ -f "$STATUS" ] || return 1
  local at streams
  at="$(field "$STATUS" at)"
  streams="$(field "$STATUS" streams)"
  [ -n "$at" ] && [ $(($(now) - at)) -lt 120 ] && [ "${streams:-0}" -gt 0 ] || return 1
  if [ "$streams" -eq 1 ]; then OPEN="1 stream"; else OPEN="$streams streams"; fi
}

if [ "$WANT" = "$HAVE" ]; then
  # Installed but not running yet (an install that stopped before the
  # restart): restart once nobody is watching.
  RUNNING="$(field "$STATUS" version)"
  if [ -z "$HOME_DIR" ] && [ -n "$RUNNING" ] && [ "$RUNNING" != "$(current_version)" ] && [ $(($(now) - $(field "$STATUS" at))) -lt 120 ]; then
    if busy; then
      result waiting "A new version is installed. Plexodus restarts into it once nobody is watching ($OPEN open now)."
    else
      systemctl restart plexodus 9>&- && LAST_UPDATED="$(now)" && result updated "Restarted into $(current_version)."
    fi
    exit 0
  fi
  result up-to-date "Plexodus is up to date."
  exit 0
fi

if busy; then
  result waiting "A new version is ready. It installs once nobody is watching ($OPEN open now)."
  exit 0
fi

FROM="$(current_version)"
echo "Updating Plexodus from $FROM..."
if [ -n "$HOME_DIR" ]; then
  # No sudo: swap the binary in the home folder and restart it.
  curl -fsSL --max-time 900 "$BASE/plexodus-linux-$ARCH" -o "$TMP/plexodus" || { result failed "The download failed. Trying again later."; exit 1; }
  if [ "$(sha256sum "$TMP/plexodus" | cut -d' ' -f1)" != "$WANT" ]; then
    result failed "The download didn't match its checksum (the release may still be uploading). Trying again later."
    exit 1
  fi
  chmod +x "$TMP/plexodus"
  mv -f "$TMP/plexodus" "$BIN.new" && mv -f "$BIN.new" "$BIN"
  # Keep this script current too (a new file, never rewritten in place,
  # because bash is still reading this one).
  if curl -fsSL --max-time 60 "$BASE/update.sh" -o "$TMP/update.sh"; then
    chmod +x "$TMP/update.sh" && mv -f "$TMP/update.sh" "$HOME_DIR/update.sh.new" && mv -f "$HOME_DIR/update.sh.new" "$HOME_DIR/update.sh"
  fi
  "$HOME_DIR/start.sh" 9>&-
else
  # The installer verifies the checksum, refreshes the service files and
  # this updater, and restarts Plexodus. It must keep the account the
  # service already runs as (there's no sudo here to say which).
  if ! curl -fsSL --max-time 60 "$BASE/install.sh" -o "$TMP/install.sh"; then
    result failed "Couldn't download the installer. Trying again later."
    exit 1
  fi
  SVC_USER="$(sed -n 's/^User=//p' /etc/systemd/system/plexodus.service 2>/dev/null | head -n1)"
  if ! PLEXODUS_USER="${SVC_USER:-plexodus}" PLEXODUS_REPO="$REPO" PLEXODUS_UPDATER=1 bash "$TMP/install.sh" 9>&-; then
    result failed "The update didn't install (the release may still be uploading). Trying again later."
    exit 1
  fi
fi

if [ "$(sha256sum "$BIN" | cut -d' ' -f1)" = "$WANT" ]; then
  LAST_UPDATED="$(now)"
  result updated "Updated from $FROM to $(current_version)."
else
  result failed "The new version didn't install. Trying again later."
  exit 1
fi
