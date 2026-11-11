#!/usr/bin/env bash
# Guided Plexodus setup. Asks a few questions and does everything else:
# installs (or updates) the server, creates the admin account, adds your
# libraries, and optionally sets up posters (TMDB) and a Plex import.
#
#   bash <(curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/setup.sh)
#
# Run it as the user that can read your media (root is fine on your own
# server). It asks for your password when it needs sudo. Safe to run
# again: it updates Plexodus and skips what's done.
set -uo pipefail

REPO="${PLEXODUS_REPO:-sfxhewitt/plexodus}"
BASE="${PLEXODUS_RELEASE_URL:-https://github.com/$REPO/releases/download/latest}"
TTY="${PLEXODUS_TTY:-/dev/tty}"   # where answers are read from
APK_URL="https://github.com/$REPO/releases/download/latest/plexodus.apk"

if [ -t 1 ]; then
  B=$'\e[1m'; DIM=$'\e[2m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; C=$'\e[36m'; N=$'\e[0m'
else
  B=; DIM=; G=; Y=; R=; C=; N=
fi

say()   { printf '%s\n' "$*"; }
step()  { printf '\n%s== %s ==%s\n' "$C$B" "$*" "$N"; }
ok()    { printf '%s✔ %s%s\n' "$G" "$*" "$N"; }
warn()  { printf '%s! %s%s\n' "$Y" "$*" "$N"; }
fail()  { printf '%s✘ %s%s\n' "$R" "$*" "$N" >&2; }
die()   { fail "$*"; exit 1; }

# ask VAR "Question" [default]
ask() {
  local __v="$1" __q="$2" __d="${3-}" __a
  if [ -n "$__d" ]; then printf '%s%s%s [%s]: ' "$B" "$__q" "$N" "$__d"; else printf '%s%s%s: ' "$B" "$__q" "$N"; fi
  IFS= read -r -e -u 3 __a || eof
  [ -z "$__a" ] && __a="$__d"
  printf -v "$__v" '%s' "$__a"
}
# ask_secret VAR "Question"
ask_secret() {
  local __v="$1" __q="$2" __a
  printf '%s%s%s: ' "$B" "$__q" "$N"
  IFS= read -r -s -u 3 __a || eof
  printf '\n'
  printf -v "$__v" '%s' "$__a"
}
# confirm "Question" y|n  -> returns 0 for yes
confirm() {
  local __a __d="${2:-y}" __hint="Y/n"
  [ "$__d" = n ] && __hint="y/N"
  printf '%s%s%s [%s]: ' "$B" "$1" "$N" "$__hint"
  IFS= read -r -u 3 __a || eof
  __a="${__a:-$__d}"
  case "$__a" in [Yy]*) return 0 ;; *) return 1 ;; esac
}
json_str() { # JSON-quote a string
  local s="$1"
  s="${s//\\/\\\\}"; s="${s//\"/\\\"}"; s="${s//$'\t'/\\t}"; s="${s//$'\n'/\\n}"; s="${s//$'\r'/}"
  printf '"%s"' "$s"
}
json_field() { # json_field name < json  (first string value, with \" unescaped)
  sed -n "s/.*\"$1\":\"\\(\\([^\"\\\\]\\|\\\\.\\)*\\)\".*/\\1/p" | head -n1 | sed 's/\\"/"/g'
}

exec 3<"$TTY" 2>/dev/null || die "This setup asks questions, so it needs to be run in a terminal window."
eof() { printf '\n'; die "Setup was cancelled."; }
command -v curl >/dev/null || die "curl is needed. Install it first (for example: sudo apt install curl)."

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "Sorry, this computer's processor ($(uname -m)) isn't supported. Plexodus needs a 64-bit Intel/AMD or ARM machine." ;;
esac
[ "$(uname -s)" = Linux ] || die "Plexodus runs on Linux. See the setup guide for Windows and Mac options."

ME="$(id -un)"
if [ "$(id -u)" -eq 0 ]; then
  if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ]; then
    ME="$SUDO_USER"
  else
    say "You're logged in as root, so Plexodus will run as root and can read every folder."
  fi
fi
DEFUSER="$ME"
[ "$ME" = root ] && DEFUSER=admin
SUDO=""
[ "$(id -u)" -ne 0 ] && SUDO="sudo"

cat <<EOF

${B}Welcome to Plexodus setup${N}
This will install Plexodus on this computer and set it up for you.
Press Enter to accept the suggestion in [brackets].
EOF

# ---------------------------------------------------------------- install
step "1. Installing Plexodus"

