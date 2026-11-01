package app.plexodus

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.GestureDetector
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import android.view.WindowManager
import android.widget.Button
import android.widget.ImageButton
import android.widget.SeekBar
import android.widget.TextView
import android.widget.Toast
import org.json.JSONArray
import org.json.JSONObject
import org.videolan.libvlc.LibVLC
import org.videolan.libvlc.Media
import org.videolan.libvlc.MediaPlayer
import org.videolan.libvlc.util.VLCVideoLayout
import java.text.DateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.Executors

/**
 * Direct-play video player built on libVLC. It streams the original file,
 * decodes whatever it is, and reports progress to the server the same way
 * the web player does.
 */
class PlayerActivity : Activity() {
    private class Marker(val type: String, val startMs: Long, val endMs: Long)

    private lateinit var libVlc: LibVLC
    private lateinit var player: MediaPlayer
    private lateinit var videoLayout: VLCVideoLayout
    private lateinit var overlay: View
    private lateinit var spinner: View
    private lateinit var errorText: TextView
    private lateinit var playPause: ImageButton
    private lateinit var seek: SeekBar
    private lateinit var timeText: TextView
    private lateinit var durationText: TextView
    private lateinit var endsAt: TextView
    private lateinit var skipButton: Button
    private lateinit var audioButton: Button
    private lateinit var subsButton: Button

    private val ui = Handler(Looper.getMainLooper())
    private val io = Executors.newSingleThreadExecutor()

    private var server = ""
    private var cookie = ""
    private var itemId = 0L
    private var kind = ""
    private var markers = listOf<Marker>()

    private var lengthMs = 0L
    private var timeMs = 0L
    private var playing = false
    private var seeking = false
    private var finished = false
    private var lastReport = 0L
    private var controlsVisible = true
    private var scaleIndex = 0

