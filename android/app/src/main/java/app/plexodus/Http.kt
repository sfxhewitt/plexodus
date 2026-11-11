package app.plexodus

import java.net.HttpURLConnection
import java.net.URL

/** The little bit of HTTP the shell needs outside the WebView. */
object Http {
    class Result(val code: Int, val body: String, val headers: Map<String?, List<String>> = emptyMap()) {
        /** All values of a response header, matched without regard to case. */
        fun header(name: String): List<String> =
            headers.entries.filter { it.key.equals(name, ignoreCase = true) }.flatMap { it.value }
    }

    fun request(
        url: String,
        method: String = "GET",
        body: String? = null,
        headers: Map<String, String> = emptyMap(),
        timeoutMs: Int = 10_000,
    ): Result {
        val c = URL(url).openConnection() as HttpURLConnection
        try {
            c.requestMethod = method
            c.connectTimeout = timeoutMs
            c.readTimeout = timeoutMs
            c.instanceFollowRedirects = true
            for ((k, v) in headers) c.setRequestProperty(k, v)
            if (body != null) {
                c.doOutput = true
                c.setRequestProperty("Content-Type", "application/json")
                c.outputStream.use { it.write(body.toByteArray()) }
            }
            val code = c.responseCode
            val stream = if (code >= 400) c.errorStream else c.inputStream
            val text = stream?.bufferedReader()?.use { it.readText() } ?: ""
            return Result(code, text, c.headerFields ?: emptyMap())
        } finally {
            c.disconnect()
        }
    }

    /** Fetches a small binary (artwork); null on any failure. */
    fun bytes(url: String, headers: Map<String, String> = emptyMap(), timeoutMs: Int = 10_000): ByteArray? {
        val c = URL(url).openConnection() as HttpURLConnection
        return try {
            c.connectTimeout = timeoutMs
            c.readTimeout = timeoutMs
            c.instanceFollowRedirects = true
            for ((k, v) in headers) c.setRequestProperty(k, v)
            if (c.responseCode != 200) null else c.inputStream.use { it.readBytes() }
        } catch (_: Exception) {
            null
        } finally {
            c.disconnect()
        }
    }
}
