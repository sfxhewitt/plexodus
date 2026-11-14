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

    private const val KEY_SUB_SIZE = "subtitleSize"
    private const val KEY_SUB_BOX = "subtitleBox"

    /** Subtitle size on this device: 0 small, 1 medium (default), 2 large. */
    fun subtitleSize(ctx: Context): Int =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getInt(KEY_SUB_SIZE, 1).coerceIn(0, 2)

    fun setSubtitleSize(ctx: Context, size: Int) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putInt(KEY_SUB_SIZE, size.coerceIn(0, 2)).apply()
    }

    /** Subtitles on a dark box instead of an outline. */
    fun subtitleBox(ctx: Context): Boolean =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getBoolean(KEY_SUB_BOX, false)

    fun setSubtitleBox(ctx: Context, on: Boolean) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putBoolean(KEY_SUB_BOX, on).apply()
    }

    /** The audio or subtitle track name last picked for a show's episodes, if any. */
    fun showTrack(ctx: Context, show: String, audio: Boolean): String? =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getString(showTrackKey(show, audio), null)

    fun setShowTrack(ctx: Context, show: String, audio: Boolean, name: String) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putString(showTrackKey(show, audio), name).apply()
    }

    private fun showTrackKey(show: String, audio: Boolean) = (if (audio) "showAudio:" else "showSubs:") + show.lowercase()

    private const val KEY_PASSTHROUGH = "passthrough"

    /**
     * Send Dolby/DTS untouched to the TV when it says it takes them (default),
     * or always decode here. Off fixes a TV or soundbar that goes silent.
     */
    fun passthrough(ctx: Context): Boolean =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getBoolean(KEY_PASSTHROUGH, true)

    fun setPassthrough(ctx: Context, on: Boolean) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putBoolean(KEY_PASSTHROUGH, on).apply()
    }

    /** Turns whatever the user typed into http(s)://host[:port] without a trailing slash. */
    fun normalize(input: String): String {
        var s = input.trim().trimEnd('/')
        if (s.isNotEmpty() && !s.contains("://")) s = "http://$s"
        return s
    }
}
