# Plexodus privacy policy

_Last updated: 12 November 2026_

Plexodus is an app for watching videos from your own Plexodus media server, or
one a friend has invited you to. This policy covers the Plexodus app for
Android, Android TV and Fire TV.

## The short version

- The app has no accounts, analytics, advertising or tracking of its own.
- Your library, watch history and account live on the media server you
  connect to, which is run by you or the friend who invited you, not by us.
- We do not collect, sell or share your personal data.

## What the app sends, and where

**Your media server.** The app talks to the server address you enter (or the
one an invite code points to). That server holds your username, password
(stored hashed), profiles, watch progress, watchlist and history, and sees
which videos you play. It belongs to whoever runs it; ask them about their
own policies.

**Invite code lookup.** If you type an invite code, the app sends that code to
a small directory service (hosted on Supabase) that answers with the server's
address. Only the code is sent. Codes expire and can be revoked by the server
owner.

**Sign in with Plex (optional).** If you choose "Sign in with Plex", the app
asks plex.tv for a sign-in code, and plex.tv tells the app which servers your
Plex account can use. Plex's own privacy policy applies to that sign-in. The
app keeps only the server address you pick and the sign-in to that server.

**Trailers.** Some screens can play film trailers, which are loaded from
YouTube (youtube-nocookie.com) inside the app. YouTube's privacy policy
applies to those videos. Trailers can be turned off in Settings.

**Artwork.** Posters and backgrounds come from your media server.

## What stays on your device

The server address, your sign-in cookie for that server, and a few display
preferences (for example lip-sync delay and screensaver settings). Uninstalling
the app removes them.

## Deleting your account

Accounts are created on the media server you connect to. To delete yours,
open **Settings** (on a TV) or the profile menu (on a phone or tablet) and
choose **Delete my account**. This deletes the account, its profiles and its
watch history from that server straight away. See
[Deleting your Plexodus account](delete-account.md). You can also ask the
person who runs the server to delete it.

## Children

The app is not directed at children. Server owners can create kids profiles
that only show age-appropriate titles; those profiles collect nothing beyond
what is described above.

## Security

Connections use HTTPS when the server offers it. Passwords are never stored
on the device.

## Changes and contact

We will update this page if anything above changes. Questions: open an issue
at https://github.com/sfxhewitt/plexodus/issues.
