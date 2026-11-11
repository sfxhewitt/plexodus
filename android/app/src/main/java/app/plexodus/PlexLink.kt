package app.plexodus

import android.os.Build
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/**
 * Sign in with Plex before we know any server: a plex.tv/link PIN gives a
 * Plex token, plex.tv lists the servers that account can reach, and every
 * Plexodus server adds its own address to its Plex server's custom access
 * URLs. So checking those addresses finds the Plexodus servers this person
 * may use, with nothing typed and no address built into the app.
 */
class PlexLink(private val clientId: String) {
    class Pin(val id: Long, val code: String)

    /** A Plexodus server found through Plex. */
    class Found(val name: String, val owner: String?, val url: String)

    private fun headers(token: String? = null) = buildMap {
        put("Accept", "application/json")
        put("X-Plex-Product", "Plexodus")
        put("X-Plex-Client-Identifier", clientId)
        put("X-Plex-Device-Name", Build.MODEL ?: "Android")
        put("X-Plex-Platform", "Android")
        if (token != null) put("X-Plex-Token", token)
    }

    fun newPin(): Pin {
        val r = Http.request("$PLEX/api/v2/pins?strong=false", "POST", headers = headers())
        if (r.code / 100 != 2) error("plex.tv answered ${r.code}")
        val j = JSONObject(r.body)
        return Pin(j.getLong("id"), j.getString("code"))
    }

    /** The token once the code has been entered at plex.tv/link, else null. Throws when the PIN has expired. */
    fun token(pin: Pin): String? {
        val r = Http.request("$PLEX/api/v2/pins/${pin.id}", headers = headers())
        if (r.code == 404) throw ExpiredException()
        if (r.code / 100 != 2) error("plex.tv answered ${r.code}")
        val j = JSONObject(r.body)
        return if (j.isNull("authToken")) null else j.optString("authToken").ifEmpty { null }
    }

    class ExpiredException : Exception("expired")

    fun username(token: String): String = try {
        val j = JSONObject(Http.request("$PLEX/api/v2/user", headers = headers(token)).body)
        j.optString("username").ifEmpty { j.optString("title") }.ifEmpty { "you" }
    } catch (_: Exception) {
        "you"
    }

    /** Checks every address of every Plex server this account can reach for a Plexodus server. */
    fun findServers(token: String): List<Found> {
        val r = Http.request("$PLEX/api/v2/resources?includeHttps=1&includeRelay=0", headers = headers(token))
        if (r.code / 100 != 2) error("plex.tv answered ${r.code}")
        val resources = JSONArray(r.body)
        class Candidate(val name: String, val owner: String?, val url: String)
        val candidates = mutableListOf<Candidate>()
        val seen = HashSet<String>()
        for (i in 0 until resources.length()) {
            val res = resources.getJSONObject(i)
            if (!res.optString("provides").contains("server")) continue
            val owner = if (res.optBoolean("owned")) null else res.optString("sourceTitle").ifEmpty { null }
            val conns = res.optJSONArray("connections") ?: continue
            for (k in 0 until conns.length()) {
                val c = conns.getJSONObject(k)
                if (c.optBoolean("relay")) continue
                val uri = c.optString("uri").trimEnd('/')
                // Plex's own addresses are *.plex.direct; ours never are.
                if (uri.isEmpty() || uri.contains(".plex.direct")) continue
                if (seen.add(uri.lowercase())) candidates += Candidate(res.optString("name", "Plex server"), owner, uri)
            }
        }
        if (candidates.isEmpty()) return emptyList()
        val pool = Executors.newFixedThreadPool(minOf(16, candidates.size))
        try {
            val checks = candidates.map { c ->
                pool.submit(Callable {
                    try {
                        val s = Http.request("${c.url}/api/status", timeoutMs = 5_000)
                        if (s.code == 200 && JSONObject(s.body).optString("name") == "Plexodus") c else null
                    } catch (_: Exception) {
                        null
                    }
                })
            }
            val found = checks.mapNotNull { runCatching { it.get(12, TimeUnit.SECONDS) }.getOrNull() }
            // One entry per server (a server can publish more than one address).
            return found.distinctBy { it.name + "\u0000" + (it.owner ?: "") }.map { Found(it.name, it.owner, it.url) }
        } finally {
            pool.shutdownNow()
        }
    }

    /** Signs in to a Plexodus server with the Plex token; returns the session cookie or throws with the server's message. */
    fun signIn(server: String, token: String): List<String> {
        val body = JSONObject().put("token", token).toString()
        val r = Http.request("$server/api/auth/plex", "POST", body)
        if (r.code != 200) {
            val msg = runCatching { JSONObject(r.body).optString("error") }.getOrNull().orEmpty()
            error(msg.ifEmpty { "the server answered ${r.code}" })
        }
        return r.header("Set-Cookie")
    }

    companion object {
        private const val PLEX = "https://plex.tv"

    }
}
