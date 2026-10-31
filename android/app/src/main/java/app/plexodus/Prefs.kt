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

    /** Turns whatever the user typed into http(s)://host[:port] without a trailing slash. */
    fun normalize(input: String): String {
        var s = input.trim().trimEnd('/')
        if (s.isNotEmpty() && !s.contains("://")) s = "http://$s"
        return s
    }
}
