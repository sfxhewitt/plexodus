# Setting up your own Plexodus server

A step-by-step guide for people who aren't server experts. Take it one part
at a time. Most people are done in about half an hour, plus however long the
first scan of a big library takes.

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

It needs to be a normal 64-bit Intel/AMD or ARM machine. You need to be able
to type commands on it (usually by logging in with SSH, see below), and the
easy install needs **sudo** (admin rights). If you're not sure whether you
have sudo, see [section 2](#2-check-you-can-use-sudo).

**Your films and shows in folders** that this computer can see. They can be
anywhere: on its own disk, on a USB drive, on a NAS shared over the network,
or on Google Drive mounted with rclone. Plexodus never changes or moves your
files. It only reads them.

**Optional but recommended:**

- A free **TMDB key**, for posters, descriptions and episode names
  ([section 7](#7-get-posters-and-descriptions-tmdb-key)).
- **Plex**, if you already have it: Plexodus can copy your whole Plex library
  across in a few minutes ([section 8](#8-optional-copy-everything-from-plex)).

### What about Windows, Mac or a NAS?

Being honest: Plexodus only comes ready-made for Linux right now. There is no
Windows or Mac version you can just download and double-click.

- **Windows or Mac:** the simplest route is a cheap Linux seedbox or VPS, or
  an old PC / Raspberry Pi running Linux. (Technically you can run it inside
  WSL on Windows or build it yourself on a Mac, but that's fiddly and not
  something this guide covers.)
- **NAS (Synology, QNAP, Unraid…):** if you can log in to it with SSH, you can
  usually run the program directly the way
  [section 2b](#2b-no-sudo-shared-seedbox) describes. Docker is possible for
  people who are comfortable building an image themselves. If in doubt, ask
  whoever sent you this guide.

### Logging in to the server (SSH)

Your seedbox provider gives you an address, a username and a password. On
Windows open **PowerShell**, on a Mac open **Terminal**, and type:

```sh
ssh yourusername@your-server-address
```

Type `yes` if it asks about a fingerprint, then your password (nothing shows
on screen while you type it; that's normal). You're in when you see a prompt
ending in `$`. Every command in this guide is typed (or pasted) there and
run with Enter.

If your box is a PC on your desk, open its **Terminal** app instead.

---

## 2. Check you can use sudo

Type:

```sh
sudo -v
```

and enter your password if asked.

- **No error?** You have sudo. Go to [section 3](#3-install-plexodus).
- **"is not in the sudoers file" or "command not found"?** Your box is
  shared and you aren't allowed admin rights. Use
  [section 2b](#2b-no-sudo-shared-seedbox) instead.

### 2b. No sudo (shared seedbox)

Many cheap seedboxes are shared between customers, so you can't install
things system-wide. Plexodus still runs; you just start it yourself.

1. Ask your provider (or check their help pages) **which port number you're
   allowed to use** for your own apps. This guide uses `12345` as an
   example; use yours instead.
2. Download Plexodus into your home folder:

   ```sh
   curl -fsSL -o ~/plexodus https://github.com/sfxhewitt/plexodus/releases/download/latest/plexodus-linux-amd64
   chmod +x ~/plexodus
   ```

   (On a Raspberry Pi or other ARM box, change `amd64` to `arm64`.)
3. Start it so it keeps running after you log out:

   ```sh
   nohup ~/plexodus -data ~/plexodus-data -addr :12345 > ~/plexodus.log 2>&1 &
   ```

4. Your web address is `http://your-server-address:12345`. Carry on from
   [section 4](#4-open-plexodus-and-create-your-account).

What's different without sudo:

- It won't start by itself if the server reboots. Run the `nohup` command
  again after a reboot (or ask your provider how to start apps on boot).
- To **update**, stop it, download again and start it again:

  ```sh
  pkill -f "plexodus -data"
  curl -fsSL -o ~/plexodus https://github.com/sfxhewitt/plexodus/releases/download/latest/plexodus-linux-amd64
  chmod +x ~/plexodus
  nohup ~/plexodus -data ~/plexodus-data -addr :12345 > ~/plexodus.log 2>&1 &
  ```

- Settings like the TMDB key go in through the web page
  ([section 7](#7-get-posters-and-descriptions-tmdb-key)), so you don't need
  the settings file mentioned later.
- The log is in `~/plexodus.log` instead of `journalctl`.

---

## 3. Install Plexodus

Log in as **the same user that can open your media folders** (on a seedbox,
your normal user), then paste this one line:

```sh
curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/install.sh | sudo bash
```

It takes a few seconds. What it does:

- downloads Plexodus and checks the download isn't corrupted,
- sets it up to start automatically, also after a reboot,
- runs it as your user, so it can see the same folders you can,
- keeps its own data (database, posters) in `/var/lib/plexodus`,
- creates a settings file at `/etc/plexodus.env`.

At the end you should see a line like:

```
Plexodus ... is running as yourusername on port 8484.
```

**Recommended extra:** install ffmpeg, so web browsers (especially iPhone
Safari) can play MKV files and show subtitles:

```sh
sudo apt install -y ffmpeg
```

The TV and Android app don't need it: they play the files as they are.

---

## 4. Open Plexodus and create your account

In a web browser, go to:

```
http://your-server-address:8484
```

(Use the port from section 2b if you went that way.)

- **On a home PC or Pi**, the address is its local IP address, for example
  `http://192.168.1.50:8484`. You can find it by typing `hostname -I` on
  that machine.
- **On a seedbox**, see [section 5](#5-make-it-reachable-ports-and-firewall)
  first if the page doesn't load.

The first time, Plexodus says **"Create the admin account to get started."**
Pick a username and a good password and press **Create account**. This is
your account; whoever creates it first becomes the admin, so do it straight
away after installing.

---

## 5. Make it reachable (ports and firewall)

If the page in section 4 doesn't load, something is blocking port 8484.

- **Seedbox:** many providers only let you use certain ports. Look in your
  provider's control panel for "ports" or "port forwarding", or ask their
  support to open/forward a port to **8484**. If they give you a different
  outside number (say 8104 → 8484), your address becomes
  `http://your-server-address:8104`.
- **Server with a firewall (ufw):** run

  ```sh
  sudo ufw allow 8484/tcp
  ```

- **Home PC or Pi, watching at home:** nothing to do. Use the local address
  (like `http://192.168.1.50:8484`) on the TV.
- **Home PC or Pi, watching away from home:** you'd need to forward port 8484
  on your router to that machine. Only do this if you know how; see
  "Sharing with friends and family" in the [README](../README.md) for safer
  options with HTTPS.

To check Plexodus itself is running, on the server type:

```sh
systemctl status plexodus
```

It should say **active (running)** in green. Press `q` to get out.

---

## 6. Add your libraries

In Plexodus, click the **gear icon** (Settings, top right). Under
**Libraries**, press **Add library**:

1. **Name:** for example `Movies`.
2. **Type:** **Movies** or **TV shows**. Keep films and TV in separate
   libraries.
3. **Folders:** click the **folder icon** next to the box to browse to the
   folder, then **Use this folder**. You can also type the path, for example
   `/home/yourusername/media/Movies`. If one library is spread over several
   folders or drives, press **Add folder** to add more.
4. Press **Add library**.

Plexodus starts scanning straight away. It never opens your video files while
scanning, it only lists the folders, so even huge libraries are quick. After
that it rescans every 15 minutes by itself, so new files appear on their own.
You can also press **Scan** on a library any time.

Leave **Skip unchanged folders** off unless you know your storage updates
folder dates when files are added (local disks do). Plexodus switches it on
by itself for libraries over 50,000 files.

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

- Films: the title and the **year in brackets** helps it find the right
  film. Extra words like `1080p`, `BluRay`, `x265` are ignored.
- TV: one folder per show, with `S01E01`-style numbers in the file names.
  `Season 01` folders are optional but tidy.
- Files with `sample` or `trailer` in the name are skipped.

If a title gets matched to the wrong film or show, open it in a web browser
and press the **magic wand** button (Fix match) to pick the right one.

### Using Google Drive (rclone)

If your media lives on Google Drive mounted with rclone, it works the same:
pick the mounted folder when you add the library. Two things matter:

- The rclone mount must be readable by the user that installed Plexodus.
  If rclone runs as a different user, it needs the `--allow-other` option.
- Add these options to your rclone mount so rescans are fast and don't hit
  Google's limits:

  ```
  --dir-cache-time 1000h --poll-interval 15s
  ```

The [README](../README.md#rclone-mount-settings-important) has the full
recommended mount command.

---

## 7. Get posters and descriptions (TMDB key)

Without this, your library still works but has no posters or descriptions.

1. Make a free account at [themoviedb.org](https://www.themoviedb.org/).
2. Go to your profile → **Settings** → **API**, and request an API key
   (choose "Developer"; any sensible answers to the form are fine).
3. Copy the **API Key** (or the longer "API Read Access Token"; either
   works).
4. In Plexodus: **Settings** → **Metadata** → paste it into **TMDB API key**
   → **Save key**.
5. On each library, press **Refresh metadata** to fetch artwork for titles
   that were already scanned.

---

## 8. Optional: copy everything from Plex

If you already use Plex, Plexodus can import it in one go: libraries, titles,
artwork, descriptions, ratings, what you've watched and where you stopped,
and Plex's intro/credits markers (for **Skip intro**). It doesn't read your
video files, so it only takes minutes.

1. Find your **Plex token**: Plex's own help page
   ["Finding an authentication token / X-Plex-Token"](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/)
   shows how (open any item → **Get Info** → **View XML**, and copy the part
   after `X-Plex-Token=` in the address bar). Treat it like a password.
2. In Plexodus: **Settings** → **Import from Plex**.
3. **Plex URL:** if Plex runs on the same machine, `http://127.0.0.1:32400`.
   Otherwise the address of your Plex server.
4. Paste the token, press **Save & check connection**. It lists your Plex
   libraries and whether Plexodus can see their folders.
5. Press **Import**.

If the check says folders aren't visible, Plex sees your files at a different
path than Plexodus does (common when Plex runs in Docker). Fill in **Path
mapping** as `plex-path=real-path`, for example
`/data/media=/home/yourusername/media`.

After importing, **Watch status from Plex** (also in Settings) keeps what you
watch in Plex in step with Plexodus every few minutes. Plex is optional from
then on; Plexodus keeps scanning by itself.

**Sign in with Plex** (log in with your Plex account instead of pasting a
token) is coming soon. For now, use the token.

---

## 9. Install the app on your TV

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

## 10. Connect the app

Open **Plexodus** on the TV. It asks **"Connect to your server"**:

1. Type the same address you use in the browser, for example
   `your-server-address:8484` (it adds `http://` for you).
2. Press **Connect**, then sign in with your Plexodus username and password.

On a TV the app switches to a big-screen layout made for the remote. It plays
your files exactly as they are (MKV, 4K HEVC, surround sound, subtitles), with
no conversion on the server.

To point the app at a different server later: **Settings** in the app →
**Change server**.

---

## 11. Updating later

New features land often. To update the server, run **the same command you
installed with**:

```sh
curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/install.sh | sudo bash
```

Your libraries, accounts, watch history and settings are kept. (No sudo? Use
the update steps in [section 2b](#2b-no-sudo-shared-seedbox).)

Most look-and-feel changes, including the TV layout, come from the server, so
updating the server is usually enough. When the app itself needs an update,
install the new app file the same way as in section 9, using the same
address. It installs over the old one and keeps your server address.

---

## 12. When something isn't right

**The web page won't load.**
Check Plexodus is running: `systemctl status plexodus`. If it isn't, start it
with `sudo systemctl restart plexodus`. If it is running, the port is
probably blocked: see [section 5](#5-make-it-reachable-ports-and-firewall).

**The TV app says "Couldn't reach …".**
Make sure the address works in a browser first, then type exactly the same
thing in the app, including the `:8484` (or your port) at the end.

**"That address answered, but it isn't a Plexodus server."**
The address points at something else (often the wrong port, or Plex's port
32400). Use Plexodus's port.

**A library shows nothing, or the folder browser can't open my folder.**
Plexodus runs as the user that installed it. Check that user can see the
files: `ls /path/to/your/folder`. For rclone mounts, make sure the mount is
running and uses `--allow-other` if it belongs to a different user.

**No posters.**
Add a TMDB key ([section 7](#7-get-posters-and-descriptions-tmdb-key)), then
press **Refresh metadata** on the library.

**Wrong film or show matched.**
Open it in a web browser and press the **magic wand** button (Fix match).
Better file names (title and year) help too.

**A video won't play in a web browser, especially on iPhone.**
Install ffmpeg: `sudo apt install -y ffmpeg`. Browsers can't play every file
as it is; with ffmpeg the server repackages it for the browser on the fly
(without lowering the quality). The TV app doesn't need this.

**Video stutters when you start or skip ahead on Google Drive.**
That's Drive being slow to hand over the first part of the file. The rclone
playback options in the [README](../README.md#rclone-mount-settings-important)
help.

**Changing settings by file.**
Settings that aren't on the web page live in `/etc/plexodus.env`. Edit it
with `sudo nano /etc/plexodus.env`, save with Ctrl+O, Enter, exit with
Ctrl+X, then run `sudo systemctl restart plexodus`.

**Seeing what's going on (logs).**

```sh
journalctl -u plexodus -f
```

shows the live log (Ctrl+C to stop). Copy the last lines into a message when
asking for help.

---

## Handy commands

| What | Command |
|---|---|
| Install or update | `curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/install.sh \| sudo bash` |
| Is it running? | `systemctl status plexodus` |
| Restart it | `sudo systemctl restart plexodus` |
| Live log | `journalctl -u plexodus -f` |
| Edit settings file | `sudo nano /etc/plexodus.env` |
| Install ffmpeg | `sudo apt install -y ffmpeg` |
