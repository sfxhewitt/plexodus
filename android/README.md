# Plexodus for Android

A native shell around the Plexodus web UI with a built-in direct-play video
player. The player is libVLC, so it decodes whatever the file is (MKV, HEVC,
AV1, AC3, E-AC3, DTS, TrueHD, PGS and ASS subtitles) without the server ever
transcoding. Works on phones, tablets, Android TV and Fire TV.

## Install

Download `plexodus.apk` from the
[latest release](https://github.com/sfxhewitt/plexodus/releases/download/latest/plexodus.apk),
open it on the device and allow installing from this source. On first launch
enter your server address (the one you use in a browser, for example
`http://your-server:8484`) and sign in as usual.

After that the app updates itself. On start (at most every six hours) it
reads `android-version.json` from the `latest` release, and when that build
is newer it offers to download it and hands it to the system installer. TV
Settings has a "Check for updates" button too. The first update asks you once
to let Plexodus install apps ("Install unknown apps"). Updates install over
the top, keeping the server address and sign-in, because every build is
signed with the same key. Most TV interface changes come from the server and
need no app update at all.

## How it works

- `ConnectActivity` asks for the server address once and checks it answers
  `/api/status`.
- `MainActivity` shows the web UI in a WebView. It adds a `PlexodusNative`
  object to the page; `web/src/native.ts` is the other side of that bridge.
- When you press play, the page sends the signed stream URL, title, resume
  position and intro/credit markers to `PlayerActivity`, which plays the file
  with libVLC, offers audio and subtitle tracks, skip-intro and aspect ratio,
  and reports progress to `/api/items/{id}/progress` like the web player does.
  When it closes, the page gets a `plexodus:native-ended` event so "up next"
  and resume positions behave the same as in a browser.
- At the credits (the credits marker, or 10 seconds from the end) an episode
  shows an "Up next in 10" card for the following episode (next in the
  season, else the first of the next season). It counts down while playing
  and then plays it in the same player; Back or Cancel dismisses it. After
  three episodes in a row with no button pressed, the card asks "Are you
  still watching?" instead and waits; unanswered, the player lets the TV
  sleep and closes after 30 minutes. Once the player has offered the next
  episode itself it reports `ended: false` (with the started item's id), so
  the page doesn't offer it again.

- `RoomLight` tints a Govee Wi-Fi light to match the picture (Audio panel >
  "Room light"). About ten times a second it PixelCopies the libVLC video
  surface into a 32x18 bitmap, picks the main hue (weighted by saturation,
  ignoring black bars), smooths it, and sends it to the light over Govee's
  LAN API (UDP 4003; the light needs "LAN Control" on in the Govee Home app).
  The light is found by multicast scan the first time (a picker if several
  answer) and its state is put
  back when the player closes. Pausing holds the colour.

- `Updater` is the self-update: the release workflow writes
  `android-version.json` (version code, name, size, SHA-256) after uploading
  the APK; the app downloads into its cache, checks the hash and that the APK
  is a newer `app.plexodus`, and opens it through its own small content
  provider (`UpdateFileProvider`). Local and debug builds (version code 1)
  don't update themselves.

## On a TV

On Fire TV, Android TV and the Shield the page switches to its own 10-foot
interface (`web/src/tv/`): a sidebar of libraries, hub rows under a spotlight
of the focused title, D-pad focus everywhere, and Back handled by the page
(`window.__plexodusBack`). The app reports a TV through `PlexodusNative.isTv()`
and pins the WebView text zoom to 100% so the system font scale can't inflate
it. In a desktop browser, add `?tv=1` to the address to preview it (`?tv=0`
to switch back).

## Building

CI (`.github/workflows/build.yml`) builds the APK on every push and attaches
it to the `latest` release. Locally you need JDK 17, Gradle 8.11+ and the
Android SDK:

```
cd android
gradle assembleGithubRelease   # app/build/outputs/apk/github/release/ (self-updating APK)
gradle bundleStoreRelease      # app/build/outputs/bundle/storeRelease/ (Google Play AAB)
gradle assembleStoreRelease    # app/build/outputs/apk/store/release/ (Amazon Appstore APK)
```

There are two flavours. `github` is the APK on the `latest` release and
updates itself. `store` is for Google Play and the Amazon Appstore: the stores
update it, so it leaves out the self-updater, its file provider and the
`REQUEST_INSTALL_PACKAGES` permission (both stores forbid apps updating
themselves). See `docs/store/README.md` for the listing kit.

## Signing

CI signs the APK with the key in the repository secrets `ANDROID_KEYSTORE_B64`
(a base64-encoded Java keystore with a key alias `plexodus`) and
`ANDROID_KEYSTORE_PASSWORD`. With those set, every build is signed with the
same key, so a new APK installs over the old one. Without them CI makes a
one-off key for each build, and updating means uninstalling first.

To create a key:

```
keytool -genkeypair -keystore plexodus.jks -alias plexodus -keyalg RSA -keysize 2048 \
  -validity 36500 -storepass CHOOSE_A_PASSWORD -keypass CHOOSE_A_PASSWORD -dname "CN=Plexodus"
base64 -w0 plexodus.jks   # paste into ANDROID_KEYSTORE_B64
```

Local builds without `PLEXODUS_KEYSTORE` use the debug key.
