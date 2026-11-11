# Plexodus: quick start

You need a Linux server that stays on (a seedbox, Hetzner/VPS, old PC or
Raspberry Pi) and can see your films and TV folders.

## 1. Run the setup

Log in to the server (`ssh you@your-server`), paste this line and press
Enter:

```sh
bash <(curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/setup.sh)
```

Answer its questions (Enter accepts the suggestion in `[brackets]`). It asks
for a port, a username and password, your film and TV folders, and,
optionally, a free [TMDB](https://www.themoviedb.org/settings/api) key for
posters and your Plex token to copy your Plex library across. At the end it
shows the address to open in a browser.

To update later, run the same line again.

## 2. Make it reachable

Open the address it showed you. If it doesn't load, the port is blocked:

- **Cloudflare Tunnel** (no open ports needed): in Cloudflare, **Zero Trust**
  → **Networks** → **Tunnels** → your tunnel → **Public Hostname** → add
  `plexodus.yourdomain`, service **HTTP** `localhost:8484`. Then use
  `https://plexodus.yourdomain`.
- **Seedbox:** ask your provider to open or forward the port.

## 3. Install the TV app

On Fire TV / Shield / Google TV, install the free **Downloader** app, allow
it to install apps, and type:

```
https://github.com/sfxhewitt/plexodus/releases/download/latest/plexodus.apk
```

Open Plexodus and either choose **Sign in with Plex** (if you connected Plex
in the setup) or type your server address and sign in.

## If something's off

- **Plex import says folders can't be seen** (Plex in Docker): in Portainer,
  open your Plex container → **Volumes**. Enter each pair as
  `Container=Host` in Plexodus **Settings** → **Import from Plex** → **Path
  mapping**.
- **Anything else:** run the setup line again; it restarts Plexodus and
  shows the address.
  Everything can also be changed in the web page under **Settings** (gear
  icon).
