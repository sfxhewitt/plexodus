# Store listing kit: Google Play and Amazon Appstore

Everything needed to list the Plexodus app. The store build is the `store`
flavour (no self-updater, no install-packages permission), built by CI on
every push to main:

| Store | File on the `latest` release |
| --- | --- |
| Google Play | `plexodus-play.aab` |
| Amazon Appstore (Fire TV) | `plexodus-amazon.apk` |

The `plexodus.apk` GitHub download stays as it is and keeps updating itself.
Store and GitHub builds share the package name `app.plexodus` but the stores
sign with their own keys, so a device moving from the GitHub APK to a store
copy has to uninstall the GitHub one first (server address is re-entered).

Version codes are the CI run number, so every main build can be uploaded.

## Listing text (same for both stores)

**App name:** Plexodus

**Short description (Play, max 80):**
Watch your own film and TV library on your phone, tablet and TV. No transcoding.

**Full description:**

> Plexodus is the app for your own Plexodus media server: the films and TV
> shows you already own, on your TV, phone and tablet.
>
> PLAYS FILES AS THEY ARE
> A built-in player handles MKV, MP4, HEVC, AV1, Dolby Digital, DTS and
> TrueHD audio, and styled or image subtitles, straight from your server with
> no transcoding, so you get the original picture and sound.
>
> MADE FOR THE BIG SCREEN
> A fast, remote-friendly interface for Android TV, Google TV, Fire TV and
> NVIDIA SHIELD, with smart rows, Up Next, skip intro and credits, and a
> screensaver of your own library.
>
> FOR THE WHOLE HOUSE
> Profiles with optional PINs, kids profiles that only show suitable titles,
> watchlists, and progress that follows you from the TV to your phone.
>
> JOIN A FRIEND'S SERVER
> Got an invite code? Type it in and you're in. No addresses to remember.
>
> Plexodus does not provide any films or TV shows. You need a Plexodus media
> server (free, open source, see github.com/sfxhewitt/plexodus) with your own
> media, or an invite from someone who runs one. Only use media you have the
> right to watch.

**Category:** Video Players & Editors (Play) / Video (Amazon)
**Tags (Amazon):** media player, home media, personal video library, Fire TV
**Price:** Free. No ads, no in-app purchases.
**Contact email:** use the developer account email.
**Website:** https://github.com/sfxhewitt/plexodus
**Privacy policy URL:** https://github.com/sfxhewitt/plexodus/blob/main/docs/privacy-policy.md
**Account deletion URL (Play):** https://github.com/sfxhewitt/plexodus/blob/main/docs/delete-account.md

## Graphics (in this folder)

| Asset | Play | Amazon | File |
| --- | --- | --- | --- |
| App icon 512x512 | Hi-res icon | Large icon | `graphics/icon-512.png` |
| Small icon 114x114 | | Small icon | `graphics/icon-114.png` |
| Feature graphic 1024x500 | Required | | `graphics/feature-graphic-1024x500.png` |
| TV banner 1280x720 | Android TV banner | Promotional image | `graphics/tv-banner-1280x720.png` |
| Fire TV background 1920x1080 | | Fire TV background | `graphics/firetv-background-1920x1080.png` |
| Phone screenshots 1080x1920 | Phone (2-8) | Phone/tablet | `screenshots/phone-*.png` |
| TV screenshots 1920x1080 | Android TV (1-8) | Fire TV (3-10) | `screenshots/tv-*.png` |

Screenshots use a made-up demo library with drawn artwork, so no film studio's
posters appear (stores reject listings that show other people's artwork).
Regenerate with `STORE_SHOTS=../docs/store/screenshots npx playwright test
store-shots --trace off` and `node scripts/store-graphics.mjs
../docs/store/graphics` from `web/`.

## Reviewer access

Run once on the server, as the user Plexodus runs as:

```
python3 <(curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/store-demo.py)
```

It downloads four Creative Commons films from the Blender Foundation (Sintel,
Tears of Steel, Elephants Dream, Big Buck Bunny; about 2.4 GB), adds them as a
"Store demo" library, creates a `storereview` account and a reviewer invite
code that can only see that library (a year, any number of uses), and prints
the text to paste into:

- **Play Console > App content > App access:** "All or some functionality is
  restricted", add instructions, paste the printed text (username/password
  fields: `storereview` and the printed password).
- **Amazon > App > Testing instructions:** paste the same text.

Keep the server online while a review is open, and run the script again any
time to get a fresh reviewer password.

## Play Console answers

**App content**

- Ads: No.
- Target audience: 18+ (also fine: 13+). Not designed for children.
- Content rating (IARC questionnaire): category **"All other app types"**. Answer No to violence,
  sexual content, language, controlled substances, gambling. "Does the app
  allow users to interact or exchange content?" **No**. "Does the app share
  the user's location?" No. "Does the app allow purchases of digital goods?"
  No. Note: it is a media player for the user's own files; it supplies no
  content. Expected rating: Everyone / PEGI 3.
- News app: No. COVID app: No. Government app: No. Financial features: None.
- Health: No.
- Data safety:
  - Does your app collect or share user data? **Yes, collects** (account on the
    user's server counts once the app sends it).
  - Is all data encrypted in transit? **No** (home servers are often plain
    http). Answer honestly: No.
  - Can users request deletion? **Yes**, in the app and at the deletion URL.
  - Data types: **Personal info > User IDs** (username) and **App activity >
    Other actions** (watch progress). Purpose: App functionality, Account
    management. Not shared with third parties. Collection is required for the
    app to work. Nothing else (no location, contacts, device IDs, crash logs,
    analytics).
- Advertising ID: the app does not use it.
- Permissions: INTERNET, ACCESS_NETWORK_STATE, WAKE_LOCK only. Nothing
  sensitive to declare.

**Store settings:** category Video Players & Editors, tags as above.

**Release > Advanced settings > Form factors:** add **Android TV** (needs the
TV banner and TV screenshots; Google runs a separate TV quality review).

**Testing:** new personal developer accounts must run a **closed test with at
least 12 testers who stay opted in for 14 days** before Production is
unlocked. Create a closed test track, upload `plexodus-play.aab`, add tester
Gmail addresses (an email list), share the opt-in link, and wait 14 days.

**App signing:** accept "Google-managed app signing key" (default). Upload the
AAB signed with our existing key; Google treats it as the upload key.

## Amazon Appstore answers

- Availability: all countries where Amazon sells Fire TV (or worldwide).
- Device support: Fire TV devices on Fire OS 7 or later (Android 9+; minSdk
  is 26, so older Fire OS 5/6 devices are excluded automatically), plus Fire
  tablets.
- Content rating questionnaire: same answers as Play ("None" everywhere).
  Expected: All ages.
- Contains ads: No. Collects personal info: Yes (account on the user's server,
  see privacy policy). Privacy policy URL as above.
- Uses Login with Amazon / in-app items: No.
- Upload `plexodus-amazon.apk`. Amazon checks D-pad navigation, Back behaviour
  and that the app does not update itself outside the store (the store build
  doesn't).

## Name check

"Plexodus" is close to the "Plex" trademark. Store reviewers sometimes reject
names that look like another company's product. The listing never mentions
Plex. If a store flags it, renaming the listing (the app can keep its package
name) is the fix.
