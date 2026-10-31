# What users want beyond Plex

Research notes, October 2026. These notes rank features for Plexodus by **user demand** against **implementation cost**, and filter them through three project constraints:

- **Direct play only.** No transcoding. That removes a large share of Plex's top requests (HEVC transcode, Linux NVIDIA decode, Raspberry Pi transcoding, distributed transcoding), since Plexodus never transcodes.
- **Library on rclone/Google Drive.** Every byte read from a media file is slow and quota-limited. Any feature that reads file contents (intro detection, trickplay thumbnails, probing) must be lazy, opt-in, or triggered by playback.
- **Low resources, and it must look incredible.**

Sources are listed at the end. Demand signals come from the vote-sorted Plex feature-suggestion forum (reply/view counts), Plex redesign backlash threads (2025–2026), Jellyfin comparisons, and the most popular Jellyfin plugins.

## The big picture

1. **People are leaving Plex because of trust, not features.** The 2025 price hike, the paywalled remote streaming, the mandatory plex.tv account, telemetry, and redesigns forced on users with no opt-out drove the migration threads. Plexodus wins by default if it is local-first, account-free, and never rearranges a user's home screen without consent.
2. **The 2025–2026 Plex redesign backlash is a free UI spec.** Users lost the left sidebar with pinned, ordered libraries. They got a horizontal top bar that pushes Discover and Live TV, more clicks per task, season "tiles" that interrupt autoplay, laggy poster scrolling, and no way to switch back. Plex partly reverted after the backlash. The lesson: a **sidebar of pinned libraries, a home screen you control, and fast scrolling** is what people want.
3. **Jellyfin's edge is mostly plugins that should be built in.** The most-loved Jellyfin add-ons are Intro Skipper, Merge Versions, trickplay/chapter thumbnails, Playback Reporting, custom CSS skins, OpenSubtitles, Trakt, LDAP, and "Jellyfin Enhanced" quality-of-life tweaks (keyboard shortcuts, pause-screen media info, random pick, subtitle presets). Plexodus can ship these as first-class features.
4. **Jellyfin's weakness is polish.** Its clients look dated, and remote access needs a reverse proxy. Infuse is often cited as the better-looking front end for Plex and Jellyfin libraries, so a Jellyfin-like feature set with Infuse-like visuals is an open niche.

## Ranked features

Demand: **H** = among the most-discussed requests or the main cause of complaints; **M** = recurring request; **L** = niche.
Cost: **S** = days; **M** = a week or two; **L** = significant subsystem.
Fit notes call out the rclone and direct-play constraints.

### Tier 1: high demand, low cost (build these first)

| Feature | Demand | Cost | Notes |
|---|---|---|---|
| Local accounts, no cloud login, works offline | H | S | Top trust complaint ("built-in local authentication server", "prevent plex.tv outage"). Optional LDAP/OIDC later. |
| Left sidebar with pinned, reorderable libraries | H | S | The single loudest complaint about the Plex redesign. Keep the same layout across web, TV, and mobile. |
| Customizable home screen | H | S | Reorder/hide rows per user, hide watched items, never inject upsell rows (Discover, Live TV, "free movies"). Store layout in the user's account so it follows them across devices. |
| "Recently added" grouped by show | M | S | Show "Severance, 3 new episodes", not 3 separate episode cards. |
| Continue Watching that just works | H | S | Plus "Up Next": the next unwatched episode, landing directly on the episode, never on a season tile. |
| Continuous autoplay, configurable skip-back/forward lengths | H | S | "Continuous Autoplay" and "customize skip-back duration" both rank on page 1 of Plex votes. |
| Audio and subtitle track names, rich media info | H | S | Show real track titles ("Commentary", "SDH"), codec, HDR/Dolby Vision profile, bitrate, file size. Data comes from ffprobe on first play, so it is free after that. Include "ends at 10:42 PM". |
| Multiple versions merged into one title | H | S | Jellyfin needs the Merge Versions plugin for this. Group by TMDB/IMDb id, and let the user choose a version (4K vs 1080p) with full info in the picker. |
| Direct download of any file | M | S | Trivial for a direct-play server: it is an HTTP range request on the original file. Covers "offline downloads" without a sync engine. |
| Fast global search across all libraries | H | S | SQLite FTS5 is already in the stack. Search titles, people, and collections; an actor page shows results from every library. |
| Missing episodes/seasons, missing movies in collections | M | S | Comes from TMDB metadata at no extra I/O cost. Show as greyed placeholders that can be toggled off. |
| Episode ordering (aired, DVD, absolute, TMDB episode groups) | H | M | The top-voted Plex request. Important for anime and for shows such as Firefly. |
| Lock artwork, choose posters, delete posters | M | S | Per-field metadata locks so a refresh never overwrites a manual edit. |
| Keyboard and D-pad shortcuts in the web UI | M | S | Space, arrows, J/K/L, F, S (subtitles), N (next). Jellyfin Enhanced's most-praised feature. |

