package app.plexodus

import org.json.JSONObject

/**
 * Invite codes: a Plexodus server registers each code it hands out in a
 * small public directory (code -> server address), so a friend types only
 * the code. The directory key is a publishable one; it can only call the
 * register/resolve/revoke functions.
 */
object InviteCodes {
    // Same directory as the server's internal/api/invitecodes.go. The key is
    // Supabase's publishable (client-side) key, meant to ship in apps.
    private const val DIRECTORY = "https://samxshajnvjyarvuqfsf.supabase.co/rest/v1/rpc/plexodus_resolve"
    private const val KEY = "sb_publishable_wJCyACOz0jtPh-KjUd9f5w_r8JJOr4o"

    private val shape = Regex("^[A-Z2-9]{8}$")

    /** The code in canonical form ("ABCD-2345" -> "ABCD2345"), or null when the text isn't one. */
    fun normalize(text: String): String? {
        val t = text.trim().replace("-", "").replace(" ", "").uppercase()
        return if (shape.matches(t)) t else null
    }

    /** The server address for a code, or null when the code is unknown or expired. */
    fun resolve(code: String): String? {
        val body = JSONObject().put("p_code", code).toString()
        val r = Http.request(DIRECTORY, "POST", body, mapOf("apikey" to KEY))
        if (r.code != 200) throw IllegalStateException("directory answered ${r.code}")
        // PostgREST answers a text function with a JSON string, or null.
        val v = JSONObject("{\"v\":${r.body.trim().ifEmpty { "null" }}}")
        return if (v.isNull("v")) null else v.getString("v").ifEmpty { null }
    }
}
