package app.plexodus

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Color
import android.os.Handler
import android.os.HandlerThread
import android.os.SystemClock
import android.view.PixelCopy
import android.view.SurfaceView
import android.view.View
import android.view.ViewGroup
import org.json.JSONObject
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.SocketTimeoutException
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.min
import kotlin.math.pow

/**
 * Room light: tints a Govee Wi-Fi light to match what's on screen.
 *
 * About ten times a second it copies the video surface into a tiny bitmap
 * (PixelCopy, so the GPU does the scaling), finds the picture's main colour,
 * smooths it so cuts don't strobe, and sends it to the light over Govee's LAN
 * API (UDP, no cloud, ~20 ms). The light's own state is read when it starts
 * and put back when the player closes.
 *
 * The light needs "LAN Control" switched on in the Govee Home app. It's found
 * by multicast on the first run and its address is remembered.
 *
 * DRM'd video can't be copied; Plexodus plays files directly, so that's fine.
 */
class RoomLight(private val ctx: Context, private val videoRoot: ViewGroup) {
    private val thread = HandlerThread("room-light").apply { start() }
    private val bg = Handler(thread.looper)
    private val frame = Bitmap.createBitmap(SAMPLE_W, SAMPLE_H, Bitmap.Config.ARGB_8888)
    private val pixels = IntArray(SAMPLE_W * SAMPLE_H)
    private var socket: DatagramSocket? = null
    private var light: InetAddress? = null
    private var running = false
    private var released = false
    private var saved: JSONObject? = null

    // Smoothed colour and brightness, and what the light was last sent.
    private var r = 0f; private var g = 0f; private var b = 0f; private var bri = 50f
    private var haveColour = false
    private var sentR = -1; private var sentG = -1; private var sentB = -1; private var sentBri = -1
    private var sentAt = 0L

    /** Starts (or carries on) following the picture. */
    fun resume() {
        bg.post {
            if (released || running) return@post
            if (light == null && !connect()) return@post
            running = true
            bg.post(tick)
        }
    }

    /** Holds the current colour, e.g. while paused. */
    fun pause() {
        bg.post { running = false; bg.removeCallbacks(tick) }
    }

    /** Stops for good and gives the light back the state it had before. */
    fun release() {
        bg.post {
            running = false
            released = true
            bg.removeCallbacks(tick)
            saved?.let { restore(it) }
            socket?.close()
            thread.quitSafely()
        }
    }

    private fun connect(): Boolean {
        val ip = Prefs.roomLightIp(ctx) ?: discover(ctx)?.also { Prefs.setRoomLightIp(ctx, it) } ?: return false
        return try {
            light = InetAddress.getByName(ip)
            socket = DatagramSocket(null).apply {
                reuseAddress = true
                soTimeout = 600
                bind(InetSocketAddress(LISTEN_PORT))
            }
            saved = status()
            send("turn", JSONObject().put("value", 1))
            true
        } catch (_: Exception) {
            socket?.close(); socket = null; light = null
            false
        }
    }

    private val tick: Runnable = object : Runnable {
        override fun run() {
            if (!running) return
            val started = SystemClock.uptimeMillis()
            val surface = findVideoSurface(videoRoot)
            if (surface != null && surface.holder.surface?.isValid == true) {
                try {
                    PixelCopy.request(surface, frame, { result ->
                        if (result == PixelCopy.SUCCESS && running) onFrame()
                    }, bg)
                } catch (_: IllegalArgumentException) {
                    // Surface went away between the check and the copy (player restart).
                }
            }
            bg.postAtTime(this, started + INTERVAL_MS)
        }
    }

    private fun onFrame() {
        frame.getPixels(pixels, 0, SAMPLE_W, 0, 0, SAMPLE_W, SAMPLE_H)
        val (tr, tg, tb, luma) = mainColour(pixels) ?: return
        // Brightness follows the scene: dark scenes dim the room, bright ones lift it.
        val targetBri = (MIN_BRI + (100 - MIN_BRI) * luma.pow(0.7f)).coerceIn(1f, 100f)
        if (!haveColour) {
            r = tr; g = tg; b = tb; bri = targetBri; haveColour = true
        } else {
            // Faster on big changes (a cut), gentler on small drift (no flicker).
            val jump = (abs(tr - r) + abs(tg - g) + abs(tb - b)) / 765f
            val k = if (jump > 0.25f) 0.6f else 0.25f
            r += (tr - r) * k; g += (tg - g) * k; b += (tb - b) * k
            bri += (targetBri - bri) * 0.2f
        }
        push()
    }