MODE=""
if [ "${PLEXODUS_NO_SUDO:-}" = 1 ]; then
  MODE=user
elif ! command -v systemctl >/dev/null || [ ! -d /run/systemd/system ]; then
  MODE=user
elif [ "$(id -u)" -eq 0 ]; then
  MODE=system
elif command -v sudo >/dev/null; then
  say "Checking you have admin rights (sudo). Type your password if asked; nothing shows while you type."
  if sudo -v <&3; then MODE=system; else MODE=user; fi
else
  MODE=user
fi

PORT=""
DATA=""
if [ "$MODE" = system ]; then
  say "You have admin rights, so Plexodus will start by itself, also after a reboot."
  if [ -f /etc/plexodus.env ]; then
    PORT="$($SUDO sed -n 's/^PLEXODUS_ADDR=.*:\([0-9]\+\)$/\1/p' /etc/plexodus.env | tail -n1)"
    say "Plexodus is already installed; updating it to the latest version."
  fi
  if [ -z "$PORT" ]; then
    say "Plexodus needs a port (a number between 1024 and 65535). 8484 is the usual one;"
    say "if your seedbox provider gave you a specific port to use, type that instead."
    ask PORT "Port" 8484
  fi
  case "$PORT" in ''|*[!0-9]*) die "That isn't a number: $PORT" ;; esac
  curl -fsSL "$BASE/install.sh" -o /tmp/plexodus-install.$$ || die "Couldn't download the installer. Check this computer is online."
  $SUDO env PLEXODUS_USER="$ME" PLEXODUS_REPO="$REPO" bash /tmp/plexodus-install.$$ || { rm -f /tmp/plexodus-install.$$; die "The install didn't finish. The messages above say why."; }
  rm -f /tmp/plexodus-install.$$
  if ! $SUDO grep -q "^PLEXODUS_ADDR=:$PORT\$" /etc/plexodus.env; then
    $SUDO sed -i "s/^PLEXODUS_ADDR=.*/PLEXODUS_ADDR=:$PORT/" /etc/plexodus.env
    $SUDO systemctl restart plexodus
  fi
else
  say "You don't have admin rights here (common on shared seedboxes). That's fine:"
  say "Plexodus will run from your home folder instead."
  DIR="$HOME/plexodus"
  DATA="$DIR/data"
  mkdir -p "$DIR" "$DATA"
  if [ -f "$DIR/settings.env" ]; then
    PORT="$(sed -n 's/^PLEXODUS_ADDR=.*:\([0-9]\+\)$/\1/p' "$DIR/settings.env" | tail -n1)"
    say "Plexodus is already installed in $DIR; updating it."
  fi
  if [ -z "$PORT" ]; then
    say "Your provider decides which ports you may use. Check their help pages or control"
    say "panel for a free port for your own apps, and type it here."
    ask PORT "Port" 8484
  fi
  case "$PORT" in ''|*[!0-9]*) die "That isn't a number: $PORT" ;; esac
  say "Downloading Plexodus..."
  curl -fsSL "$BASE/plexodus-linux-$ARCH" -o "$DIR/plexodus.new" || die "Couldn't download Plexodus. Check this computer is online."
  if curl -fsSL "$BASE/SHA256SUMS" -o "$DIR/SHA256SUMS" 2>/dev/null && command -v sha256sum >/dev/null; then
    want="$(grep " plexodus-linux-$ARCH\$" "$DIR/SHA256SUMS" | cut -d' ' -f1)"
    got="$(sha256sum "$DIR/plexodus.new" | cut -d' ' -f1)"
    [ -n "$want" ] && [ "$want" != "$got" ] && { rm -f "$DIR/plexodus.new"; die "The download was damaged. Please run setup again."; }
  fi
  chmod +x "$DIR/plexodus.new"
  pkill -f "$DIR/plexodus -data" 2>/dev/null && sleep 1
  mv -f "$DIR/plexodus.new" "$DIR/plexodus"
  if [ ! -f "$DIR/settings.env" ]; then
    printf '# Plexodus settings. Restart after editing: %s/start.sh\nPLEXODUS_ADDR=:%s\n' "$DIR" "$PORT" >"$DIR/settings.env"
    chmod 600 "$DIR/settings.env"
  else
    sed -i "s/^PLEXODUS_ADDR=.*/PLEXODUS_ADDR=:$PORT/" "$DIR/settings.env"
  fi
  cat >"$DIR/start.sh" <<EOF