### Tier 2: high demand, medium cost

| Feature | Demand | Cost | Notes |
|---|---|---|---|
| Native ASS/SSA subtitle rendering in the browser | H | M | Plex burns these in by transcoding, and "Burn subtitles: avoid at all costs" has 25k views. Since Plexodus has no transcoder, a WASM renderer (JASSUB/libass) is mandatory for anime. External .srt/.ass sidecars, subtitle style presets, and an offset slider. |
| Intro and credits skip | H | M | Two-stage approach for rclone: (1) free markers from chapter names ("Intro", "Opening", "Credits"); (2) audio chromaprint fingerprinting, like Jellyfin's Intro Skipper, run **lazily per season when a user first plays it**, reading only the first ~10 minutes and last ~5 minutes of each episode. Users asked for a semi-persistent "auto-skip" toggle. |
| Collections and smart collections | H | M | Auto collections from TMDB (franchises) plus rule-based smart collections (for example "unwatched 4K sci-fi under 2 hours"). Hide collections with one item. Already on Daniel's wish list. |
| Scrubbing previews (trickplay) | H | M–L | Very popular, but generating them reads the whole file. Default: chapter thumbnails only. Generate full trickplay **after a title has been played once**, throttled, and stored as a single sprite sheet per title. |
| Trakt scrobbling and sync | M | M | 268 replies on Plex; a plugin in Jellyfin. Two-way watched-state sync. |
| Playback stats dashboard | M | M | Tautulli and Playback Reporting prove demand. Plexodus logs plays already, so it only needs charts: who watched what, when, on which device, and with which bandwidth. |
| Per-user permissions and profiles | M | M | Managed/kids profiles, library access per user, content-rating limits, optional profile PIN. |
| Share link without an account, with expiry | M | M | Public link to one title or a collection, with an expiry date and play limit. Remote access needs no paid tier. |
| Extras and special features | M | M | Local `Extras/`, `Featurettes/`, and `Behind The Scenes/` folders, plus TMDB trailers as links (not downloads). The extras request has 51k views. |
| Read-ahead buffering for direct play | M | M | "Buffer content YouTube-style" and "parallel connections" are top Plex requests, and they matter more on Google Drive. The server should read ahead in large chunks and cache hot ranges on local disk, so seeking within the buffered window is instant. |
| NFO import and export | M | S–M | Native .nfo reading is a long-standing Plex ask, and it helps migration. |

### Tier 3: lower priority or high cost

| Feature | Demand | Cost | Notes |
|---|---|---|---|
| Watch together (synced playback) | H | L | 442 replies ("Tandem playback"). Direct play makes sync simpler (no transcode sessions), but it still needs real-time sessions. Build it after the core is solid. |
| Native TV apps (Apple TV, Android TV, Roku) | H | L | The polished web UI comes first. A PWA on Android TV via a WebView wrapper is cheap. Infuse can be a stopgap on Apple TV if Plexodus exposes a Jellyfin/Emby-compatible API, which is a cost to consider later. |
| Webhooks and a small plugin API | M | M | Plex killed plugins in 2018. Webhooks (on play, on scan complete) cover most uses at low cost. |
| Music (Plexamp-style) | M | L | Plexamp is the one thing Plex users say Jellyfin can't match. Out of scope for now. |
| Requests (Overseerr/Jellyseerr) | M | M | Daniel does not manage downloads, so this has little value for this library. |
| Live TV and DVR, photos, Sonos, Alexa | L–M | L | Skip. |
| Any transcoding feature | H | — | Out of scope by design. Unplayable files get an "open in VLC/mpv/Infuse" link instead. |