    private fun push() {
        val ir = r.toInt(); val ig = g.toInt(); val ib = b.toInt(); val ibri = bri.toInt()
        val now = SystemClock.uptimeMillis()
        val keepAlive = now - sentAt > 2000
        val colourMoved = abs(ir - sentR) + abs(ig - sentG) + abs(ib - sentB) >= 6
        if (colourMoved || keepAlive) {
            send("colorwc", JSONObject()
                .put("color", JSONObject().put("r", ir).put("g", ig).put("b", ib))
                .put("colorTemInKelvin", 0))
            sentR = ir; sentG = ig; sentB = ib; sentAt = now
        }
        if (abs(ibri - sentBri) >= 4 || (keepAlive && ibri != sentBri)) {
            send("brightness", JSONObject().put("value", ibri))
            sentBri = ibri
        }
    }

    private fun send(cmd: String, data: JSONObject) {
        val to = light ?: return
        val body = JSONObject().put("msg", JSONObject().put("cmd", cmd).put("data", data)).toString().toByteArray()
        try { socket?.send(DatagramPacket(body, body.size, to, CONTROL_PORT)) } catch (_: Exception) {}
    }

    /** The light's on/off, brightness and colour, so they can be put back. */
    private fun status(): JSONObject? {
        val s = socket ?: return null
        send("devStatus", JSONObject())
        val buf = ByteArray(2048)
        val until = SystemClock.uptimeMillis() + 800
        while (SystemClock.uptimeMillis() < until) {
            try {
                val p = DatagramPacket(buf, buf.size)
                s.receive(p)
                if (p.address != light) continue
                val msg = JSONObject(String(p.data, 0, p.length)).optJSONObject("msg") ?: continue
                if (msg.optString("cmd") == "devStatus") return msg.optJSONObject("data")
            } catch (_: SocketTimeoutException) { return null } catch (_: Exception) { return null }
        }
        return null
    }

    private fun restore(st: JSONObject) {
        val k = st.optInt("colorTemInKelvin", 0)
        if (k > 0) {
            send("colorwc", JSONObject().put("color", JSONObject().put("r", 0).put("g", 0).put("b", 0)).put("colorTemInKelvin", k))
        } else st.optJSONObject("color")?.let {
            send("colorwc", JSONObject().put("color", it).put("colorTemInKelvin", 0))
        }
        if (st.has("brightness")) send("brightness", JSONObject().put("value", st.optInt("brightness").coerceIn(1, 100)))
        send("turn", JSONObject().put("value", st.optInt("onOff", 1)))
    }

