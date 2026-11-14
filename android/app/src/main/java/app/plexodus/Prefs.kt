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

    private const val KEY_ROOM_LIGHT = "roomLight"
    private const val KEY_ROOM_LIGHT_IP = "roomLightIp"

    /** Room light: tint a Govee light to match the picture while playing. Off until turned on. */
    fun roomLight(ctx: Context): Boolean =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getBoolean(KEY_ROOM_LIGHT, false)

    fun setRoomLight(ctx: Context, on: Boolean) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putBoolean(KEY_ROOM_LIGHT, on).apply()
    }

    /** The Govee light's IP, found by a LAN scan the first time Room light is turned on. */
    fun roomLightIp(ctx: Context): String? =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getString(KEY_ROOM_LIGHT_IP, null)

    fun setRoomLightIp(ctx: Context, ip: String?) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putString(KEY_ROOM_LIGHT_IP, ip).apply()
    }

    /** A Govee light's room name ("Living Room"), keyed by its IP or "ble:" MAC. */
    fun lightName(ctx: Context, addr: String): String? =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getString("lightName:" + addr.uppercase(), null)

    fun setLightName(ctx: Context, addr: String, name: String?) {
        val e = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit()
        if (name.isNullOrBlank()) e.remove("lightName:" + addr.uppercase()) else e.putString("lightName:" + addr.uppercase(), name.trim())
        e.apply()
    }

    /** Govee developer API key, used only to read the lights' names from the Govee account. */
    fun goveeKey(ctx: Context): String? =
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).getString("goveeKey", null)

    fun setGoveeKey(ctx: Context, key: String?) {
        ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putString("goveeKey", key?.trim()?.takeIf { it.isNotEmpty() }).apply()
    }

    /** Turns whatever the user typed into http(s)://host[:port] without a trailing slash. */
    fun normalize(input: String): String {
        var s = input.trim().trimEnd('/')
        if (s.isNotEmpty() && !s.contains("://")) s = "http://$s"
        return s
    }
}
