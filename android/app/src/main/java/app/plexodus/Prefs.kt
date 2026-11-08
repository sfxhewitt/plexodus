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

    /** Turns whatever the user typed into http(s)://host[:port] without a trailing slash. */
    fun normalize(input: String): String {
        var s = input.trim().trimEnd('/')
        if (s.isNotEmpty() && !s.contains("://")) s = "http://$s"
        return s
    }
}