    companion object {
        private const val SAMPLE_W = 32
        private const val SAMPLE_H = 18
        private const val INTERVAL_MS = 100L
        private const val MIN_BRI = 8f
        private const val SCAN_PORT = 4001
        private const val LISTEN_PORT = 4002
        private const val CONTROL_PORT = 4003
        private const val MULTICAST = "239.255.255.250"

        /**
         * The picture's main colour as (r, g, b, overall brightness 0..1).
         * Hues are binned, weighted by how saturated and bright each pixel is,
         * so a small vivid area beats a big grey one, and black bars and dark
         * pixels are ignored. Null when there's nothing usable (a black frame
         * keeps the last colour).
         */
        fun mainColour(px: IntArray): FloatArray? {
            val bins = 24
            val weight = FloatArray(bins); val sr = FloatArray(bins); val sg = FloatArray(bins); val sb = FloatArray(bins)
            var greyW = 0f; var greyR = 0f; var greyG = 0f; var greyB = 0f
            var lumaSum = 0f
            val hsv = FloatArray(3)
            for (c in px) {
                val pr = Color.red(c); val pg = Color.green(c); val pb = Color.blue(c)
                lumaSum += (0.2126f * pr + 0.7152f * pg + 0.0722f * pb) / 255f
                Color.RGBToHSV(pr, pg, pb, hsv)
                val s = hsv[1]; val v = hsv[2]
                if (v < 0.08f) continue
                if (s < 0.15f) {
                    greyW += v; greyR += pr * v; greyG += pg * v; greyB += pb * v
                    continue
                }
                val w = s * s * v
                val i = ((hsv[0] / 360f) * bins).toInt().coerceIn(0, bins - 1)
                weight[i] += w; sr[i] += pr * w; sg[i] += pg * w; sb[i] += pb * w
            }
            val luma = lumaSum / px.size
            // Merge each bin with its neighbours so a hue split across a bin edge still wins.
            var best = -1; var bestW = 0f
            for (i in 0 until bins) {
                val w = weight[i] + 0.5f * (weight[(i + 1) % bins] + weight[(i + bins - 1) % bins])
                if (w > bestW) { bestW = w; best = i }
            }
            val colourful = bestW > 0.004f * px.size
            val out = if (best >= 0 && colourful) {
                var w = 0f; var rr = 0f; var gg = 0f; var bb = 0f
                for (j in -1..1) {
                    val i = (best + j + bins) % bins
                    w += weight[i]; rr += sr[i]; gg += sg[i]; bb += sb[i]
                }
                floatArrayOf(rr / w, gg / w, bb / w)
            } else if (greyW > 0f) {
                // A mostly grey scene: use its (slightly warm) average, not a random hue.
                floatArrayOf(greyR / greyW, greyG / greyW * 0.95f, greyB / greyW * 0.85f)
            } else return null
            // Full-strength colour: the bulb's brightness carries the light level.
            Color.RGBToHSV(out[0].toInt(), out[1].toInt(), out[2].toInt(), hsv)
            hsv[1] = min(1f, hsv[1] * 1.25f)
            hsv[2] = 1f
            val c = Color.HSVToColor(hsv)
            return floatArrayOf(Color.red(c).toFloat(), Color.green(c).toFloat(), Color.blue(c).toFloat(), luma)
        }

        /** libVLC's video SurfaceView inside the VLCVideoLayout (not the subtitle one). */
        fun findVideoSurface(v: View): SurfaceView? {
            if (v is SurfaceView) {
                val name = try { v.resources.getResourceEntryName(v.id) } catch (_: Exception) { "" }
                return if (name.contains("subtitle")) null else v
            }
            if (v is ViewGroup) for (i in 0 until v.childCount) findVideoSurface(v.getChildAt(i))?.let { return it }
            return null
        }

        /**
         * Finds a Govee light on the network with LAN Control on. Blocks for
         * up to ~2 s; call off the main thread. Returns its IP, or null.
         */
        fun discover(ctx: Context): String? {
            val wifi = ctx.applicationContext.getSystemService(Context.WIFI_SERVICE) as? android.net.wifi.WifiManager
            val lock = wifi?.createMulticastLock("plexodus-room-light")?.apply { setReferenceCounted(false) }
            return try {
                lock?.acquire()
                DatagramSocket(null).use { s ->
                    s.reuseAddress = true
                    s.soTimeout = 700
                    s.bind(InetSocketAddress(LISTEN_PORT))
                    val scan = """{"msg":{"cmd":"scan","data":{"account_topic":"reserve"}}}""".toByteArray()
                    val group = InetAddress.getByName(MULTICAST)
                    val buf = ByteArray(2048)
                    repeat(3) {
                        s.send(DatagramPacket(scan, scan.size, group, SCAN_PORT))
                        try {
                            val p = DatagramPacket(buf, buf.size)
                            s.receive(p)
                            val data = JSONObject(String(p.data, 0, p.length)).optJSONObject("msg")?.optJSONObject("data")
                            val ip = data?.optString("ip")?.takeIf { it.isNotEmpty() } ?: p.address.hostAddress
                            return ip
                        } catch (_: SocketTimeoutException) {}
                    }
                    null
                }
            } catch (_: Exception) {
                null
            } finally {
                lock?.release()
            }
        }
    }
}
