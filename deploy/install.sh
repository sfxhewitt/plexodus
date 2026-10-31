#!/usr/bin/env bash
# Installs or updates Plexodus as a systemd service.
#   curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/install.sh | sudo bash
# Re-running it updates the binary and keeps your data and settings.
set -euo pipefail

REPO="${PLEXODUS_REPO:-sfxhewitt/plexodus}"
RELEASE="${PLEXODUS_RELEASE:-latest}"
# Run as the account that invoked sudo, so Plexodus can read the same rclone
# mount that account (and Plex) can.
SERVICE_USER="${PLEXODUS_USER:-${SUDO_USER:-plexodus}}"

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root (sudo)." >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

BASE="https://github.com/$REPO/releases/download/$RELEASE"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "Downloading plexodus-linux-$ARCH ($RELEASE)..."
curl -fsSL "$BASE/plexodus-linux-$ARCH" -o "$TMP/plexodus"
curl -fsSL "$BASE/SHA256SUMS" -o "$TMP/SHA256SUMS"
(cd "$TMP" && grep " plexodus-linux-$ARCH\$" SHA256SUMS | sed "s/plexodus-linux-$ARCH/plexodus/" | sha256sum -c -)
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
ENV
  chmod 600 /etc/plexodus.env
fi

curl -fsSL "$BASE/plexodus.service" -o /etc/systemd/system/plexodus.service
sed -i "s/^User=.*/User=$SERVICE_USER/; s/^Group=.*/Group=$SERVICE_GROUP/" /etc/systemd/system/plexodus.service

if ! command -v ffprobe >/dev/null 2>&1; then
  echo "Note: ffprobe not found. Install it for codec checks: sudo apt install ffmpeg"
fi

systemctl daemon-reload
systemctl enable plexodus >/dev/null
systemctl restart plexodus
sleep 1
systemctl --no-pager --lines=5 status plexodus || true
echo
echo "Plexodus $(/usr/local/bin/plexodus -version) is running as $SERVICE_USER on port 8484."
echo "Settings: /etc/plexodus.env   Data: /var/lib/plexodus   Logs: journalctl -u plexodus -f"