#!/usr/bin/env bash
# Starts Plexodus (or restarts it if it's already running).
pkill -f "$DIR/plexodus -data" 2>/dev/null && sleep 1
set -a; . "$DIR/settings.env"; set +a
cd "$DIR" || exit 1
nohup "$DIR/plexodus" -data "$DATA" >>"$DIR/plexodus.log" 2>&1 </dev/null &
EOF
  chmod +x "$DIR/start.sh"
  "$DIR/start.sh"
  if command -v crontab >/dev/null; then
    if ! crontab -l 2>/dev/null | grep -qF "$DIR/start.sh"; then
      (crontab -l 2>/dev/null; echo "@reboot $DIR/start.sh") | crontab - 2>/dev/null \
        && ok "Plexodus will start again by itself after a reboot." \
        || warn "Couldn't set Plexodus to start after a reboot. Run $DIR/start.sh after one."
    fi
  else
    warn "After this computer restarts, start Plexodus again with: $DIR/start.sh"
  fi
fi

URL="http://127.0.0.1:$PORT"
say "Waiting for Plexodus to start..."
STATUS=""
for _ in $(seq 1 30); do
  STATUS="$(curl -fsS -m 3 "$URL/api/status" 2>/dev/null)" && break
  sleep 1
done
if [ -z "$STATUS" ]; then
  fail "Plexodus didn't start."
  if [ "$MODE" = system ]; then say "See why with: journalctl -u plexodus -n 30"; else say "See why with: tail -n 30 $HOME/plexodus/plexodus.log"; fi
  say "A common cause is the port ($PORT) already being used by something else. Run setup again and pick another."
  exit 1
fi
ok "Plexodus is running on port $PORT."

# ---------------------------------------------------------------- extras
if [ "$MODE" = system ]; then
  if ! command -v ffprobe >/dev/null && command -v apt-get >/dev/null; then
    say ""
    say "ffmpeg lets web browsers (especially iPhones) play MKV files. The TV app doesn't need it."
    if confirm "Install ffmpeg now? (takes a minute)" y; then
      $SUDO apt-get install -y ffmpeg >/dev/null 2>&1 || { $SUDO apt-get update >/dev/null 2>&1; $SUDO apt-get install -y ffmpeg >/dev/null 2>&1; } \
        && { ok "ffmpeg installed."; $SUDO systemctl restart plexodus; sleep 2; } \
        || warn "ffmpeg didn't install. You can try later with: sudo apt install ffmpeg"
    fi
  fi
  if command -v ufw >/dev/null && $SUDO ufw status 2>/dev/null | grep -q "Status: active"; then
    if ! $SUDO ufw status | grep -q "^$PORT/tcp"; then
      say ""
      say "This computer has a firewall switched on, which would block Plexodus."
      if confirm "Open port $PORT in the firewall?" y; then
        $SUDO ufw allow "$PORT/tcp" >/dev/null && ok "Port $PORT opened."
      fi
    fi
  fi
fi

# ---------------------------------------------------------------- account
step "2. Your Plexodus account"
JAR="$(mktemp)"
trap 'rm -f "$JAR"' EXIT
api() { # api METHOD PATH [JSON]  -> prints body, returns 0 on 2xx
  local out code
  out="$(curl -sS -m 60 -b "$JAR" -c "$JAR" -X "$1" -H 'Content-Type: application/json' ${3:+--data "$3"} -w '\n%{http_code}' "$URL$2" 2>&1)" || { printf '%s' "$out"; return 1; }
  code="${out##*$'\n'}"; out="${out%$'\n'*}"
  printf '%s' "$out"
  [ "${code:0:1}" = 2 ]
}
api_error() { printf '%s' "$1" | json_field error; }

