# Setting up your own Plexodus server

A step-by-step guide for people who aren't server experts. You paste **one
command**, answer a few questions, and the setup does the rest. Most people
are done in about 15 minutes.

Plexodus has two halves:

- **The server.** A small program that runs on a computer that is always on
  and can see your films and TV shows. It keeps track of what you have and
  sends the video to your TV.
- **The app.** What you use on the TV (Fire TV, Android TV, Nvidia Shield,
  Google TV), on an Android phone, or in any web browser.

You set up the server once, then point the app at it.

---

## 1. What you need

**A Linux computer that stays on.** Any of these works:

- a **seedbox** or rented server running Ubuntu or Debian,
- an old PC or mini PC with Ubuntu installed,
- a Raspberry Pi 4 or 5 (64-bit Raspberry Pi OS).

It doesn't matter whether you have admin rights (sudo) on it: the setup
works either way and sorts it out for you.

**Your films and shows in folders** that this computer can see. They can be
anywhere: on its own disk, on a USB drive, on a NAS shared over the network,
or on Google Drive mounted with rclone. Plexodus never changes or moves your
files. It only reads them.

### Have these ready

The setup will ask you for these, so it helps to have them to hand:

1. **The folder paths** where your films and your TV shows are, for example
   `/home/yourname/media/Movies` and `/home/yourname/media/TV`. If you're not
   sure, see [Finding your folders](#finding-your-folders) below.
2. **Optional: a free TMDB key** for posters and descriptions
   ([how to get one](#getting-a-tmdb-key)). You can skip this and add it
   later.
3. **Optional: your Plex token**, if you use Plex and want to copy your
   library and watch history across
   ([how to find it](#finding-your-plex-token)).
4. **On a shared seedbox:** the **port number** your provider lets you use
   for your own apps (look in their control panel or help pages). On your own
   box, just accept the suggested 8484.

### What about Windows, Mac or a NAS?

Being honest: Plexodus only comes ready-made for Linux right now. There is no
Windows or Mac version you can just download and double-click.

- **Windows or Mac:** the simplest route is a cheap Linux seedbox or VPS, or
  an old PC / Raspberry Pi running Linux.
- **NAS (Synology, QNAP, Unraid…):** if you can log in to it with SSH, the
  setup command below usually works. If in doubt, ask whoever sent you this
  guide.

---

## 2. Log in to the server

Your seedbox provider gives you an address, a username and a password. On
Windows open **PowerShell**, on a Mac open **Terminal**, and type:

```sh
ssh yourusername@your-server-address
```

Type `yes` if it asks about a fingerprint, then your password (nothing shows
on screen while you type it; that's normal). You're in when you see a prompt
ending in `$`.

If your box is a PC on your desk, just open its **Terminal** app instead.

Log in as the user that can open your media folders. On a shared seedbox
that's your normal user. On your own server (Hetzner, a VPS) where you only
have `root`, root is fine: Plexodus will run as root.

---

## 3. Run the setup

Copy this whole line, paste it in (right-click usually pastes), and press
Enter:

```sh
bash <(curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/setup.sh)
```

Then answer the questions. Pressing **Enter** accepts the suggestion shown in
`[brackets]`. Here's what it asks, in order:

1. **Your password** (if you have admin rights). This lets it install
   Plexodus so it starts by itself, also after a reboot. If you don't have
   admin rights, it installs into your home folder instead and tells you so.
2. **Port.** Press Enter for 8484, or type the port your seedbox provider
   gave you.
3. **Install ffmpeg?** Say yes. It lets web browsers (especially iPhones)
   play MKV files. The TV app doesn't need it.
4. **Open the firewall?** Only asked if your server has one switched on. Say
   yes.
5. **Username and password** for Plexodus. You'll use these to sign in on
   the TV and in a browser. Pick a good password: this account is the admin.
6. **Your libraries.** Type `1` for films or `2` for TV shows, then the
   folder path. Tip: type the start of a folder name and press **Tab** to
   finish it. You can add several folders to one library and as many
   libraries as you like. Press Enter on its own when you're done.
7. **TMDB key** for posters. Paste it, or press Enter to skip.
8. **Import from Plex?** Type `y` if you want to, then your Plex address
   (press Enter if Plex is on the same machine) and your token. Connecting
   Plex also switches on **Sign in with Plex** (see section 6).

At the end it shows **the address to open in a browser**, and how to get the
TV app. Write the address down.

Plexodus starts scanning your folders straight away. It never opens your
video files while scanning, it only lists the folders, so even huge libraries
are quick. After that it rescans every 15 minutes, so new files appear on
their own.

---

## 4. Open Plexodus in a browser

Go to the address the setup showed you, for example:

```
http://your-server-address:8484
```

Sign in with the username and password you chose.

- **On a home PC or Pi**, the address is its local IP address, for example
  `http://192.168.1.50:8484`.
- **On a seedbox**, use your provider's address with your port on the end.

**If the page won't load**, the port is being blocked:

- **Seedbox:** many providers only let you use certain ports. Look in your
  provider's control panel for "ports" or "port forwarding", or ask their
  support to open or forward a port to Plexodus's port. If they give you a
  different outside number (say 20123 → 8484), your address becomes
  `http://your-server-address:20123`.
- **Home PC or Pi:** at home it works on your local network as it is. To
  watch away from home you'd need to forward the port on your router; only
  do that if you know how.
- **Behind Cloudflare or a VPN:** see the next part.

### If your server is behind Cloudflare or a VPN

Many people run Plex on a rented server (Hetzner, OVH, a VPS) behind a
**Cloudflare Tunnel**, sometimes with a VPN such as ProtonVPN on the server
too. A VPN on the server usually blocks incoming connections, which is why the
tunnel is used. Plexodus works the same way: don't open any port, add it to
the tunnel instead.

1. In the Cloudflare dashboard, go to **Zero Trust** → **Networks** →
   **Tunnels**, pick the tunnel your Plex already uses, and open **Public
   Hostname** → **Add a public hostname**.
2. Pick a name, for example `plexodus` on your domain, and set the service
   to **HTTP** and `localhost:8484` (or the port you chose in the setup).
3. Save. Plexodus is now at `https://plexodus.yourdomain.com`. Use that
   address in the browser and in the TV app.

The VPN doesn't get in the way of anything else: Plexodus fetching posters,
talking to Plex and to plex.tv all work through it.

If you use Cloudflare without a tunnel (an orange-cloud DNS record pointing
at the server), Cloudflare only passes certain ports, and 8484 isn't one of
them. Put a reverse proxy such as Caddy in front on port 443; the
[README](../README.md#sharing-with-friends-and-family) shows how.

Cloudflare's terms restrict serving lots of video through its network. That
applies to Plex behind Cloudflare just the same, so it's your call.

---

## 5. Install the app on your TV

The app works on Fire TV / Fire TV Stick / Fire TV Cube, Nvidia Shield,
Android TV / Google TV boxes, and Android phones and tablets. It isn't in an
app store, so you install it with the free **Downloader** app.

The app file lives at:

```
https://github.com/sfxhewitt/plexodus/releases/download/latest/plexodus.apk
```

### Fire TV

1. On the home screen, search for **Downloader** (orange icon, by
   AFTVnews) and install it.
2. Allow it to install apps: **Settings** → **My Fire TV** → **Developer
   options** → **Install unknown apps** → turn **Downloader** on.
   (If you can't see **Developer options**: go to **Settings** → **My Fire
   TV** → **About**, click on your device's name 7 times, then go back.)
3. Open **Downloader**, select the address box, type the app address above
   exactly, and press **Go**.
4. When it's downloaded, choose **Install**, then **Done** (you can delete
   the downloaded file when it asks).

### Nvidia Shield / Android TV / Google TV

1. Install **Downloader** from the Google Play Store.
2. Allow it to install apps: **Settings** → **Apps** (or **Device
   preferences** → **Security & restrictions**) → **Unknown sources** → turn
   **Downloader** on. The exact menu names vary by box.
3. Open Downloader, type the app address above, **Go**, then **Install**.

### Android phone or tablet

Open the app address above in Chrome on the phone, open the downloaded file,
and allow installing when asked.

---

## 6. Connect the app

Open **Plexodus** on the TV. It asks **"Connect to your server"**. There are
two ways in:

**Sign in with Plex (easiest, if you connected Plex in the setup).**

1. Choose **Sign in with Plex**. The TV shows a short code.
2. On your phone or computer, go to **plex.tv/link** and type the code.
3. The app finds your Plexodus server by itself and signs you in. No address
   to type.

For this to work, open Plexodus in a browser once first (section 4), at the
address your TV and friends will use (your Cloudflare address, if you use
one), so it knows its own address. If the app says no server was found, check
**Settings** → **Sign in with Plex** in the web page: both switches should be
on and **Public address of this server** should be the address from
section 4.

**Or type the address.**

1. Type the same address you use in the browser, for example
   `your-server-address:8484` (it adds `http://` for you).
2. Press **Connect**, then sign in with your Plexodus username and password.

On a TV the app switches to a big-screen layout made for the remote. It plays
your files exactly as they are (MKV, 4K HEVC, surround sound, subtitles), with
no conversion on the server.

To point the app at a different server later: **Settings** in the app →
**Change server**.

---

## 7. Updating later

To update, log in to the server and run **the same setup command again**:

```sh
bash <(curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/setup.sh)
```

It updates Plexodus, keeps your libraries, account, watch history and
settings, asks you to sign in, and lets you add more libraries if you want
(press Enter to skip each question).

Most look-and-feel changes, including the TV layout, come from the server, so
updating the server is usually enough. When the app itself needs an update,
install the app file again the same way as in section 5. It installs over the
old one and keeps your server address.

---

## 8. Changing things later

Everything the setup did can also be done in the web page. Sign in, then
click the **gear icon** (Settings, top right):

- **Libraries → Add library** to add more films or TV folders (the folder
  icon lets you browse to them). **Scan** looks for new files straight away.
- **Metadata** to add or change the TMDB key. Then press **Refresh
  metadata** on a library to fetch posters for titles already scanned.
- **Import from Plex** to import later, or to fix **Path mapping** if Plex
  sees your files at a different path. This is common when Plex runs in
  Docker: Plex might call a folder `/data/movies` when on the server it's
  really `/mnt/storage/movies`. Path mapping is then
  `/data=/mnt/storage`. The **Save & check connection** button shows
  whether every Plex folder can be found.
- **Share libraries** to invite friends and family with their own logins.
  If Plex is connected, anyone you already share your Plex server with can
  simply use **Sign in with Plex** and sees the libraries you share with them
  in Plex.

If a title gets matched to the wrong film or show, open it and press the
**magic wand** button (Fix match) to pick the right one.

### How to name your files

Plexodus recognises the usual naming styles:

```
Movies/
  The Matrix (1999)/The Matrix (1999).mkv
  Heat (1995).mkv

TV/
  Breaking Bad (2008)/Season 01/Breaking Bad S01E01.mkv
  Breaking Bad (2008)/Season 01/Breaking.Bad.S01E02.1080p.mkv
  Breaking Bad (2008)/Specials/Breaking Bad S00E01.mkv
```

- Films: the title and the **year in brackets** help it find the right film.
  Extra words like `1080p`, `BluRay`, `x265` are ignored.
- TV: one folder per show, with `S01E01`-style numbers in the file names.
  `Season 01` folders are optional but tidy.
- Files with `sample` or `trailer` in the name are skipped.

---

## 9. Help and how-tos

### Finding your folders

On the server, these commands help you look around:

- `pwd` shows which folder you're in (usually your home folder).
- `ls` lists what's in it. `ls media` lists what's in a folder called
  `media`, and so on.
- `ls -d ~/*/` lists just the folders in your home folder.

Keep going until you see your films (or your show folders), then the path is
your home folder plus the names, for example `/home/yourname/media/Movies`.

### Getting a TMDB key

1. Make a free account at [themoviedb.org](https://www.themoviedb.org/).
2. Go to your profile → **Settings** → **API**, and request an API key
   (choose "Developer"; any sensible answers to the form are fine).
3. Copy the **API Key** (or the longer "API Read Access Token"; either
   works).

### Finding your Plex token

Plex's own help page
["Finding an authentication token / X-Plex-Token"](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/)
shows how: in Plex on the web, open any film → **⋯** → **Get Info** → **View
XML**, and copy the part after `X-Plex-Token=` in the address bar. Treat it
like a password.


### Google Drive (rclone)

If your media lives on Google Drive mounted with rclone, give the setup the
mounted folder like any other. Two things matter:

- The mount must be readable by your user. If rclone runs as a different
  user, it needs the `--allow-other` option.
- Add `--dir-cache-time 1000h --poll-interval 15s` to your rclone mount so
  rescans are fast and don't hit Google's limits. The
  [README](../README.md#rclone-mount-settings-important) has the full
  recommended mount command.

### When something isn't right

**The setup says "Plexodus didn't start".**
Usually the port is already used by something else. Run the setup again and
pick another port.

**The web page won't load.**
See the port advice in [section 4](#4-open-plexodus-in-a-browser). To check
Plexodus is running, run the setup again: it tells you.

**The TV app says "Couldn't reach …".**
Make sure the address works in a browser first, then type exactly the same
thing in the app, including the `:8484` (or your port) at the end.

**"That address answered, but it isn't a Plexodus server."**
The address points at something else (often the wrong port, or Plex's port
32400). Use Plexodus's port.

**A library shows nothing.**
Check the folder path is right (capitals matter) and that your user can open
it: `ls /path/to/your/folder`. For rclone, make sure the mount is running.

**No posters.**
Add a TMDB key (Settings → Metadata), then press **Refresh metadata** on the
library.

**A video won't play in a web browser, especially on iPhone.**
Browsers can't play every file as it is. With ffmpeg installed (the setup
offers it) the server repackages the file for the browser on the fly without
lowering the quality. On a shared seedbox without admin rights you can't
install ffmpeg, so use the TV or Android app, which play everything.

**Video stutters when you start or skip ahead on Google Drive.**
That's Drive being slow to hand over the first part of the file. The rclone
playback options in the [README](../README.md#rclone-mount-settings-important)
help.

**Getting help.** Send the last lines of the log to whoever helps you:

- with admin rights: `journalctl -u plexodus -n 50`
- without: `tail -n 50 ~/plexodus/plexodus.log`

---

## Handy commands

| What | Command |
|---|---|
| Set up or update | the setup command in [section 3](#3-run-the-setup) |
| Is it running? (admin rights) | `systemctl status plexodus` |
| Restart it (admin rights) | `sudo systemctl restart plexodus` |
| Restart it (no admin rights) | `~/plexodus/start.sh` |
| Log (admin rights) | `journalctl -u plexodus -f` |
| Log (no admin rights) | `tail -f ~/plexodus/plexodus.log` |