## UI direction

The goal is "Infuse-level beautiful, Jellyfin-level capable, lighter than both".

**Layout (lessons from the Plex backlash)**
- A persistent left sidebar with pinned, user-ordered libraries that collapses to icons. The same model on every screen size and on TV.
- A home screen of rows the user owns: Continue Watching, Up Next, Recently Added per library, collections. Nothing promotional, ever.
- One click from anywhere to any library; never more than two to start playback of the next episode.
- Changes to layout are opt-in. A redesign that removes a workflow ships behind a toggle.

**Visual language**
- Dark-first, cinematic. Full-bleed backdrop hero on detail pages with a slow Ken Burns pan, the title logo (TMDB "logos") instead of plain text, and an accent colour extracted from the poster to tint gradients and buttons per title.
- Large, generous poster grids with soft shadows, subtle hover lift, and focus rings that work for D-pad navigation.
- Smooth motion with restraint: shared-element transitions from poster to detail page, 150–250 ms ease-out, and `prefers-reduced-motion` respected.
- Pause screen with title, episode, synopsis, and media info (a Jellyfin Enhanced favourite).
- Clean typography with one display face for titles and a neutral UI face; tabular numbers for runtimes.

**Speed and resources (what makes it feel premium)**
- Virtualized grids so a 30,000-poster library scrolls at 60 fps. Poster lag was a specific complaint about Plex's new app.
- BlurHash placeholders stored in the DB and returned with list responses, so a grid paints instantly and images fade in.
- Artwork resized once on download into fixed sizes, stored as WebP/AVIF on local disk, served with immutable cache headers. Never fetched from rclone.
- Cursor-paginated APIs with small JSON payloads, and prefetching the detail page on hover or focus.
- Code-split routes; the player loads only on play. Keep the JS bundle small and avoid heavy UI kits.

## Suggested build order

1. Auth (local), sidebar and home layout, library grids with virtualization and BlurHash, search.
2. Player: direct play, track names and media info, continuous autoplay, skip lengths, ASS subtitles, keyboard shortcuts, download.
3. Version merging, missing episodes, episode orders, artwork locks.
4. Intro/credits skip (chapters first, then lazy fingerprinting), collections and smart collections.
5. Stats, Trakt, per-user permissions, share links, read-ahead cache tuning.
6. Watch together, webhooks, TV wrappers.

## Sources

- [Plex forum feature suggestions, sorted by votes (page 1)](https://forums.plex.tv/c/feature-suggestions/8/l/votes?page=1), [page 2](https://forums.plex.tv/c/feature-suggestions/8/l/votes?page=2), [page 9](https://forums.plex.tv/c/feature-suggestions/8/l/votes?page=9)
- [Plex forum: "New UI is an awful experience"](https://forums.plex.tv/t/new-ui-is-an-awful-experience/931048?page=23)
- [PiunikaWeb: Plex Fire TV redesign "unusable", users slam navigation (May 2026)](https://piunikaweb.com/2026/05/04/plex-fire-tv-redesign-unusable-users-slam-navigation/)
- [Neowin: Plex reverts TV app layout after user backlash](https://neowin.net/news/plex-reverts-tv-app-layout-after-intense-user-backlash/)
- [Tech Insider: Jellyfin vs Plex 2026](https://www.tech-insider.org/jellyfin-vs-plex-2026/) (pricing, remote-access paywall, feature gaps; its lifetime price figures are internally inconsistent)
- [JellyWatch: must-have Jellyfin plugins (Intro Skipper, Merge Versions, trickplay)](https://jellywatch.app/blog/jellyfin-intro-skipper-chapters-plugins-quality-of-life-2026)
- [JellyWatch: Jellyfin Enhanced plugin guide](https://jellywatch.app/blog/jellyfin-enhanced-plugin-guide-2026)
- [rclone forum: slow Plex scans on Google Drive](https://forum.rclone.org/t/slow-plex-scanning/14289), [whole-library rescans after adding one file](https://forum.rclone.org/t/adding-file-to-encrypted-google-drive-causes-plex-to-go-through-entire-library/21768)
- [Firecore community: why use Infuse with Plex or Jellyfin](https://community.firecore.com/t/why-use-a-server-such-as-plex-or-jellyfin-with-infuse/48899)