    private val hideControls = Runnable { if (playing) setControls(false) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        setContentView(R.layout.activity_player)

        server = intent.getStringExtra(EXTRA_SERVER) ?: ""
        cookie = intent.getStringExtra(EXTRA_COOKIE) ?: ""
        itemId = intent.getLongExtra(EXTRA_ITEM_ID, 0)
        kind = intent.getStringExtra(EXTRA_KIND) ?: ""
        val url = intent.getStringExtra(EXTRA_URL) ?: run { finish(); return }
        val startMs = intent.getLongExtra(EXTRA_START_MS, 0)
        lengthMs = intent.getLongExtra(EXTRA_DURATION_MS, 0)
        markers = parseMarkers(intent.getStringExtra(EXTRA_MARKERS))

        videoLayout = findViewById(R.id.video)
        overlay = findViewById(R.id.overlay)
        spinner = findViewById(R.id.spinner)
        errorText = findViewById(R.id.error)
        playPause = findViewById(R.id.play_pause)
        seek = findViewById(R.id.seek)
        timeText = findViewById(R.id.time)
        durationText = findViewById(R.id.duration)
        endsAt = findViewById(R.id.ends_at)
        skipButton = findViewById(R.id.skip)
        audioButton = findViewById(R.id.audio)
        subsButton = findViewById(R.id.subtitles)
        findViewById<TextView>(R.id.title).text = intent.getStringExtra(EXTRA_TITLE)
        findViewById<TextView>(R.id.subtitle).text = intent.getStringExtra(EXTRA_SUBTITLE)

        findViewById<View>(R.id.back).setOnClickListener { finishPlayback(false) }
        playPause.setOnClickListener { togglePlay() }
        findViewById<View>(R.id.rewind).setOnClickListener { seekBy(-10_000) }
        findViewById<View>(R.id.forward).setOnClickListener { seekBy(10_000) }
        audioButton.setOnClickListener { pickTrack(audio = true) }
        subsButton.setOnClickListener { pickTrack(audio = false) }
        findViewById<View>(R.id.aspect).setOnClickListener { cycleAspect() }
        skipButton.setOnClickListener { skipMarker() }

        seek.setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(bar: SeekBar, progress: Int, fromUser: Boolean) {
                if (fromUser) timeText.text = fmt(progress * 1000L)
            }

            override fun onStartTrackingTouch(bar: SeekBar) {
                seeking = true
                ui.removeCallbacks(hideControls)
            }

            override fun onStopTrackingTouch(bar: SeekBar) {
                seeking = false
                player.setTime(bar.progress * 1000L)
                timeMs = bar.progress * 1000L
                scheduleHide()
            }
        })

        // Tap to show or hide the controls; double-tap the sides to skip 10s.
        val gestures = GestureDetector(this, object : GestureDetector.SimpleOnGestureListener() {
            override fun onSingleTapConfirmed(e: MotionEvent): Boolean {
                setControls(!controlsVisible)
                return true
            }

            override fun onDoubleTap(e: MotionEvent): Boolean {
                val w = videoLayout.width
                when {
                    e.x < w / 3f -> seekBy(-10_000)
                    e.x > w * 2 / 3f -> seekBy(10_000)
                    else -> togglePlay()
                }
                return true
            }
        })
        findViewById<View>(R.id.touch).setOnTouchListener { _, ev -> gestures.onTouchEvent(ev); true }

        libVlc = LibVLC(this, arrayListOf("--network-caching=4000", "--http-reconnect", "--audio-time-stretch"))
        player = MediaPlayer(libVlc)
        player.attachViews(videoLayout, null, true, false)
        player.setEventListener { onEvent(it) }

        val media = Media(libVlc, Uri.parse(url))
        media.setHWDecoderEnabled(true, false)
        media.addOption(":network-caching=4000")
        if (startMs > 5_000) media.addOption(":start-time=${startMs / 1000}")
        player.setMedia(media)
        media.release()

        requestAudioFocus()
        player.play()
        updateTime()
        setControls(true)
    }

    // ---- libVLC events (delivered on the main thread) ----

    private fun onEvent(e: MediaPlayer.Event) {
        when (e.type) {
            MediaPlayer.Event.Playing -> {
                playing = true
                spinner.visibility = View.GONE
                playPause.setImageResource(R.drawable.ic_pause)
                scheduleHide()
            }
            MediaPlayer.Event.Paused -> {
                playing = false
                playPause.setImageResource(R.drawable.ic_play)
                report(force = true)
                setControls(true)
            }
            MediaPlayer.Event.Buffering -> spinner.visibility = if (e.buffering < 100f) View.VISIBLE else View.GONE
            MediaPlayer.Event.TimeChanged -> {
                timeMs = e.timeChanged
                updateTime()
                updateSkip()
                report()
            }
            MediaPlayer.Event.LengthChanged -> {
                if (e.lengthChanged > 0) lengthMs = e.lengthChanged
                updateTime()
            }
            MediaPlayer.Event.ESAdded, MediaPlayer.Event.ESSelected -> updateTrackButtons()
            MediaPlayer.Event.EndReached -> finishPlayback(ended = true)
            MediaPlayer.Event.EncounteredError -> {
                spinner.visibility = View.GONE
                errorText.text = getString(R.string.player_error)
                errorText.visibility = View.VISIBLE
                setControls(true)
            }
        }
    }

    // ---- controls ----

    private fun togglePlay() {
        if (player.isPlaying) player.pause() else player.play()
    }

    private fun seekBy(deltaMs: Long) {
        val target = (timeMs + deltaMs).coerceIn(0, if (lengthMs > 0) lengthMs else Long.MAX_VALUE)
        player.setTime(target)
        timeMs = target
        updateTime()
        setControls(true)
    }

    private fun setControls(visible: Boolean) {
        controlsVisible = visible
        overlay.visibility = if (visible) View.VISIBLE else View.GONE
        ui.removeCallbacks(hideControls)
        if (visible) {
            scheduleHide()
        } else {
            hideSystemBars()
        }
    }

    private fun scheduleHide() {
        ui.removeCallbacks(hideControls)
        if (playing && !seeking) ui.postDelayed(hideControls, 3500)
    }

    private fun updateTime() {
        if (!seeking) {
            seek.max = (lengthMs / 1000).toInt().coerceAtLeast(1)
            seek.progress = (timeMs / 1000).toInt()
            timeText.text = fmt(timeMs)
        }
        durationText.text = fmt(lengthMs)
        if (lengthMs > 0 && timeMs <= lengthMs) {
            val at = Date(System.currentTimeMillis() + ((lengthMs - timeMs) / player.rate).toLong())
            endsAt.text = getString(R.string.ends_at, DateFormat.getTimeInstance(DateFormat.SHORT, Locale.getDefault()).format(at))
        }
    }

    private fun activeMarker(): Marker? = markers.firstOrNull { timeMs >= it.startMs && timeMs < it.endMs - 1000 }

    private fun updateSkip() {
        val m = activeMarker()
        val show = m != null && (m.type == "intro" || kind == "episode")
        skipButton.visibility = if (show) View.VISIBLE else View.GONE
        if (m != null) skipButton.text = getString(if (m.type == "intro") R.string.skip_intro else R.string.next_episode)
    }

    private fun skipMarker() {
        val m = activeMarker() ?: return
        if (m.type == "intro" || kind != "episode") {
            player.setTime(m.endMs)
            timeMs = m.endMs
            updateTime()
        } else {
            finishPlayback(ended = true)
        }
    }

    private fun updateTrackButtons() {
        audioButton.visibility = if (player.audioTracksCount > 1) View.VISIBLE else View.GONE
        subsButton.visibility = if (player.spuTracksCount > 0) View.VISIBLE else View.GONE
    }

    private fun pickTrack(audio: Boolean) {
        val tracks = (if (audio) player.audioTracks else player.spuTracks) ?: return
        val current = if (audio) player.audioTrack else player.spuTrack
        val names = tracks.map { it.name }.toTypedArray()
        val selected = tracks.indexOfFirst { it.id == current }
        ui.removeCallbacks(hideControls)
        AlertDialog.Builder(this, android.R.style.Theme_DeviceDefault_Dialog_Alert)
            .setTitle(if (audio) R.string.audio else R.string.subtitles)
            .setSingleChoiceItems(names, selected) { d, i ->
                if (audio) player.setAudioTrack(tracks[i].id) else player.setSpuTrack(tracks[i].id)
                d.dismiss()
            }
            .setOnDismissListener { scheduleHide() }
            .show()
    }

    private fun cycleAspect() {
        val types = MediaPlayer.ScaleType.getMainScaleTypes()
        scaleIndex = (scaleIndex + 1) % types.size
        player.videoScale = types[scaleIndex]
        Toast.makeText(this, types[scaleIndex].name.removePrefix("SURFACE_").replace('_', ' ').lowercase(), Toast.LENGTH_SHORT).show()
    }

    // ---- keyboard / remote control ----

    // Any button press while the controls are up keeps them up, so they don't
    // vanish while you're moving between buttons with the remote.
    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.action == KeyEvent.ACTION_DOWN && controlsVisible) scheduleHide()
        return super.dispatchKeyEvent(event)
    }

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        when (keyCode) {
            KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE, KeyEvent.KEYCODE_SPACE -> togglePlay()
            KeyEvent.KEYCODE_MEDIA_PLAY -> player.play()
            KeyEvent.KEYCODE_MEDIA_PAUSE -> player.pause()
            KeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> seekBy(30_000)
            KeyEvent.KEYCODE_MEDIA_REWIND -> seekBy(-30_000)
            KeyEvent.KEYCODE_DPAD_RIGHT -> if (controlsVisible) return super.onKeyDown(keyCode, event) else seekBy(10_000)
            KeyEvent.KEYCODE_DPAD_LEFT -> if (controlsVisible) return super.onKeyDown(keyCode, event) else seekBy(-10_000)
            KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_DPAD_UP, KeyEvent.KEYCODE_DPAD_DOWN -> {
                if (controlsVisible) return super.onKeyDown(keyCode, event)
                val ok = keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER
                if (ok && skipButton.visibility == View.VISIBLE) {
                    // "Skip intro" is on screen: OK presses it, like Plex.
                    skipMarker()
                } else {
                    setControls(true)
                    playPause.requestFocus()
                }
            }
            KeyEvent.KEYCODE_BACK -> if (controlsVisible && playing) setControls(false) else finishPlayback(false)
            else -> return super.onKeyDown(keyCode, event)
        }
        return true
    }

    // ---- progress reporting, same contract as the web player ----

    private fun report(force: Boolean = false, positionMs: Long = timeMs) {
        if (lengthMs <= 0 || itemId == 0L) return
        val now = System.currentTimeMillis()
        if (!force && now - lastReport < 10_000) return
        lastReport = now
        val body = "{\"position\":${positionMs / 1000.0},\"duration\":${lengthMs / 1000.0}}"
        io.execute {
            try {
                Http.request("$server/api/items/$itemId/progress", "POST", body, mapOf("Cookie" to cookie))
            } catch (_: Exception) {
            }
        }
    }

    private fun finishPlayback(ended: Boolean) {
        if (finished) return
        finished = true
        val position = if (ended) lengthMs else timeMs
        report(force = true, positionMs = position)
        val result = JSONObject()
            .put("itemId", itemId)
            .put("position", position / 1000.0)
            .put("duration", lengthMs / 1000.0)
            .put("ended", ended)
        setResult(RESULT_OK, Intent().putExtra(EXTRA_RESULT, result.toString()))
        finish()
    }

    // ---- lifecycle ----

    override fun onResume() {
        super.onResume()
        hideSystemBars()
    }

    override fun onStop() {
        if (::player.isInitialized && player.isPlaying) player.pause()
        super.onStop()
    }

    override fun onDestroy() {
        ui.removeCallbacksAndMessages(null)
        if (::player.isInitialized) {
            player.setEventListener(null)
            player.stop()
            player.detachViews()
            player.release()
            libVlc.release()
        }
        abandonAudioFocus()
        // Let the last progress POST go out before the executor dies.
        io.shutdown()
        super.onDestroy()
    }

    @Suppress("DEPRECATION")
    private fun hideSystemBars() {
        window.decorView.systemUiVisibility = (View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
            or View.SYSTEM_UI_FLAG_FULLSCREEN
            or View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
            or View.SYSTEM_UI_FLAG_LAYOUT_STABLE
            or View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
            or View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION)
    }

    private var focusRequest: AudioFocusRequest? = null

    private fun requestAudioFocus() {
        val am = getSystemService(Context.AUDIO_SERVICE) as AudioManager
        val req = AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN)
            .setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_MEDIA).setContentType(AudioAttributes.CONTENT_TYPE_MOVIE).build())
            .setOnAudioFocusChangeListener { change ->
                if (change == AudioManager.AUDIOFOCUS_LOSS || change == AudioManager.AUDIOFOCUS_LOSS_TRANSIENT) {
                    if (::player.isInitialized && player.isPlaying) player.pause()
                }
            }
            .build()
        focusRequest = req
        am.requestAudioFocus(req)
    }

    private fun abandonAudioFocus() {
        focusRequest?.let { (getSystemService(Context.AUDIO_SERVICE) as AudioManager).abandonAudioFocusRequest(it) }
    }

    private fun fmt(ms: Long): String {
        val s = (ms / 1000).coerceAtLeast(0)
        val h = s / 3600
        val m = (s % 3600) / 60
        val sec = s % 60
        return if (h > 0) String.format(Locale.US, "%d:%02d:%02d", h, m, sec) else String.format(Locale.US, "%d:%02d", m, sec)
    }

    private fun parseMarkers(json: String?): List<Marker> {
        if (json.isNullOrEmpty()) return emptyList()
        return try {
            val arr = JSONArray(json)
            (0 until arr.length()).map { i ->
                val o = arr.getJSONObject(i)
                Marker(o.optString("type"), (o.optDouble("start", 0.0) * 1000).toLong(), (o.optDouble("end", 0.0) * 1000).toLong())
            }
        } catch (_: Exception) {
            emptyList()
        }
    }

    companion object {
        const val EXTRA_RESULT = "result"
        private const val EXTRA_SERVER = "server"
        private const val EXTRA_COOKIE = "cookie"
        private const val EXTRA_URL = "url"
        private const val EXTRA_TITLE = "title"
        private const val EXTRA_SUBTITLE = "subtitle"
        private const val EXTRA_ITEM_ID = "itemId"
        private const val EXTRA_KIND = "kind"
        private const val EXTRA_START_MS = "startMs"
        private const val EXTRA_DURATION_MS = "durationMs"
        private const val EXTRA_MARKERS = "markers"

        /** Builds the launch intent from the web UI's play request (web/src/native.ts). */
        fun intent(ctx: Context, server: String, cookie: String, req: JSONObject): Intent =
            Intent(ctx, PlayerActivity::class.java)
                .putExtra(EXTRA_SERVER, server)
                .putExtra(EXTRA_COOKIE, cookie)
                .putExtra(EXTRA_URL, req.optString("url"))
                .putExtra(EXTRA_TITLE, req.optString("title"))
                .putExtra(EXTRA_SUBTITLE, req.optString("subtitle"))
                .putExtra(EXTRA_ITEM_ID, req.optLong("itemId"))
                .putExtra(EXTRA_KIND, req.optString("kind"))
                .putExtra(EXTRA_START_MS, (req.optDouble("startSeconds", 0.0) * 1000).toLong())
                .putExtra(EXTRA_DURATION_MS, (req.optDouble("durationSeconds", 0.0) * 1000).toLong())
                .putExtra(EXTRA_MARKERS, req.optJSONArray("markers")?.toString() ?: "[]")
    }
}
