#!/usr/bin/env bash
# Installs or updates Plexodus as a systemd service.
#   curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/install.sh | sudo bash
# Re-running it updates the binary and keeps your data and settings.
# It also installs the automatic updater (deploy/update.sh), which keeps
# Plexodus on the latest release from then on, waiting while anyone is
# watching. Turn that off in Settings > Updates or /etc/plexodus.env.
set -euo pipefail

REPO="${PLEXODUS_REPO:-sfxhewitt/plexodus}"
RELEASE="${PLEXODUS_RELEASE:-latest}"
# Run as the account that invoked sudo, so Plexodus can read the same rclone
# mount that account (and Plex) can.
# Without sudo (e.g. a root shell), keep the account it already runs as.
EXISTING_USER="$(sed -n 's/^User=//p' /etc/systemd/system/plexodus.service 2>/dev/null | head -n1 || true)"
SERVICE_USER="${PLEXODUS_USER:-${SUDO_USER:-${EXISTING_USER:-plexodus}}}"

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root (sudo)." >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

BASE="${PLEXODUS_RELEASE_URL:-https://github.com/$REPO/releases/download/$RELEASE}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "Downloading plexodus-linux-$ARCH ($RELEASE)..."
curl -fsSL "$BASE/plexodus-linux-$ARCH" -o "$TMP/plexodus"
curl -fsSL "$BASE/SHA256SUMS" -o "$TMP/SHA256SUMS"
(cd "$TMP" && grep " plexodus-linux-$ARCH\$" SHA256SUMS | sed "s/plexodus-linux-$ARCH/plexodus/" | sha256sum -c -)
# Everything is downloaded before anything is installed, so a release that's
# still uploading fails here and changes nothing.
for f in plexodus.service update.sh plexodus-update.service plexodus-update.timer plexodus-update.path; do
  curl -fsSL "$BASE/$f" -o "$TMP/$f"
done
install -m 0755 "$TMP/plexodus" /usr/local/bin/plexodus

if ! id "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --home /var/lib/plexodus --shell /usr/sbin/nologin "$SERVICE_USER"
fi
SERVICE_GROUP="$(id -gn "$SERVICE_USER")"
install -d -o "$SERVICE_USER" -g "$SERVICE_GROUP" -m 0750 /var/lib/plexodus

if [ ! -f /etc/plexodus.env ]; then
  cat > /etc/plexodus.env <<'ENV'
# Plexodus settings. Restart after editing: sudo systemctl restart plexodus
PLEXODUS_ADDR=:8484
# PLEX_URL=http://127.0.0.1:32400
# PLEX_TOKEN=
# PLEX_PATH_MAP=
# TMDB_API_KEY=
# ANTHROPIC_API_KEY=
ENV
  chmod 600 /etc/plexodus.env
fi
if ! grep -q 'PLEXODUS_AUTO_UPDATE' /etc/plexodus.env; then
  cat >> /etc/plexodus.env <<'ENV'
# Plexodus updates itself when nobody is watching. To stop that, uncomment:
# PLEXODUS_AUTO_UPDATE=off
ENV
fi

install -m 0644 "$TMP/plexodus.service" /etc/systemd/system/plexodus.service
sed -i "s/^User=.*/User=$SERVICE_USER/; s/^Group=.*/Group=$SERVICE_GROUP/" /etc/systemd/system/plexodus.service

if ! command -v ffprobe >/dev/null 2>&1; then
  echo "Note: ffmpeg/ffprobe not found. Install them so web browsers can play MKV files: sudo apt install ffmpeg"
fi

# The automatic updater: a timer every 15 minutes, plus a path unit for
# Settings > Updates > "Update now". Files are replaced, never rewritten in
# place, because the updater may be the one running this script.
install -m 0755 "$TMP/update.sh" /usr/local/sbin/.plexodus-update.new
mv -f /usr/local/sbin/.plexodus-update.new /usr/local/sbin/plexodus-update
DATA_REAL="$(readlink -f /var/lib/plexodus)"
for unit in plexodus-update.service plexodus-update.timer plexodus-update.path; do
  sed -i "s|/var/lib/plexodus/|$DATA_REAL/|" "$TMP/$unit"
  install -m 0644 "$TMP/$unit" "/etc/systemd/system/$unit"
done

systemctl daemon-reload
systemctl enable plexodus >/dev/null
systemctl enable plexodus-update.timer plexodus-update.path >/dev/null 2>&1 || true
systemctl restart plexodus
# start, not restart: when the updater runs this, its own path unit and
# timer are what started it.
systemctl start plexodus-update.timer plexodus-update.path || true
sleep 1
systemctl --no-pager --lines=5 status plexodus || true
echo
PORT="$(sed -n 's/^PLEXODUS_ADDR=.*:\([0-9]*\)$/\1/p' /etc/plexodus.env | tail -n1)"
echo "Plexodus $(/usr/local/bin/plexodus -version) is running as $SERVICE_USER on port ${PORT:-8484}."
echo "Settings: /etc/plexodus.env   Data: /var/lib/plexodus   Logs: journalctl -u plexodus -f"
if [ -z "${PLEXODUS_UPDATER:-}" ]; then
  # Tells Settings > Updates the updater is installed.
  printf '{"state":"installed","message":"Installed. Plexodus checks for updates every 15 minutes.","checkedAt":%s,"updatedAt":%s,"version":"%s"}\n' \
    "$(date +%s)" "$(date +%s)" "$(/usr/local/bin/plexodus -version)" > /var/lib/plexodus/update-result.json
  chmod 644 /var/lib/plexodus/update-result.json
  if grep -Eiq '^PLEXODUS_AUTO_UPDATE=(off|0|false|no)' /etc/plexodus.env; then
    echo "Automatic updates are off (PLEXODUS_AUTO_UPDATE in /etc/plexodus.env)."
  else
    echo "Automatic updates are on: Plexodus installs new releases by itself when nobody is watching."
    echo "Turn them off in Settings > Updates. Update log: journalctl -u plexodus-update"
  fi
fi
