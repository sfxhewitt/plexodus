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
`ftl19.seedit4.me:8104`) and sign in as usual.

To update, install the newer APK over the old one. Updates install over each
other because every build is signed with the same key.

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
gradle assembleRelease
# app/build/outputs/apk/release/app-release.apk
```

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