if printf '%s' "$STATUS" | grep -q '"setupRequired":true'; then
  say "Choose the username and password you'll sign in with, on the TV and in a browser."
  ask USERNAME "Username" "$DEFUSER"
  while :; do
    ask_secret PASS "Password (nothing shows while you type)"
    [ ${#PASS} -lt 6 ] && { warn "Please use at least 6 characters."; continue; }
    ask_secret PASS2 "Type the password again"
    [ "$PASS" != "$PASS2" ] && { warn "Those didn't match. Let's try again."; continue; }
    if res="$(api POST /api/setup "{\"username\":$(json_str "$USERNAME"),\"password\":$(json_str "$PASS")}")"; then
      ok "Account '$USERNAME' created. You're the admin."
      break
    fi
    warn "That didn't work: $(api_error "$res")"
    ask USERNAME "Username" "$USERNAME"
  done
else
  say "Plexodus already has an admin account. Sign in with it to continue."
  tries=0
  while :; do
    ask USERNAME "Username" "$DEFUSER"
    ask_secret PASS "Password"
    res="$(api POST /api/login "{\"username\":$(json_str "$USERNAME"),\"password\":$(json_str "$PASS")}")" && break
    warn "$(api_error "$res")"
    tries=$((tries + 1))
    [ $tries -ge 3 ] && die "Couldn't sign in. Your server is installed and running; finish setting it up in a browser."
  done
  if ! api GET /api/settings >/dev/null; then
    die "That account isn't the admin, so setup can't change settings. Sign in as the admin and run setup again."
  fi
  ok "Signed in."
fi
unset PASS PASS2

# ---------------------------------------------------------------- libraries
step "3. Your films and TV shows"
cat <<EOF
Now tell Plexodus where your videos are. Each library is either films or TV
shows, in one or more folders. Type the folder's full path, like
${DIM}$HOME/media/Movies${N}. Tip: press Tab to finish folder names for you.
EOF
EXISTING="$(api GET /api/libraries 2>/dev/null || true)"
if printf '%s' "$EXISTING" | grep -q '"name"'; then
  say ""
  say "You already have these libraries:"
  printf '%s' "$EXISTING" | grep -o '"name":"[^"]*"' | sed 's/"name":"\(.*\)"/  - \1/'
fi
added=0
while :; do
  say ""
  say "Add a library:  ${B}1${N} = Films   ${B}2${N} = TV shows   ${B}Enter${N} = I'm done"
  ask KIND "Your choice" ""
  case "$KIND" in
    1) KIND=movies; DEFNAME="Movies" ;;
    2) KIND=shows; DEFNAME="TV Shows" ;;
    "") break ;;
    *) warn "Type 1, 2, or just press Enter."; continue ;;
  esac
  PATHS=()
  while :; do
    ask P "Folder path" ""
    [ -z "$P" ] && { [ ${#PATHS[@]} -gt 0 ] && break; warn "Type a folder path (or press Ctrl+C to quit)."; continue; }
    P="${P/#\~/$HOME}"; P="${P%/}"
    case "$P" in /*) ;; *) P="$PWD/$P" ;; esac
    if printf '%s' "$EXISTING" | grep -qF "$(json_str "$P")"; then
      warn "That folder is already in one of your libraries."
      continue
    fi
    if [ ! -d "$P" ]; then
      warn "Can't find a folder at $P. Check the spelling (capitals matter)."
      continue
    fi
    if [ "$MODE" = system ] && [ "$ME" != "$(id -un)" ] && command -v sudo >/dev/null && ! sudo -u "$ME" test -r "$P"; then
      warn "$ME can't read that folder, so Plexodus won't be able to either."
      continue
    fi
    n="$(find "$P" -maxdepth 3 -type f \( -iname '*.mkv' -o -iname '*.mp4' -o -iname '*.avi' -o -iname '*.m4v' -o -iname '*.ts' -o -iname '*.mov' -o -iname '*.webm' \) 2>/dev/null | head -n 200 | wc -l)"
    if [ "$n" -eq 0 ]; then
      warn "No video files found near the top of that folder. It may be deeper down, or this may be the wrong folder."
      confirm "Use it anyway?" n || continue
    fi
    PATHS+=("$P")
    confirm "Add another folder to this same library?" n || break
  done
  ask NAME "Library name" "$DEFNAME"
  plist=""
  for p in "${PATHS[@]}"; do plist="$plist${plist:+,}$(json_str "$p")"; done
  if res="$(api POST /api/libraries "{\"name\":$(json_str "$NAME"),\"kind\":\"$KIND\",\"paths\":[$plist]}")"; then
    ok "Added '$NAME'. Plexodus is scanning it now."
    added=$((added + 1))
    EXISTING="$(api GET /api/libraries 2>/dev/null || true)"
  else
    warn "Couldn't add it: $(api_error "$res")"
  fi
done
[ $added -eq 0 ] && say "No new libraries added. You can add them later in Settings (gear icon) > Libraries."

# ---------------------------------------------------------------- TMDB
step "4. Posters and descriptions"
SETTINGS="$(api GET /api/settings 2>/dev/null || true)"
if printf '%s' "$SETTINGS" | grep -q '"tmdbKeySet":true'; then
  ok "A TMDB key is already set."
else
  cat <<EOF
Posters, descriptions and episode names come from TMDB (free).
To get a key: sign up at ${B}themoviedb.org${N}, then your profile > Settings > API >
request a key (choose Developer). Copy the "API Key" and paste it here.
You can also skip this and add it later in Settings > Metadata.
EOF
  ask TMDB "TMDB API key (or Enter to skip)" ""
  TMDB="$(printf '%s' "$TMDB" | tr -d '[:space:]')"
  if [ -n "$TMDB" ]; then
    if api PUT /api/settings "{\"tmdbKey\":$(json_str "$TMDB")}" >/dev/null; then ok "TMDB key saved."; else warn "Couldn't save the key; add it later in Settings > Metadata."; fi
  fi
fi

# ---------------------------------------------------------------- Plex
step "5. Bring over your Plex library (optional)"
say "If you already use Plex, Plexodus can copy your libraries, artwork and what"
say "you've watched from it in a few minutes."
if confirm "Do you want to import from Plex?" n; then
  ask PLEXURL "Plex server address" "http://127.0.0.1:32400"
  case "$PLEXURL" in http://*|https://*) ;; *) PLEXURL="http://$PLEXURL" ;; esac
  say "Your Plex token: in Plex on the web, open any film > ... > Get Info > View XML,"
  say "and copy the text after X-Plex-Token= in the address bar."
  ask_secret PLEXTOKEN "Plex token (nothing shows while you type)"
  if [ -n "$PLEXTOKEN" ] && api PUT /api/plex "{\"url\":$(json_str "$PLEXURL"),\"token\":$(json_str "$PLEXTOKEN")}" >/dev/null; then
    if res="$(api POST /api/plex/test)"; then
      libs="$(printf '%s' "$res" | grep -o '"title":"[^"]*"' | wc -l)"
      missing="$(printf '%s' "$res" | grep -o '"exists":false' | wc -l)"
      ok "Connected to Plex ($libs film/TV libraries)."
      PLEX_OK=1
      go=0
      if [ "$missing" -gt 0 ]; then
        warn "$missing of Plex's folders can't be seen from here, so those titles won't play."
        say "This happens when Plex sees the files at a different path (for example Plex in Docker)."
        say "You can fix it under Settings > Import from Plex > Path mapping, then import from there."
        confirm "Import anyway?" n && go=1
      else
        go=1
      fi
      if [ $go = 1 ]; then
        if api POST /api/plex/import >/dev/null; then
          ok "Import started. It carries on in the background; watch it in Settings > Import from Plex."
        else
          warn "The import didn't start. Try from Settings > Import from Plex."
        fi
      fi
    else
      warn "Couldn't reach Plex: $(api_error "$res")"
      say "Check the address and token, then try again in Settings > Import from Plex."
    fi
  else
    warn "Skipped: no token entered."
  fi
  unset PLEXTOKEN
fi

# ---------------------------------------------------------------- done
step "All done!"
IPS="$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | grep -v '^127\.' | head -n 3)"
HOST="$(hostname -f 2>/dev/null || hostname)"
cat <<EOF
Open Plexodus in a web browser at one of these addresses:
EOF
for ip in $IPS; do say "   ${B}http://$ip:$PORT${N}"; done
case "$HOST" in *.*) say "   ${B}http://$HOST:$PORT${N}" ;; esac
cat <<EOF
${DIM}On a seedbox, use the address your provider gave you, with :$PORT on the end.
If your provider forwards a different outside port to $PORT, use that number instead.
If none of these open, the port is probably blocked; see the setup guide.${N}

${B}On your TV${N} (Fire TV, Shield, Android TV / Google TV):
  1. Install the free ${B}Downloader${N} app and allow it to install apps.
  2. In Downloader, type:  ${B}$APK_URL${N}
  3. Install, open Plexodus, type the address above, and sign in as ${B}${USERNAME:-$ME}${N}.

${B}To update later${N}, run this same setup command again. Your libraries,
account and watch history are kept.
EOF
if [ "${PLEX_OK:-}" = 1 ]; then
  say "Tip: on the TV you can also choose ${B}Sign in with Plex${N} instead of typing the address."
  say "Open Plexodus in a browser once first, so it knows its own address. Friends you"
  say "share your Plex server with can sign in the same way."
fi
if [ "$MODE" = system ]; then
  say "${DIM}Logs: journalctl -u plexodus -f    Settings file: /etc/plexodus.env${N}"
else
  say "${DIM}Logs: $HOME/plexodus/plexodus.log    Restart: $HOME/plexodus/start.sh${N}"
fi
