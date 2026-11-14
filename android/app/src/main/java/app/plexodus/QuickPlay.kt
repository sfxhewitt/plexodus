package app.plexodus

import android.net.Uri
import org.json.JSONArray
import org.json.JSONObject

/**
 * "Play this title" without touching the UI:
 *   adb shell am start -a android.intent.action.VIEW -d 'plexodus://play?q=spider-verse&t=1:10:00'
 * Finds the best match in the library and builds the same play request the
 * web UI would (see web/src/tv/play.ts). A show plays its next episode.
 */
object QuickPlay {
    class Request(val query: String, val startSeconds: Double?)

    /** Reads plexodus://play?q=…&t=… (t as seconds, m:ss, h:mm:ss or "start"). */
    fun parse(uri: Uri?): Request? {
        if (uri == null || uri.scheme != "plexodus" || uri.host != "play") return null
        val q = uri.getQueryParameter("q")?.trim().orEmpty()
        if (q.isEmpty()) return null
        return Request(q, parseTime(uri.getQueryParameter("t")))
    }

    fun parseTime(t: String?): Double? {
        val s = t?.trim()?.lowercase() ?: return null
        if (s.isEmpty()) return null
        if (s == "start" || s == "0") return 0.0
        val parts = s.split(":").map { it.toDoubleOrNull() ?: return null }
        return parts.fold(0.0) { acc, p -> acc * 60 + p }
    }

    private fun norm(s: String) = s.lowercase().replace(Regex("[^a-z0-9]+"), " ").trim()

    /** Blocking: searches the server and returns the play request JSON. */
    fun resolve(server: String, cookie: String, req: Request): JSONObject {
        val base = server.trimEnd('/')
        val headers = mapOf("Cookie" to cookie, "Accept" to "application/json")
        fun get(path: String): String {
            val r = Http.request(base + path, headers = headers, timeoutMs = 20_000)
            if (r.code != 200) throw IllegalStateException("GET $path failed (${r.code})")
            return r.body
        }

        val results = JSONObject(get("/api/search?q=" + Uri.encode(req.query)))
        val candidates = mutableListOf<JSONObject>()
        for (key in listOf("movies", "shows", "episodes")) {
            val arr = results.optJSONArray(key) ?: continue
            for (i in 0 until arr.length()) candidates += arr.getJSONObject(i)
        }
        if (candidates.isEmpty()) throw IllegalStateException("Nothing called \"${req.query}\" in the library")
        val q = norm(req.query)
        val words = q.split(" ").filter { it.isNotEmpty() }
        // Exact title first, then titles holding every word, then the server's order.
        val best = candidates.withIndex().maxByOrNull { (i, c) ->
            val t = norm(c.optString("title"))
            val score = when {
                t == q -> 3
                words.all { t.contains(it) } -> 2
                t.contains(q) || q.contains(t) -> 1
                else -> 0
            }
            score * 1000 - i
        }!!.value

        var id = best.getLong("id")
        var kind = best.optString("kind")
        // A show (or season) plays its next episode.
        repeat(2) {
            if (kind == "movie" || kind == "episode") return@repeat
            val d = JSONObject(get("/api/items/$id"))
            val next = d.optJSONObject("onDeck") ?: d.optJSONArray("children")?.optJSONObject(0)
                ?: throw IllegalStateException("Nothing to play in ${d.optString("title")}")
            id = next.getLong("id")
            kind = next.optString("kind")
        }

        val d = JSONObject(get("/api/items/$id"))
        val files = d.optJSONArray("files") ?: JSONArray()
        if (files.length() == 0) throw IllegalStateException("No media file for ${d.optString("title")}")
        val file = files.getJSONObject(0)
        val fileId = file.getLong("id")
        val p = JSONObject(get("/api/play/$fileId?probe=0"))
        val markers = try {
            JSONArray(get("/api/items/$id/markers?recap=1"))
        } catch (_: Exception) {
            JSONArray()
        }
        val m = JSONArray()
        for (i in 0 until markers.length()) {
            val x = markers.getJSONObject(i)
            m.put(JSONObject().put("type", x.optString("type")).put("start", x.optDouble("start")).put("end", x.optDouble("end")))
        }

        val isEp = d.optString("kind") == "episode"
        val title = if (isEp) {
            d.optString("showTitle").ifEmpty { d.optJSONObject("show")?.optString("title").orEmpty() }.ifEmpty { d.optString("title") }
        } else d.optString("title")
        val subtitle = if (isEp) {
            val label = if (d.has("seasonNumber") && d.has("episodeNumber")) "S${d.optInt("seasonNumber")} · E${d.optInt("episodeNumber")}" else ""
            listOf(label, d.optString("title")).filter { it.isNotEmpty() }.joinToString(" · ")
        } else d.optInt("year").takeIf { it > 0 }?.toString().orEmpty()
        val duration = p.optJSONObject("file")?.optDouble("duration", 0.0)?.takeIf { it > 0 } ?: file.optDouble("duration", 0.0)

        return JSONObject()
            .put("itemId", d.getLong("id"))
            .put("kind", d.optString("kind"))
            .put("url", p.optString("externalUrl"))
            .put("title", title)
            .put("subtitle", subtitle)
            .put("overview", d.optString("overview"))
            .put("startSeconds", req.startSeconds ?: p.optDouble("position", 0.0))
            .put("durationSeconds", duration)
            .put("markers", m)
            .put("fileId", fileId)
            .put("audioOffsetMs", p.optInt("audioOffsetMs", 0))
    }
}
