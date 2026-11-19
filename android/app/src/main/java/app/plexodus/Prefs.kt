package app.plexodus

import android.content.Context

object Prefs {
    private const val FILE = "plexodus"
    private const val KEY_SERVER = "server"

    fun server(ctx: Context): String? =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getString(KEY_SERVER, null)

    fun setServer(ctx: Context, url: String?) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putString(KEY_SERVER, url).apply()
    }

    private const val KEY_CLIENT_ID = "plexClientId"

    /** This install's id towards plex.tv (Plex lists the app under it). */
    fun clientId(ctx: Context): String {
        val p = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        p.getString(KEY_CLIENT_ID, null)?.let { return it }
        val id = "plexodus-app-" + java.util.UUID.randomUUID().toString()
        p.edit().putString(KEY_CLIENT_ID, id).apply()
        return id
    }

    private const val KEY_AUDIO_DELAY = "audioDelayMs"

    /**
     * This device's own lip sync offset in ms (positive plays the sound
     * later), for delay the TV or soundbar adds. Set by the calibration
     * screen and added to every file's own offset.
     */
    fun audioDelay(ctx: Context): Int =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getInt(KEY_AUDIO_DELAY, 0)

    fun setAudioDelay(ctx: Context, ms: Int) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putInt(KEY_AUDIO_DELAY, ms.coerceIn(-MAX_DELAY, MAX_DELAY)).apply()
    }

    const val MAX_DELAY = 5000

    private const val KEY_DISPLAY_LAG_MIGRATED = "displayLagMigrated"

    /**
     * The player now makes up for the picture reaching the screen a refresh
     * later than libVLC thinks (see PlayerActivity.displayLagMs). Offsets set
     * before that, by calibration at the 60 Hz menu rate, already held that
     * part, so take it out once to keep them where they were.
     */
    fun migrateDisplayLag(ctx: Context, lagAt60Ms: Int) {
        val p = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        if (p.getBoolean(KEY_DISPLAY_LAG_MIGRATED, false)) return
        val old = p.getInt(KEY_AUDIO_DELAY, 0)
        val e = p.edit().putBoolean(KEY_DISPLAY_LAG_MIGRATED, true)
        if (old != 0) e.putInt(KEY_AUDIO_DELAY, (old - lagAt60Ms).coerceIn(-MAX_DELAY, MAX_DELAY))
        e.apply()
    }

    /** The audio or subtitle track name last picked for a show's episodes, if any. */
    fun showTrack(ctx: Context, show: String, audio: Boolean): String? =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getString(showTrackKey(show, audio), null)

    fun setShowTrack(ctx: Context, show: String, audio: Boolean, name: String) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putString(showTrackKey(show, audio), name).apply()
    }

    private fun showTrackKey(show: String, audio: Boolean) = (if (audio) "showAudio:" else "showSubs:") + show.lowercase()

    /** Turns whatever the user typed into http(s)://host[:port] without a trailing slash. */
    fun normalize(input: String): String {
        var s = input.trim().trimEnd('/')
        if (s.isNotEmpty() && !s.contains("://")) s = "http://$s"
        return s
    }
}
