package app.plexodus

import android.app.Activity
import android.app.Dialog
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.content.IntentFilter
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.media.AudioDeviceCallback
import android.media.AudioDeviceInfo
import android.media.AudioFormat
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.view.Display
import android.view.GestureDetector
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import android.view.WindowManager
import android.widget.Button
import android.widget.ImageButton
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.SeekBar
import android.widget.TextView
import android.widget.Toast
import org.json.JSONArray
import org.json.JSONObject
import org.videolan.libvlc.LibVLC
import org.videolan.libvlc.Media
import org.videolan.libvlc.MediaPlayer
import org.videolan.libvlc.util.VLCVideoLayout
import java.net.URLEncoder
import java.text.DateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.Executors
import kotlin.math.abs

/**
 * Direct-play video player built on libVLC. It streams the original file,
 * decodes whatever it is, and reports progress to the server the same way
 * the web player does.
 */
class PlayerActivity : Activity() {
    private class Marker(val type: String, val startMs: Long, val endMs: Long)

    /** The episode after the one playing, looked up in the background. */
    private class NextEpisode(
        val id: Long,
        val title: String,
        val showTitle: String,
        val season: Int,
        val episode: Int,
        val art: Bitmap?,
    )

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
    private lateinit var statsText: TextView
    private var statsVisible = false
    private var lastReadBytes = -1
    private var passthrough = false
    /** The HDMI sink takes Dolby/DTS, so passthrough is on whenever headphones aren't. */
    private var hdmiSurround = false
    /** libVLC is mixing down to stereo for headphones (see [STEREO_FOR_HEADPHONES]). */
    private var stereoOut = false
    private var modeSwitched = false
    private lateinit var overviewText: TextView
    private val modeRetry = Any()

    private val ui = Handler(Looper.getMainLooper())
    private val io = Executors.newSingleThreadExecutor()

    private var server = ""
    private var cookie = ""
    private var itemId = 0L
    private var fileId = 0L
    /** People whose Plex Watchlist Share can add to (id, name); only for admins. */
    @Volatile private var shareTargets: List<Pair<String, String>> = emptyList()

    // ---- lip sync ----
    /** This device's offset (TV/soundbar delay), from TV Settings. */
    private var deviceDelayMs = 0
    /** The file's own fix, saved on the server. Positive plays the sound later. */
    private var fileDelayMs = 0
    /** Flash-and-beep calibration of deviceDelayMs instead of a film. */
    private var calibrating = false
    /** Calibration by a phone filming the TV: a QR code, then the measured fix is applied. */
    private var phoneMode = false
    private var phoneCode = ""
    private var phoneRound = 0
    private lateinit var phoneStatus: TextView
    private lateinit var syncButton: Button
    /** The Audio sync bar is up: the remote adjusts the sound, not the film. */
    private var syncMode = false
    private var syncStart = 0
    private lateinit var syncBar: TextView
    private lateinit var calibrationText: TextView
    /** Phones and tablets: on-screen buttons for Audio sync and calibration. */
    private lateinit var adjustButtons: LinearLayout
    /** A touch screen and no remote: Audio sync and calibration get buttons. */
    private val touchScreen: Boolean by lazy {
        val ui = getSystemService(UI_MODE_SERVICE) as android.app.UiModeManager
        val tv = ui.currentModeType == android.content.res.Configuration.UI_MODE_TYPE_TELEVISION ||
            packageManager.hasSystemFeature(android.content.pm.PackageManager.FEATURE_LEANBACK) ||
            packageManager.hasSystemFeature("amazon.hardware.fire_tv")
        !tv && packageManager.hasSystemFeature(android.content.pm.PackageManager.FEATURE_TOUCHSCREEN)
    }
    private var kind = ""
    private var markers = listOf<Marker>()

    private var lengthMs = 0L
    private var timeMs = 0L
    private var playing = false
    private var seeking = false
    /** A finger is dragging the seek bar (touch screens). */
    private var touchSeeking = false
    /** Where the remote has moved the seek bar to, before the jump is made; -1 when idle. */
    private var scrubMs = -1L
    private val commitScrub = Runnable { commitSeek() }
    /**
     * libVLC has started this file. Until then timeMs is only the resume
     * point we asked for, so nothing is reported: backing out of a film that
     * is still loading must not wipe out where you were.
     */
    private var started = false
    /** Resume point to check once the first time arrives, in case the start-time option was ignored. */
    private var pendingStartMs = 0L
    private var finished = false
    private var lastReport = 0L
    private var controlsVisible = true
    private var scaleIndex = 0

    private val hideControls = Runnable { if (playing) setControls(false) }

    // ---- up next / still watching ----
    private lateinit var upNext: View
    private lateinit var upNextImage: ImageView
    private lateinit var upNextCount: TextView
    private lateinit var upNextEyebrow: TextView
    private lateinit var upNextTitle: TextView
    private lateinit var upNextSub: TextView
    private lateinit var upNextPlay: Button
    private lateinit var upNextCancel: Button
    private lateinit var root: View

    /** The item the page asked to play; the result always names it. */
    private var startItemId = 0L
    private var next: NextEpisode? = null
    private var lookupFor = 0L
    private var upNextVisible = false
    private var upNextDismissed = false
    private var askingStillWatching = false
    private var countdown = 0
    /** A button was pressed while "Up next in N" counted down: wait for a choice. */
    private var countdownStopped = false
    private var switching = false
    /** The player already offered (or played) the next episode, so the page shouldn't. */
    private var handledNext = false
    /** Episodes started by the countdown since the remote was last touched. */
    private var autoPlays = 0

    private val tick = object : Runnable {
        override fun run() {
            if (!upNextVisible || askingStillWatching) return
            if (playing) countdown--
            if (countdown <= 0) {
                playNext(auto = true)
                return
            }
            upNextCount.text = countdown.toString()
            upNextEyebrow.text = getString(R.string.up_next_in, countdown)
            ui.postDelayed(this, 1000)
        }
    }

    /** Nobody answered "still watching?": give up and let the TV sleep. */
    private val giveUp = Runnable { finishPlayback(false) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        setContentView(R.layout.activity_player)

        server = intent.getStringExtra(EXTRA_SERVER) ?: ""
        cookie = intent.getStringExtra(EXTRA_COOKIE) ?: ""
        itemId = intent.getLongExtra(EXTRA_ITEM_ID, 0)
        startItemId = itemId
        calibrating = intent.getBooleanExtra(EXTRA_CALIBRATE, false)
        phoneMode = calibrating && intent.getBooleanExtra(EXTRA_PHONE, false)
        deviceDelayMs = Prefs.audioDelay(this)
        fileDelayMs = intent.getIntExtra(EXTRA_AUDIO_OFFSET_MS, 0)
        kind = intent.getStringExtra(EXTRA_KIND) ?: ""
        val url = intent.getStringExtra(EXTRA_URL) ?: run { finish(); return }
        fileId = intent.getLongExtra(EXTRA_FILE_ID, 0).takeIf { it > 0 } ?: fileIdFromUrl(url)
        val startMs = intent.getLongExtra(EXTRA_START_MS, 0)
        lengthMs = intent.getLongExtra(EXTRA_DURATION_MS, 0)
        markers = parseMarkers(intent.getStringExtra(EXTRA_MARKERS))
        loadShareTargets()

        videoLayout = findViewById(R.id.video)
        overlay = findViewById(R.id.overlay)
        spinner = findViewById(R.id.spinner)
        errorText = findViewById(R.id.error)
        // TVs get Netflix/Apple TV style controls: no buttons over the picture, play/pause
        // shown at the start of the bar, the remote on the progress line. Touch screens
        // keep the big rewind / play / forward in the middle.
        val barPlay = findViewById<ImageButton>(R.id.play_pause_bar)
        if (touchScreen) {
            playPause = findViewById(R.id.play_pause)
        } else {
            findViewById<View>(R.id.center_controls).visibility = View.GONE
            findViewById<View>(R.id.back).visibility = View.GONE
            barPlay.visibility = View.VISIBLE
            barPlay.isFocusable = false
            playPause = barPlay
        }
        seek = findViewById(R.id.seek)
        timeText = findViewById(R.id.time)
        durationText = findViewById(R.id.duration)
        endsAt = findViewById(R.id.ends_at)
        skipButton = findViewById(R.id.skip)
        audioButton = findViewById(R.id.audio)
        subsButton = findViewById(R.id.subtitles)
        root = findViewById(R.id.root)
        upNext = findViewById(R.id.up_next)
        upNextImage = findViewById(R.id.up_next_image)
        upNextCount = findViewById(R.id.up_next_count)
        upNextEyebrow = findViewById(R.id.up_next_eyebrow)
        upNextTitle = findViewById(R.id.up_next_title)
        upNextSub = findViewById(R.id.up_next_sub)
        upNextPlay = findViewById(R.id.up_next_play)
        upNextCancel = findViewById(R.id.up_next_cancel)
        upNextPlay.setOnClickListener { playNext(auto = false) }
        upNextCancel.setOnClickListener { if (askingStillWatching) finishPlayback(false) else dismissUpNext() }
        findViewById<TextView>(R.id.title).text = intent.getStringExtra(EXTRA_TITLE)
        findViewById<TextView>(R.id.subtitle).text = intent.getStringExtra(EXTRA_SUBTITLE)
        overviewText = findViewById(R.id.overview)
        overviewText.text = intent.getStringExtra(EXTRA_OVERVIEW) ?: ""

        findViewById<View>(R.id.back).setOnClickListener { finishPlayback(false) }
        playPause.setOnClickListener { togglePlay() }
        findViewById<View>(R.id.rewind).setOnClickListener { seekBy(-10_000) }
        findViewById<View>(R.id.forward).setOnClickListener { seekBy(10_000) }
        audioButton.setOnClickListener { showTrackPanel(subtitles = false) }
        subsButton.setOnClickListener { showTrackPanel(subtitles = true) }
        for (b in listOf<View>(subsButton, audioButton, findViewById(R.id.sync), findViewById(R.id.stats_button), findViewById(R.id.aspect), skipButton)) {
            if (b is TextView) shrinkIcons(b, 14)
            // A small lift on focus; transform only, so it stays smooth on the Cube.
            b.setOnFocusChangeListener { v, has -> v.animate().scaleX(if (has) 1.06f else 1f).scaleY(if (has) 1.06f else 1f).setDuration(120).start() }
        }
        findViewById<View>(R.id.aspect).setOnClickListener { cycleAspect() }
        statsText = findViewById(R.id.stats)
        findViewById<View>(R.id.stats_button).setOnClickListener { toggleStats() }
        skipButton.setOnClickListener { skipMarker() }
        syncButton = findViewById(R.id.sync)
        syncButton.setOnClickListener { showAudioSync() }
        if (fileId == 0L) syncButton.visibility = View.GONE
        calibrationText = findViewById(R.id.calibration)
        syncBar = findViewById(R.id.sync_bar)
        adjustButtons = findViewById(R.id.adjust_buttons)
        findViewById<View>(R.id.adjust_big_down).setOnClickListener { adjust(-bigStep()) }
        findViewById<View>(R.id.adjust_down).setOnClickListener { adjust(-step()) }
        findViewById<View>(R.id.adjust_up).setOnClickListener { adjust(step()) }
        findViewById<View>(R.id.adjust_big_up).setOnClickListener { adjust(bigStep()) }
        findViewById<View>(R.id.adjust_reset).setOnClickListener { adjust(if (calibrating) -deviceDelayMs else -fileDelayMs) }
        findViewById<View>(R.id.adjust_cancel).setOnClickListener { if (calibrating) endCalibration(save = false) else closeSync(keep = false) }
        findViewById<View>(R.id.adjust_done).setOnClickListener { if (calibrating) endCalibration(save = true) else closeSync(keep = true) }

        seek.setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(bar: SeekBar, progress: Int, fromUser: Boolean) {
                if (!fromUser) return
                timeText.text = fmt(progress * 1000L)
                showScrubPreview(progress * 1000L)
                if (!touchSeeking) {
                    // Moved by a key the player didn't catch itself: jump once it settles.
                    seeking = true
                    scrubMs = progress * 1000L
                    ui.removeCallbacks(hideControls)
                    ui.removeCallbacks(commitScrub)
                    ui.postDelayed(commitScrub, SCRUB_SETTLE_MS)
                }
            }

            override fun onStartTrackingTouch(bar: SeekBar) {
                touchSeeking = true
                seeking = true
                ui.removeCallbacks(hideControls)
            }

            override fun onStopTrackingTouch(bar: SeekBar) {
                touchSeeking = false
                scrubMs = bar.progress * 1000L
                commitSeek()
            }
        })

        // Tap to show or hide the controls; double-tap the sides to skip 10s.
        val gestures = GestureDetector(this, object : GestureDetector.SimpleOnGestureListener() {
            override fun onSingleTapConfirmed(e: MotionEvent): Boolean {
                // Phones and tablets have buttons for these; a stray tap on the film does nothing.
                if ((syncMode || calibrating) && touchScreen) return true
                if (syncMode) {
                    // Touch: left or right third nudges the sound, the middle keeps it.
                    val w = videoLayout.width
                    when {
                        e.x < w / 3f -> nudgeSync(-SYNC_STEP_MS)
                        e.x > w * 2 / 3f -> nudgeSync(SYNC_STEP_MS)
                        else -> closeSync(keep = true)
                    }
                    return true
                }
                if (calibrating) {
                    // Touch screens: tap the left or right third to nudge, the middle to save.
                    val w = videoLayout.width
                    when {
                        e.x < w / 3f -> nudgeCalibration(-CALIBRATION_STEP_MS)
                        e.x > w * 2 / 3f -> nudgeCalibration(CALIBRATION_STEP_MS)
                        else -> onKeyDown(KeyEvent.KEYCODE_ENTER, KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_ENTER))
                    }
                    return true
                }
                setControls(!controlsVisible)
                return true
            }

            override fun onDoubleTap(e: MotionEvent): Boolean {
                if (calibrating || syncMode) return true
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

        // Send Dolby/DTS untouched to the TV or receiver when it says it can
        // take them (like Plex's "passthrough"); otherwise decode to PCM.
        // Bluetooth or wired headphones can't take a bitstream: it would go out of the
        // TV's speakers instead, so decode while they're connected, and mix down to
        // stereo for them (see STEREO_FOR_HEADPHONES).
        // Not before Android 11 (so not on the Fire TV Cube): see PASSTHROUGH_CLOSES_CLEANLY.
        hdmiSurround = hdmiTakesSurround()
        passthrough = PASSTHROUGH_CLOSES_CLEANLY && hdmiSurround && !headphonesConnected()
        createPlayer()
        if (hdmiSurround) (getSystemService(Context.AUDIO_SERVICE) as AudioManager).registerAudioDeviceCallback(deviceWatch, ui)

        requestAudioFocus()
        timeMs = if (startMs > 5_000) startMs else 0L
        load(url, startMs)
        loadThumbs()
        if (calibrating) {
            overlay.visibility = View.GONE
            controlsVisible = false
            if (phoneMode) startPhoneSync() else updateCalibration()
        } else {
            setControls(true)
        }
    }

    private fun createPlayer() {
        val opts = arrayListOf("--network-caching=4000", "--http-reconnect", "--audio-time-stretch")
        // Subtitle look (size, font, edge, box, position): see SubtitleStyle.
        opts += SubtitleStyle.load(this).vlcOptions(resources.displayMetrics.heightPixels)
        if (hdmiSurround) opts += "--aout=audiotrack"
        opts += if (passthrough) "--spdif" else "--no-spdif"
        libVlc = LibVLC(this, opts)
        player = MediaPlayer(libVlc)
        stereoOut = hdmiSurround && headphonesConnected() && player.setAudioOutputDevice(STEREO_FOR_HEADPHONES)
        player.attachViews(videoLayout, null, true, false)
        player.setEventListener { onEvent(it) }
    }

    /**
     * Subtitle size and background are libVLC start-up options, so a change
     * rebuilds the player and carries on from the same moment and tracks.
     */
    private fun restartPlayer() {
        val at = timeMs
        val audio = player.audioTrack
        val spu = player.spuTrack
        player.setEventListener(null)
        player.stop()
        player.detachViews()
        player.release()
        libVlc.release()
        playing = false
        modeSwitched = true // the display already runs at the film's rate
        createPlayer()
        restoreTracks = Pair(audio, spu)
        load(mediaUrl, at)
    }

    private var restoreTracks: Pair<Int, Int>? = null
    private var mediaUrl = ""

    private fun load(url: String, startMs: Long) {
        mediaUrl = url
        val media = Media(libVlc, Uri.parse(url))
        media.setHWDecoderEnabled(true, false)
        media.addOption(":network-caching=4000")
        if (startMs > 5_000) media.addOption(":start-time=${startMs / 1000}")
        started = false
        pendingStartMs = if (startMs > 5_000 && !calibrating) startMs else 0L
        lastReport = 0L
        // The calibration clip is 10 s; it's restarted at its end (EndReached)
        // rather than with :input-repeat, after which the Cube showed no picture.
        player.setMedia(media)
        media.release()
        player.play()
        updateTime()
        // The server looks for recaps (and intros Plex didn't mark) once
        // playback starts; pick up what it finds in the first minutes.
        ui.removeCallbacksAndMessages(markerRefresh)
        val id = itemId
        for (after in longArrayOf(15_000, 40_000, 90_000)) {
            ui.postAtTime({ refreshMarkers(id) }, markerRefresh, SystemClock.uptimeMillis() + after)
        }
    }

    private val markerRefresh = Any()

    private fun refreshMarkers(id: Long) {
        if (finished || id == 0L || id != itemId || server.isEmpty()) return
        io.execute {
            try {
                val r = Http.request("$server/api/items/$id/markers?recap=1", headers = mapOf("Cookie" to cookie))
                if (r.code == 200) {
                    val fresh = parseMarkers(r.body)
                    ui.post {
                        if (!finished && id == itemId) {
                            markers = fresh
                            updateSkip()
                        }
                    }
                }
            } catch (_: Exception) {
            }
        }
    }

    // ---- libVLC events (delivered on the main thread) ----

    private fun onEvent(e: MediaPlayer.Event) {
        when (e.type) {
            MediaPlayer.Event.Playing -> {
                playing = true
                started = true
                overviewText.visibility = View.GONE
                spinner.visibility = View.GONE
                restoreTracks?.let { (a, sp) ->
                    restoreTracks = null
                    if (a >= 0) player.setAudioTrack(a)
                    player.setSpuTrack(sp)
                } ?: applyShowTracks()
                showAudioBadge()
                applyAudioDelay()
                if (calibrating) return
                playPause.setImageResource(R.drawable.ic_pause)
                scheduleHide()
                lookUpNext()
                matchRefreshRate()
            }
            MediaPlayer.Event.Paused -> {
                playing = false
                // Apple TV style: pausing brings up what you're watching.
                if (!calibrating && overviewText.text.isNotEmpty()) overviewText.visibility = View.VISIBLE
                playPause.setImageResource(R.drawable.ic_play)
                report(force = true)
                if (!upNextVisible && !calibrating && !syncMode) setControls(true)
            }
            MediaPlayer.Event.Buffering -> spinner.visibility = if (e.buffering < 100f) View.VISIBLE else View.GONE
            MediaPlayer.Event.TimeChanged -> {
                if (pendingStartMs > 0) {
                    val want = pendingStartMs
                    pendingStartMs = 0L
                    if (e.timeChanged < want - 10_000) {
                        // The resume point didn't take (some streams ignore
                        // start-time): jump there now, and don't report the start.
                        player.setTime(want)
                        timeMs = want
                        updateTime()
                        return
                    }
                }
                timeMs = e.timeChanged
                if (calibrating) return
                updateTime()
                updateSkip()
                updateUpNext()
                report()
            }
            MediaPlayer.Event.LengthChanged -> {
                if (e.lengthChanged > 0) lengthMs = e.lengthChanged
                updateTime()
            }
            MediaPlayer.Event.ESAdded, MediaPlayer.Event.ESSelected -> updateTrackButtons()
            // Not for the calibration clip: switching the TV to 50 Hz for it blanked
            // the picture on the Fire TV Cube while the beeps carried on.
            MediaPlayer.Event.Vout -> if (e.voutCount > 0 && !calibrating) matchRefreshRate()
            MediaPlayer.Event.EndReached -> if (calibrating) ui.post { if (!finished) load(mediaUrl, 0) } else onEnd()
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

    /**
     * The remote moves the seek bar: each press 10 s, holding it speeds up.
     * The player jumps once the presses stop (or on OK), like Plex and Netflix.
     */
    private fun scrubBy(dir: Int, repeat: Int) {
        if (lengthMs <= 0) return
        if (scrubMs < 0) scrubMs = timeMs
        val step = when {
            repeat < 4 -> 10_000L
            repeat < 20 -> 30_000L
            else -> maxOf(60_000L, lengthMs / 60)
        }
        scrubMs = (scrubMs + dir * step).coerceIn(0, lengthMs)
        seeking = true
        ui.removeCallbacks(hideControls)
        seek.max = (lengthMs / 1000).toInt().coerceAtLeast(1)
        seek.progress = (scrubMs / 1000).toInt()
        timeText.text = fmt(scrubMs)
        showScrubPreview(scrubMs)
        ui.removeCallbacks(commitScrub)
        ui.postDelayed(commitScrub, SCRUB_SETTLE_MS)
    }

    /** Makes the jump the seek bar has been moved to. */
    private fun commitSeek() {
        ui.removeCallbacks(commitScrub)
        val target = scrubMs
        scrubMs = -1L
        seeking = false
        if (target >= 0) {
            player.setTime(target)
            timeMs = target
            report(force = true)
        }
        updateTime()
        scheduleHide()
    }

    private fun seekBy(deltaMs: Long) {
        val target = (timeMs + deltaMs).coerceIn(0, if (lengthMs > 0) lengthMs else Long.MAX_VALUE)
        player.setTime(target)
        timeMs = target
        updateTime()
        setControls(true)
        showScrubPreview(target)
    }

    private fun setControls(visible: Boolean) {
        controlsVisible = visible
        overlay.visibility = if (visible) View.VISIBLE else View.GONE
        ui.removeCallbacks(hideControls)
        if (visible) {
            if (currentFocus?.isShown != true) focusHome()
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
        // Credits only get a button when there is no up-next card for them.
        val show = m != null && !upNextVisible && (m.type != "credits" || (kind == "episode" && next == null))
        skipButton.visibility = if (show) View.VISIBLE else View.GONE
        if (m != null) {
            skipButton.text = getString(
                when (m.type) {
                    "intro" -> R.string.skip_intro
                    "recap" -> R.string.skip_recap
                    else -> R.string.next_episode
                },
            )
        }
    }

    private fun skipMarker() {
        val m = activeMarker() ?: return
        if (m.type != "credits" || kind != "episode") {
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
        // Down from the progress line lands on the first chip, not wherever the thumb is.
        seek.nextFocusDownId = listOf(subsButton, audioButton, syncButton).firstOrNull { it.visibility == View.VISIBLE }?.id ?: View.NO_ID
    }

    /** Sets a text button's icons to [sizeDp], so the chips stay small. */
    private fun shrinkIcons(b: TextView, sizeDp: Int) {
        val px = (sizeDp * resources.displayMetrics.density).toInt()
        val d = b.compoundDrawablesRelative
        d.forEach { it?.setBounds(0, 0, px, px) }
        b.setCompoundDrawablesRelative(d[0], d[1], d[2], d[3])
    }

    /** Where the remote starts when the controls come up: the progress line on TVs. */
    private fun focusHome() {
        if (touchScreen) playPause.requestFocus() else seek.requestFocus()
    }

    // ---- Audio & Subtitles panel ----

    private var panel: Dialog? = null

    /**
     * One panel for Audio and Subtitles side by side, like Netflix and Apple TV:
     * one Down while watching opens it, Left/Right moves between the lists, OK picks.
     * "Subtitle style" inside it opens the look settings in the same panel.
     */
    private fun showTrackPanel(subtitles: Boolean) {
        if (panel?.isShowing == true) return
        ui.removeCallbacks(hideControls)
        val dialog = Dialog(this)
        panel = dialog
        val frame = android.widget.FrameLayout(this)
        dialog.requestWindowFeature(android.view.Window.FEATURE_NO_TITLE)
        dialog.setContentView(frame)
        dialog.window?.apply {
            setBackgroundDrawable(android.graphics.drawable.ColorDrawable(android.graphics.Color.TRANSPARENT))
            setGravity(android.view.Gravity.END or android.view.Gravity.CENTER_VERTICAL)
            setLayout(android.view.ViewGroup.LayoutParams.WRAP_CONTENT, android.view.ViewGroup.LayoutParams.WRAP_CONTENT)
            attributes = attributes.also { it.x = dp(28); it.dimAmount = 0f }
            clearFlags(WindowManager.LayoutParams.FLAG_DIM_BEHIND)
            setWindowAnimations(android.R.style.Animation_Translucent)
        }
        dialog.setOnDismissListener {
            panel = null
            scheduleHide()
        }
        showTracksPage(dialog, frame, subtitles)
        dialog.show()
    }

    private fun dp(v: Int) = (v * resources.displayMetrics.density).toInt()

    private fun showTracksPage(dialog: Dialog, frame: android.widget.FrameLayout, focusSubs: Boolean) {
        frame.removeAllViews()
        val audioTracks = player.audioTracks ?: emptyArray()
        val spuTracks = player.spuTracks ?: emptyArray()
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            setBackgroundResource(R.drawable.bg_glass_panel)
            setPadding(dp(14), dp(18), dp(14), dp(14))
        }
        var focus: View? = null
        fun column(title: String, build: (LinearLayout) -> Unit): View {
            val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
            build(list)
            return LinearLayout(this).apply {
                orientation = LinearLayout.VERTICAL
                addView(header(title))
                addView(ScrollView(context).apply {
                    isVerticalScrollBarEnabled = false
                    isFillViewport = false
                    addView(list)
                }, LinearLayout.LayoutParams(dp(230), LinearLayout.LayoutParams.WRAP_CONTENT))
            }
        }
        if (audioTracks.size > 1 || spuTracks.isEmpty()) {
            row.addView(column(getString(R.string.audio)) { list ->
                val current = player.audioTrack
                for (t in audioTracks) {
                    if (t.id == -1) continue
                    val r = choiceRow(t.name, t.id == current) {
                        player.setAudioTrack(t.id)
                        rememberTrack(true, t.name)
                        dialog.dismiss()
                    }
                    list.addView(r)
                    if (!focusSubs && (t.id == current || focus == null)) focus = r
                }
                if (syncButton.visibility == View.VISIBLE) {
                    list.addView(divider())
                    list.addView(linkRow(getString(R.string.audio_sync), "") {
                        dialog.dismiss()
                        showAudioSync()
                    })
                }
            })
        }
        if (spuTracks.isNotEmpty()) {
            if (row.childCount > 0) row.addView(View(this), LinearLayout.LayoutParams(dp(14), 1))
            row.addView(column(getString(R.string.subtitles)) { list ->
                val current = player.spuTrack
                for (t in spuTracks) {
                    val name = if (t.id == -1) getString(R.string.subs_off) else t.name
                    val r = choiceRow(name, t.id == current) {
                        player.setSpuTrack(t.id)
                        rememberTrack(false, t.name)
                        dialog.dismiss()
                    }
                    list.addView(r)
                    if ((focusSubs || focus == null) && t.id == current) focus = r
                }
                list.addView(divider())
                val style = SubtitleStyle.load(this)
                val preset = style.preset()
                list.addView(linkRow(getString(R.string.sub_style), if (preset >= 0) SubtitleStyle.PRESETS[preset].first else getString(R.string.sub_custom)) {
                    showStylePage(dialog, frame)
                })
            })
        }
        if (canShare()) {
            if (row.childCount > 0) row.addView(View(this), LinearLayout.LayoutParams(dp(14), 1))
            row.addView(column(getString(R.string.share)) { list ->
                for ((id, name) in shareTargets) {
                    list.addView(linkRow(getString(R.string.share_to_watchlist, name), "") {
                        dialog.dismiss()
                        shareTo(id, name)
                    })
                }
            })
        }
        frame.addView(row)
        (focus ?: firstFocusable(row))?.requestFocus()
    }

    // ---- Share to someone's Plex Watchlist ----

    private fun canShare() = shareTargets.isNotEmpty() && (fileId > 0 || itemId > 0)

    private fun panelHasContent() =
        subsButton.visibility == View.VISIBLE || audioButton.visibility == View.VISIBLE || canShare()

    /** Who Share can send to; the server only lists people for admins. */
    private fun loadShareTargets() {
        if (server.isEmpty() || calibrating) return
        io.execute {
            val list = try {
                getJson("/api/share/targets")?.optJSONArray("targets")?.let { arr ->
                    (0 until arr.length()).map { arr.getJSONObject(it) }.map { it.optString("id") to it.optString("name") }
                } ?: emptyList()
            } catch (_: Exception) {
                emptyList()
            }
            ui.post { shareTargets = list }
        }
    }

    /** Puts the film, or the show this episode is from, on their Plex Watchlist. */
    private fun shareTo(id: String, name: String) {
        val body = JSONObject().put("target", id)
        if (fileId > 0) body.put("fileId", fileId) else body.put("itemId", itemId)
        Toast.makeText(this, getString(R.string.share_sending, name), Toast.LENGTH_SHORT).show()
        io.execute {
            val msg = try {
                val r = Http.request("$server/api/share/watchlist", "POST", body.toString(), mapOf("Cookie" to cookie), timeoutMs = 20_000)
                val j = try { JSONObject(r.body) } catch (_: Exception) { JSONObject() }
                when {
                    r.code != 200 -> j.optString("error").ifEmpty { "HTTP ${r.code}" }
                    j.optBoolean("already") -> getString(R.string.share_already, j.optString("title"), name)
                    else -> getString(R.string.share_done, j.optString("title"), name)
                }
            } catch (e: Exception) {
                getString(R.string.share_failed, e.message ?: e.toString())
            }
            ui.post { if (!isFinishing) Toast.makeText(this, msg, Toast.LENGTH_LONG).show() }
        }
    }

    /** Subtitle look: a live preview at the real size, then one row per setting (Left/Right changes it). */
    private fun showStylePage(dialog: Dialog, frame: android.widget.FrameLayout) {
        frame.removeAllViews()
        val before = SubtitleStyle.load(this)
        var style = before
        val screenDp = resources.displayMetrics.heightPixels / resources.displayMetrics.density
        val sample = TextView(this).apply {
            text = getString(R.string.sub_sample)
            gravity = android.view.Gravity.CENTER
            setPadding(dp(8), dp(2), dp(8), dp(3))
        }
        val stage = android.widget.FrameLayout(this).apply {
            background = android.graphics.drawable.GradientDrawable(
                android.graphics.drawable.GradientDrawable.Orientation.TL_BR,
                intArrayOf(0xFF3A4A5E.toInt(), 0xFF8A7560.toInt(), 0xFFD9C7A8.toInt()),
            ).apply { cornerRadius = dp(12).toFloat() }
            addView(sample, android.widget.FrameLayout.LayoutParams(
                android.view.ViewGroup.LayoutParams.WRAP_CONTENT, android.view.ViewGroup.LayoutParams.WRAP_CONTENT,
                android.view.Gravity.CENTER_HORIZONTAL or android.view.Gravity.BOTTOM,
            ).apply { bottomMargin = dp(12) })
        }
        val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val page = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundResource(R.drawable.bg_glass_panel)
            setPadding(dp(14), dp(18), dp(14), dp(14))
            addView(header(getString(R.string.sub_style)))
            addView(stage, LinearLayout.LayoutParams(dp(380), dp(110)).apply { bottomMargin = dp(10) })
            addView(ScrollView(context).apply { isVerticalScrollBarEnabled = false; addView(list) },
                LinearLayout.LayoutParams(dp(380), LinearLayout.LayoutParams.WRAP_CONTENT))
        }
        val settings = ArrayList<Pair<TextView, () -> String>>()
        fun refresh() {
            style.preview(sample, screenDp)
            settings.forEach { (v, value) -> v.text = value() }
        }
        fun setting(label: String, value: () -> String, change: (Int) -> Unit): View {
            val valueView = TextView(this).apply {
                textSize = 12f
                setTextColor(getColorStateList(R.color.control_fg))
                gravity = android.view.Gravity.END or android.view.Gravity.CENTER_VERTICAL
                isDuplicateParentStateEnabled = true
            }
            settings += valueView to { "‹  " + value() + "  ›" }
            val labelView = TextView(this).apply {
                text = label
                textSize = 12f
                setTextColor(getColorStateList(R.color.control_fg))
                isDuplicateParentStateEnabled = true
            }
            return LinearLayout(this).apply {
                orientation = LinearLayout.HORIZONTAL
                gravity = android.view.Gravity.CENTER_VERTICAL
                isFocusable = true
                isClickable = true
                minimumHeight = dp(32)
                setPadding(dp(12), 0, dp(12), 0)
                setBackgroundResource(R.drawable.bg_track_row)
                addView(labelView, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
                addView(valueView)
                setOnClickListener { change(1); refresh() }
                setOnKeyListener { _, code, ev ->
                    if (ev.action != KeyEvent.ACTION_DOWN) return@setOnKeyListener false
                    when (code) {
                        KeyEvent.KEYCODE_DPAD_LEFT -> { change(-1); refresh(); true }
                        KeyEvent.KEYCODE_DPAD_RIGHT -> { change(1); refresh(); true }
                        else -> false
                    }
                }
                list.addView(this, LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT))
            }
        }
        fun cycle(i: Int, by: Int, n: Int) = ((i + by) % n + n) % n
        val first = setting(getString(R.string.sub_preset), {
            val p = style.preset()
            if (p >= 0) SubtitleStyle.PRESETS[p].first else getString(R.string.sub_custom)
        }) { by ->
            val p = style.preset()
            val next = if (p < 0) (if (by > 0) 0 else SubtitleStyle.PRESETS.size - 1) else cycle(p, by, SubtitleStyle.PRESETS.size)
            style = SubtitleStyle.PRESETS[next].second
        }
        setting(getString(R.string.sub_size_label), { SubtitleStyle.SIZES[style.size].first }) { by ->
            style = style.copy(size = (style.size + by).coerceIn(SubtitleStyle.SIZES.indices))
        }
        setting(getString(R.string.sub_font), { SubtitleStyle.FONTS[style.font].first }) { by ->
            style = style.copy(font = cycle(style.font, by, SubtitleStyle.FONTS.size))
        }
        setting(getString(R.string.sub_weight), { getString(if (style.bold) R.string.sub_bold else R.string.sub_regular) }) {
            style = style.copy(bold = !style.bold)
        }
        setting(getString(R.string.sub_colour), { SubtitleStyle.COLOURS[style.colour].first }) { by ->
            style = style.copy(colour = cycle(style.colour, by, SubtitleStyle.COLOURS.size))
        }
        setting(getString(R.string.sub_edge), { SubtitleStyle.EDGES[style.edge] }) { by ->
            style = style.copy(edge = cycle(style.edge, by, SubtitleStyle.EDGES.size))
        }
        setting(getString(R.string.sub_background), { SubtitleStyle.BACKGROUNDS[style.background].first }) { by ->
            style = style.copy(background = cycle(style.background, by, SubtitleStyle.BACKGROUNDS.size))
        }
        setting(getString(R.string.sub_position), { SubtitleStyle.POSITIONS[style.position].first }) { by ->
            style = style.copy(position = (style.position + by).coerceIn(SubtitleStyle.POSITIONS.indices))
        }
        list.addView(divider())
        val done = linkRow(getString(R.string.sub_apply), "") { dialog.dismiss() }
        list.addView(done)
        list.addView(linkRow(getString(R.string.sub_reset), "") {
            style = SubtitleStyle.DEFAULT
            refresh()
        })
        refresh()
        frame.addView(page)
        first.requestFocus()
        // Leaving (Done or Back) keeps the new look; libVLC needs a restart to use it.
        dialog.setOnDismissListener {
            panel = null
            if (style != before) {
                SubtitleStyle.save(this, style)
                restartPlayer()
            }
            scheduleHide()
        }
    }

    private fun header(title: String) = TextView(this).apply {
        text = title
        isAllCaps = true
        letterSpacing = 0.16f
        textSize = 10f
        typeface = android.graphics.Typeface.create("sans-serif-medium", android.graphics.Typeface.NORMAL)
        setTextColor(getColor(R.color.muted))
        setPadding(dp(12), 0, 0, dp(10))
    }

    private fun divider() = View(this).apply {
        setBackgroundColor(0x1AFFFFFF)
        layoutParams = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 1).apply { setMargins(dp(12), dp(8), dp(12), dp(8)) }
    }

    /** A track to pick: a red dot marks the one playing. */
    private fun choiceRow(label: String, on: Boolean, onPick: () -> Unit): View {
        val text = TextView(this).apply {
            this.text = label
            textSize = 13f
            maxLines = 1
            ellipsize = android.text.TextUtils.TruncateAt.END
            setTextColor(getColorStateList(R.color.control_fg))
            isDuplicateParentStateEnabled = true
            if (on) typeface = android.graphics.Typeface.create("sans-serif-medium", android.graphics.Typeface.NORMAL)
        }
        val mark = View(this).apply {
            background = android.graphics.drawable.GradientDrawable().apply {
                shape = android.graphics.drawable.GradientDrawable.OVAL
                setColor(getColor(R.color.red))
            }
            visibility = if (on) View.VISIBLE else View.INVISIBLE
        }
        return LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = android.view.Gravity.CENTER_VERTICAL
            isFocusable = true
            isClickable = true
            minimumHeight = dp(34)
            setPadding(dp(12), 0, dp(12), 0)
            setBackgroundResource(R.drawable.bg_track_row)
            addView(mark, LinearLayout.LayoutParams(dp(6), dp(6)).apply { marginEnd = dp(10) })
            addView(text, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
            setOnClickListener { onPick() }
            layoutParams = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
        }
    }

    /** A row that leads somewhere (Subtitle style ›, Audio sync ›). */
    private fun linkRow(label: String, value: String, onPick: () -> Unit): View {
        val l = TextView(this).apply {
            text = label
            textSize = 12f
            setTextColor(getColorStateList(R.color.control_fg))
            isDuplicateParentStateEnabled = true
        }
        val v = TextView(this).apply {
            text = if (value.isEmpty()) "›" else "$value  ›"
            textSize = 12f
            alpha = 0.7f
            setTextColor(getColorStateList(R.color.control_fg))
            isDuplicateParentStateEnabled = true
        }
        return LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = android.view.Gravity.CENTER_VERTICAL
            isFocusable = true
            isClickable = true
            minimumHeight = dp(32)
            setPadding(dp(28), 0, dp(12), 0)
            setBackgroundResource(R.drawable.bg_track_row)
            addView(l, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
            addView(v)
            setOnClickListener { onPick() }
            layoutParams = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
        }
    }

    private fun firstFocusable(v: View): View? {
        if (v.isFocusable) return v
        if (v is android.view.ViewGroup) for (i in 0 until v.childCount) firstFocusable(v.getChildAt(i))?.let { return it }
        return null
    }

    // ---- scrub previews ----

    private class Thumbs(val fileId: Long, val interval: Double, val count: Int, val cols: Int, val rows: Int,
                         val w: Int, val h: Int, val sheets: List<String>)

    private var thumbs: Thumbs? = null
    private val sheetCache = HashMap<String, android.graphics.Bitmap>()
    private val sheetLoading = HashSet<String>()
    private val thumbRetry = Any()
    private val hidePreview = Runnable { findViewById<View>(R.id.scrub_preview).visibility = View.GONE }

    /** Asks for the file's preview sheets; they're made a few minutes into its first play, so ask again later. */
    private fun loadThumbs() {
        val id = fileId
        thumbs = null
        sheetCache.clear()
        ui.removeCallbacksAndMessages(thumbRetry)
        if (id <= 0 || server.isEmpty() || calibrating) return
        for (after in longArrayOf(0, 5 * 60_000, 12 * 60_000)) {
            ui.postAtTime({ fetchThumbs(id) }, thumbRetry, SystemClock.uptimeMillis() + after)
        }
    }

    private fun fetchThumbs(id: Long) {
        if (thumbs?.fileId == id || finished) return
        io.execute {
            try {
                val r = Http.request("$server/api/files/$id/thumbs", headers = mapOf("Cookie" to cookie))
                if (r.code != 200) return@execute
                val j = org.json.JSONObject(r.body)
                val sheets = j.getJSONArray("sheets").let { a -> List(a.length()) { a.getString(it) } }
                val t = Thumbs(id, j.getDouble("interval"), j.getInt("count"), j.getInt("cols"), j.getInt("rows"),
                    j.getInt("width"), j.getInt("height"), sheets)
                ui.post { if (fileId == id) thumbs = t }
            } catch (_: Exception) {
            }
        }
    }

    private fun showScrubPreview(ms: Long) {
        val t = thumbs ?: return
        if (t.count <= 0 || lengthMs <= 0) return
        val idx = ((ms / 1000.0) / t.interval).toInt().coerceIn(0, t.count - 1)
        val perSheet = t.cols * t.rows
        val url = t.sheets.getOrNull(idx / perSheet) ?: return
        val sheet = sheetCache[url]
        if (sheet == null) {
            if (sheetLoading.add(url)) {
                io.execute {
                    val b = Http.bytes(server + url, mapOf("Cookie" to cookie))
                    val bmp = b?.let { android.graphics.BitmapFactory.decodeByteArray(it, 0, it.size) }
                    ui.post {
                        sheetLoading.remove(url)
                        if (bmp != null && thumbs === t) {
                            sheetCache[url] = bmp
                            if (seeking || controlsVisible) showScrubPreview(timeMs)
                        }
                    }
                }
            }
            return
        }
        val cell = idx % perSheet
        val x = (cell % t.cols) * t.w
        val y = (cell / t.cols) * t.h
        if (x + t.w > sheet.width || y + t.h > sheet.height) return
        val preview = findViewById<android.widget.ImageView>(R.id.scrub_preview)
        preview.setImageBitmap(android.graphics.Bitmap.createBitmap(sheet, x, y, t.w, t.h))
        // Centre it over the seek bar at that time.
        val seekLoc = IntArray(2)
        val parentLoc = IntArray(2)
        seek.getLocationInWindow(seekLoc)
        (preview.parent as View).getLocationInWindow(parentLoc)
        val frac = ms.toFloat() / lengthMs
        val cx = seekLoc[0] - parentLoc[0] + seek.paddingLeft + frac * (seek.width - seek.paddingLeft - seek.paddingRight)
        val maxX = ((preview.parent as View).width - preview.width).toFloat().coerceAtLeast(0f)
        preview.translationX = (cx - preview.width / 2f).coerceIn(0f, maxX)
        preview.visibility = View.VISIBLE
        ui.removeCallbacks(hidePreview)
        ui.postDelayed(hidePreview, 1500)
    }

    // ---- Dolby / DTS badge ----

    private var badgeFor = -1L

    /** Names the surround format once per title, and whether it's going to the TV or receiver untouched. */
    private fun showAudioBadge() {
        if (badgeFor == itemId || calibrating) return
        badgeFor = itemId
        val name = player.audioTracks?.firstOrNull { it.id == player.audioTrack }?.name ?: return
        val n = name.uppercase(Locale.US)
        val format = when {
            "ATMOS" in n -> "Dolby Atmos"
            "TRUEHD" in n -> "Dolby TrueHD"
            "E-AC3" in n || "EAC3" in n || "DD+" in n -> "Dolby Digital Plus"
            "AC3" in n || "AC-3" in n -> "Dolby Digital"
            "DTS:X" in n || "DTS-X" in n -> "DTS:X"
            "DTS-HD" in n || "DTS HD" in n -> "DTS-HD"
            "DTS" in n -> "DTS"
            else -> return
        }
        val badge = findViewById<TextView>(R.id.audioBadge)
        badge.text = if (passthrough) "$format · Passthrough" else format
        badge.animate().cancel()
        badge.animate().alpha(1f).setStartDelay(0).setDuration(250).withEndAction {
            badge.animate().alpha(0f).setStartDelay(2500).setDuration(400).start()
        }.start()
    }

    // ---- per-show memory ----

    /** Episodes of a show share their audio and subtitle choice (by track name, as ids differ per file). */
    private fun showKey(): String? =
        if (kind == "episode") findViewById<TextView>(R.id.title).text?.toString()?.takeIf { it.isNotBlank() } else null

    private fun rememberTrack(audio: Boolean, name: String?) {
        val show = showKey() ?: return
        Prefs.setShowTrack(this, show, audio, name ?: "")
    }

    private var showTracksFor = -1L

    /** On an episode's first Playing, pick the tracks chosen for earlier episodes of the show. */
    private fun applyShowTracks() {
        if (showTracksFor == itemId) return
        showTracksFor = itemId
        val show = showKey() ?: return
        for (audio in listOf(true, false)) {
            val want = Prefs.showTrack(this, show, audio) ?: continue
            val tracks = (if (audio) player.audioTracks else player.spuTracks) ?: continue
            val t = tracks.firstOrNull { it.name == want } ?: continue
            if (audio) player.setAudioTrack(t.id) else player.setSpuTrack(t.id)
        }
    }

    private fun cycleAspect() {
        val types = MediaPlayer.ScaleType.getMainScaleTypes()
        scaleIndex = (scaleIndex + 1) % types.size
        player.videoScale = types[scaleIndex]
        Toast.makeText(this, types[scaleIndex].name.removePrefix("SURFACE_").replace('_', ' ').lowercase(), Toast.LENGTH_SHORT).show()
    }

    // ---- refresh rate matching ----

    /**
     * Switch the TV to a refresh rate the film's frame rate divides into
     * (23.976 fps -> 23.976/24 Hz instead of 59.94 Hz), so pans don't judder.
     * Android puts the old mode back when the player closes.
     */
    private fun matchRefreshRate(attempt: Int = 0) {
        if (modeSwitched) return
        val track = player.currentVideoTrack
        if (track == null || track.frameRateNum <= 0 || track.frameRateDen <= 0) {
            // libVLC often learns the frame rate a moment after the first
            // frame is shown, so keep asking for a few seconds.
            ui.removeCallbacksAndMessages(modeRetry)
            if (attempt < 20) ui.postAtTime({ matchRefreshRate(attempt + 1) }, modeRetry, SystemClock.uptimeMillis() + 500)
            return
        }
        modeSwitched = true
        val fps = track.frameRateNum.toFloat() / track.frameRateDen
        @Suppress("DEPRECATION")
        val display = windowManager.defaultDisplay
        val current = display.mode
        val best = display.supportedModes
            .filter { it.physicalWidth == current.physicalWidth && it.physicalHeight == current.physicalHeight }
            .mapNotNull { m ->
                val ratio = m.refreshRate / fps
                val whole = Math.round(ratio)
                val off = Math.abs(ratio - whole)
                if (whole >= 1 && off < 0.012f) Pair(m, off) else null
            }
            // Exact match first (23.976 over 24), then the lowest such rate.
            .sortedWith(compareBy<Pair<Display.Mode, Float>>({ Math.round(it.second * 1000) }, { it.first.refreshRate }))
            .firstOrNull()?.first ?: return
        if (best.modeId == current.modeId) return
        window.attributes = window.attributes.also { it.preferredDisplayModeId = best.modeId }
    }

    // ---- audio passthrough ----

    private val headphoneTypes: Set<Int> by lazy {
        val t = mutableSetOf(
            AudioDeviceInfo.TYPE_BLUETOOTH_A2DP, AudioDeviceInfo.TYPE_WIRED_HEADSET,
            AudioDeviceInfo.TYPE_WIRED_HEADPHONES, AudioDeviceInfo.TYPE_USB_HEADSET,
        )
        if (Build.VERSION.SDK_INT >= 28) t += AudioDeviceInfo.TYPE_HEARING_AID
        if (Build.VERSION.SDK_INT >= 31) t += listOf(AudioDeviceInfo.TYPE_BLE_HEADSET, AudioDeviceInfo.TYPE_BLE_SPEAKER)
        t
    }

    private fun headphonesConnected(): Boolean =
        (getSystemService(Context.AUDIO_SERVICE) as AudioManager).getDevices(AudioManager.GET_DEVICES_OUTPUTS).any { it.type in headphoneTypes }

    /**
     * Headphones connected mid-film: stereo for them (no passthrough). Gone again: rebuild the
     * player from the same moment, so libVLC picks the HDMI output (and passthrough) itself.
     */
    private val deviceWatch = object : AudioDeviceCallback() {
        override fun onAudioDevicesAdded(added: Array<out AudioDeviceInfo>) = recheck()
        override fun onAudioDevicesRemoved(removed: Array<out AudioDeviceInfo>) = recheck()
        private fun recheck() {
            if (finished || !::player.isInitialized) return
            val phones = headphonesConnected()
            if (phones == stereoOut) return
            if (phones) {
                passthrough = false
                stereoOut = player.setAudioOutputDevice(STEREO_FOR_HEADPHONES)
            } else {
                passthrough = PASSTHROUGH_CLOSES_CLEANLY && hdmiSurround
                restartPlayer()
            }
        }
    }

    private fun hdmiTakesSurround(): Boolean {
        val wanted = setOf(AudioFormat.ENCODING_AC3, AudioFormat.ENCODING_E_AC3, AudioFormat.ENCODING_DTS)
        val am = getSystemService(Context.AUDIO_SERVICE) as AudioManager
        val hdmiTypes = mutableSetOf(AudioDeviceInfo.TYPE_HDMI, AudioDeviceInfo.TYPE_HDMI_ARC)
        if (Build.VERSION.SDK_INT >= 31) hdmiTypes += AudioDeviceInfo.TYPE_HDMI_EARC
        val fromDevices = am.getDevices(AudioManager.GET_DEVICES_OUTPUTS)
            .filter { it.type in hdmiTypes }
            .any { d -> d.encodings.any { it in wanted } }
        if (fromDevices) return true
        // Fire TV reports the HDMI sink's formats in this sticky broadcast.
        val filter = IntentFilter(AudioManager.ACTION_HDMI_AUDIO_PLUG)
        val plug = try {
            if (Build.VERSION.SDK_INT >= 33) registerReceiver(null, filter, Context.RECEIVER_NOT_EXPORTED) else registerReceiver(null, filter)
        } catch (_: Exception) {
            null
        }
        val enc = plug?.getIntArrayExtra(AudioManager.EXTRA_ENCODINGS) ?: return false
        return enc.any { it in wanted }
    }

    // ---- playback info ("nerd stats") ----

    private val statsTick = object : Runnable {
        override fun run() {
            updateStats()
            if (statsVisible) ui.postDelayed(this, 1000)
        }
    }

    private fun toggleStats() {
        statsVisible = !statsVisible
        statsText.visibility = if (statsVisible) View.VISIBLE else View.GONE
        ui.removeCallbacks(statsTick)
        lastReadBytes = -1
        if (statsVisible) statsTick.run()
    }

    private fun updateStats() {
        val sb = StringBuilder()
        val v = player.currentVideoTrack
        if (v != null) {
            val fps = if (v.frameRateDen > 0) v.frameRateNum.toFloat() / v.frameRateDen else 0f
            sb.append("VIDEO    ").append(v.codec?.trim()?.uppercase(Locale.US) ?: "?")
                .append("  ").append(v.width).append('x').append(v.height)
            if (fps > 0) sb.append(String.format(Locale.US, "  %.3f fps", fps))
            sb.append('\n')
        }
        @Suppress("DEPRECATION")
        val mode = windowManager.defaultDisplay.mode
        sb.append("DISPLAY  ").append(mode.physicalWidth).append('x').append(mode.physicalHeight)
            .append(String.format(Locale.US, " @ %.3f Hz", mode.refreshRate)).append('\n')
        val audioName = player.audioTracks?.firstOrNull { it.id == player.audioTrack }?.name
        sb.append("AUDIO    ").append(audioName ?: "-").append('\n')
        sb.append("OUTPUT   ").append(
            when {
                passthrough -> "Passthrough to HDMI (when the format allows)"
                stereoOut -> "Decoded to stereo for headphones"
                else -> "Decoded to PCM"
            }
        ).append('\n')
        val media = player.media
        try {
            val st = media?.stats
            if (st != null) {
                val rate = if (lastReadBytes >= 0) ((st.readBytes - lastReadBytes).toLong() and 0xffffffffL) * 8 / 1e6 else 0.0
                lastReadBytes = st.readBytes
                sb.append(String.format(Locale.US, "NETWORK  %.1f Mbps now · %.1f Mbps stream\n", rate, st.demuxBitrate * 8))
                sb.append("FRAMES   ").append(st.displayedPictures).append(" shown · ").append(st.lostPictures).append(" dropped\n")
                sb.append("AUDIO BUF ").append(st.playedAbuffers).append(" played · ").append(st.lostAbuffers).append(" lost\n")
            }
        } finally {
            media?.release()
        }
        sb.append("POSITION ").append(fmt(timeMs)).append(" / ").append(fmt(lengthMs))
        statsText.text = sb.toString()
    }

    // ---- up next ("next episode in 10s") and "still watching?" ----

    /** Finds the next episode in the background once an episode starts. */
    private fun lookUpNext() {
        if (kind != "episode" || itemId == 0L || lookupFor == itemId) return
        val id = itemId
        lookupFor = id
        io.execute {
            val n = try {
                findNext(id)
            } catch (_: Exception) {
                null
            }
            ui.post {
                if (itemId == id && !isFinishing) {
                    next = n
                    updateSkip()
                    updateUpNext()
                }
            }
        }
    }

    private fun getJson(path: String): JSONObject? {
        val r = Http.request("$server$path", headers = mapOf("Cookie" to cookie))
        return if (r.code == 200) JSONObject(r.body) else null
    }

    /** The episode after [id]: the next one in its season, else the first of the next season. */
    private fun findNext(id: Long): NextEpisode? {
        val cur = getJson("/api/items/$id") ?: return null
        if (cur.optString("kind") != "episode") return null
        val season = cur.optInt("seasonNumber")
        val ep = cur.optInt("episodeNumber")
        val showId = cur.optLong("showId")
        var card = firstEpisode(cur.optJSONArray("siblings")) { it.optInt("episodeNumber") > ep && it.optLong("id") != id }
        if (card == null && season > 0 && showId != 0L) {
            val show = getJson("/api/items/$showId") ?: return null
            val seasons = show.optJSONArray("children") ?: return null
            var nextSeason: JSONObject? = null
            for (i in 0 until seasons.length()) {
                val s = seasons.getJSONObject(i)
                val n = s.optInt("seasonNumber")
                if (s.optString("kind") == "season" && n > season && (nextSeason == null || n < nextSeason.optInt("seasonNumber"))) nextSeason = s
            }
            if (nextSeason != null) {
                val sd = getJson("/api/items/${nextSeason.optLong("id")}") ?: return null
                card = firstEpisode(sd.optJSONArray("children")) { true }
            }
        }
        card ?: return null
        val artPath = card.optString("backdrop").ifEmpty { card.optString("showBackdrop") }.ifEmpty { cur.optString("showBackdrop") }
        val art = artUrl(artPath)?.let { u ->
            Http.bytes(u, mapOf("Cookie" to cookie))?.let { b ->
                try {
                    BitmapFactory.decodeByteArray(b, 0, b.size)
                } catch (_: Throwable) {
                    null
                }
            }
        }
        return NextEpisode(
            id = card.optLong("id"),
            title = card.optString("title"),
            showTitle = card.optString("showTitle").ifEmpty { cur.optString("showTitle") },
            season = card.optInt("seasonNumber"),
            episode = card.optInt("episodeNumber"),
            art = art,
        )
    }

    private fun firstEpisode(arr: JSONArray?, ok: (JSONObject) -> Boolean): JSONObject? {
        arr ?: return null
        var best: JSONObject? = null
        for (i in 0 until arr.length()) {
            val c = arr.getJSONObject(i)
            if (c.optString("kind") != "episode" || !ok(c)) continue
            if (best == null || c.optInt("episodeNumber") < best.optInt("episodeNumber")) best = c
        }
        return best
    }

    /** Same mapping as art() in web/src/api.ts. */
    private fun artUrl(path: String): String? {
        if (path.isEmpty()) return null
        if (path.startsWith("plex:")) return "$server/art/plex/w500?p=" + URLEncoder.encode(path.substring(5), "UTF-8")
        return "$server/art/w500$path"
    }

    /** When the card comes up: at the credits marker, else 10 seconds from the end. */
    private fun upNextAt(): Long {
        val credits = markers.filter { it.type == "credits" && it.startMs > lengthMs / 2 }.minOfOrNull { it.startMs }
        val tail = lengthMs - COUNTDOWN_S * 1000L
        return if (credits != null) minOf(credits, tail) else tail
    }

    private fun updateUpNext() {
        if (next == null || lengthMs < 60_000 || switching || finished) return
        val at = upNextAt()
        if (timeMs < at - 3000) {
            // Rewound out of the credits: put things back as they were.
            upNextDismissed = false
            if (upNextVisible && !askingStillWatching) hideUpNext()
            return
        }
        if (timeMs >= at && !upNextVisible && !upNextDismissed) showUpNext()
    }

    private fun showUpNext() {
        val n = next ?: return
        upNextVisible = true
        handledNext = true
        askingStillWatching = autoPlays >= STILL_WATCHING_AFTER
        setControls(false)
        skipButton.visibility = View.GONE
        if (n.art != null) upNextImage.setImageBitmap(n.art) else upNextImage.setImageDrawable(null)
        val label = getString(R.string.episode_label, n.season, n.episode)
        if (askingStillWatching) {
            upNextEyebrow.text = getString(R.string.still_watching)
            upNextTitle.text = n.showTitle.ifEmpty { n.title }
            upNextSub.text = getString(R.string.still_watching_sub, "$label · ${n.title}")
            upNextCount.visibility = View.GONE
            upNextPlay.text = getString(R.string.keep_watching)
            upNextCancel.text = getString(R.string.stop_watching)
        } else {
            countdown = COUNTDOWN_S
            upNextEyebrow.text = getString(R.string.up_next_in, countdown)
            upNextTitle.text = n.showTitle.ifEmpty { n.title }
            upNextSub.text = "$label · ${n.title}"
            upNextCount.text = countdown.toString()
            upNextCount.visibility = View.VISIBLE
            upNextPlay.text = getString(R.string.play_now)
            upNextCancel.text = getString(R.string.cancel)
            countdownStopped = false
            ui.removeCallbacks(tick)
            ui.postDelayed(tick, 1000)
        }
        upNext.visibility = View.VISIBLE
        upNextPlay.requestFocus()
    }

    /** Any button on the remote stops the countdown; the card stays until Play now or Cancel. */
    private fun stopCountdown() {
        if (!upNextVisible || askingStillWatching || countdownStopped) return
        countdownStopped = true
        ui.removeCallbacks(tick)
        upNextCount.visibility = View.GONE
        upNextEyebrow.text = getString(R.string.up_next)
    }

    private fun hideUpNext() {
        upNextVisible = false
        askingStillWatching = false
        countdownStopped = false
        ui.removeCallbacks(tick)
        ui.removeCallbacks(giveUp)
        upNext.visibility = View.GONE
        updateSkip()
    }

    private fun dismissUpNext() {
        upNextDismissed = true
        hideUpNext()
        setControls(true)
        focusHome()
    }

    private fun onEnd() {
        if (upNextVisible && (askingStillWatching || countdownStopped)) {
            // Wait for an answer, but let the TV go to sleep, and close after a while.
            window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            root.keepScreenOn = false
            ui.removeCallbacks(giveUp)
            ui.postDelayed(giveUp, GIVE_UP_MS)
            return
        }
        if (upNextVisible) {
            playNext(auto = true)
            return
        }
        finishPlayback(ended = true)
    }

    /** Marks this episode watched and plays the next one in the same player. */
    private fun playNext(auto: Boolean) {
        val n = next ?: return
        if (switching || finished) return
        switching = true
        handledNext = true
        autoPlays = if (auto) autoPlays + 1 else 0
        hideUpNext()
        if (syncMode) closeSync(keep = true)
        report(force = true, positionMs = lengthMs, stopped = true)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        root.keepScreenOn = true
        spinner.visibility = View.VISIBLE
        errorText.visibility = View.GONE
        io.execute {
            val loaded = try {
                val d = getJson("/api/items/${n.id}")
                val file = d?.optJSONArray("files")?.optJSONObject(0)
                val p = file?.let { getJson("/api/play/${it.optLong("id")}?probe=0") }
                val m = try {
                    Http.request("$server/api/items/${n.id}/markers?recap=1", headers = mapOf("Cookie" to cookie)).body
                } catch (_: Exception) {
                    "[]"
                }
                if (p == null || p.optString("externalUrl").isEmpty()) null else Triple(d, p, m)
            } catch (_: Exception) {
                null
            }
            ui.post {
                if (isFinishing || finished) return@post
                switching = false
                if (loaded == null) {
                    Toast.makeText(this, R.string.player_error, Toast.LENGTH_LONG).show()
                    finishPlayback(ended = false)
                    return@post
                }
                val (d, p, m) = loaded
                val file = d!!.optJSONArray("files")!!.optJSONObject(0)
                itemId = n.id
                fileId = file.optLong("id")
                fileDelayMs = p.optInt("audioOffsetMs", 0)
                syncButton.visibility = if (fileId > 0) View.VISIBLE else View.GONE
                kind = "episode"
                next = null
                lookupFor = 0L
                upNextDismissed = false
                modeSwitched = false
                lastReport = 0L
                markers = parseMarkers(m)
                val pf = p.optJSONObject("file")
                val dur = pf?.optDouble("duration", 0.0)?.takeIf { it > 0 } ?: file.optDouble("duration", 0.0)
                lengthMs = (dur * 1000).toLong()
                val startMs = (p.optDouble("position", 0.0) * 1000).toLong()
                timeMs = if (startMs > 5_000) startMs else 0L
                findViewById<TextView>(R.id.title).text = n.showTitle.ifEmpty { n.title }
                findViewById<TextView>(R.id.subtitle).text = "${getString(R.string.episode_label, n.season, n.episode)} · ${n.title}"
                overviewText.text = d.optString("overview")
                overviewText.visibility = View.GONE
                skipButton.visibility = View.GONE
                load(p.optString("externalUrl"), startMs)
                loadThumbs()
                setControls(true)
            }
        }
    }

    // ---- lip sync ----

    /** Device offset plus the file's own fix, in libVLC's microseconds. */
    private fun applyAudioDelay() {
        if (!::player.isInitialized) return
        val ms = (deviceDelayMs + if (calibrating) 0 else fileDelayMs).coerceIn(-Prefs.MAX_DELAY * 2, Prefs.MAX_DELAY * 2)
        player.setAudioDelay(ms * 1000L)
    }

    private fun describe(ms: Int): String = when {
        ms > 0 -> getString(R.string.audio_sync_later, ms)
        ms < 0 -> getString(R.string.audio_sync_earlier, -ms)
        else -> getString(R.string.audio_sync_value_zero)
    }

    private fun nudgeCalibration(by: Int) {
        deviceDelayMs = (deviceDelayMs + by).coerceIn(-Prefs.MAX_DELAY, Prefs.MAX_DELAY)
        applyAudioDelay()
        updateCalibration()
    }

    private fun updateCalibration() {
        val text = if (touchScreen) R.string.calibration_text_touch else R.string.calibration_text
        calibrationText.text = getString(text, describe(deviceDelayMs))
        calibrationText.visibility = View.VISIBLE
        if (touchScreen) showAdjustButtons(calibrationText)
    }

    private fun endCalibration(save: Boolean) {
        if (save) Prefs.setAudioDelay(this, deviceDelayMs)
        finished = true
        setResult(if (save) RESULT_OK else RESULT_CANCELED)
        finish()
    }

    // ---- lip sync with a phone ----

    /** Gets a code from the server and shows its QR code, then waits for the phone. */
    private fun startPhoneSync() {
        val panel = findViewById<View>(R.id.phone_panel)
        phoneStatus = findViewById(R.id.phone_status)
        panel.visibility = View.VISIBLE
        phoneStatus.setText(R.string.phone_starting)
        if (touchScreen) {
            findViewById<TextView>(R.id.phone_hint).setText(R.string.phone_hint_touch)
            findViewById<View>(R.id.phone_done).apply {
                visibility = View.VISIBLE
                setOnClickListener { endCalibration(save = true) }
            }
        }
        io.execute {
            try {
                val r = Http.request("$server/api/lipsync/sessions", "POST", JSONObject().put("deviceMs", deviceDelayMs).toString(), mapOf("Cookie" to cookie))
                if (r.code != 200) throw Exception(JSONObject(r.body).optString("error", "HTTP ${r.code}"))
                val code = JSONObject(r.body).getString("code")
                val png = Http.bytes("$server/api/lipsync/sessions/$code/qr.png", mapOf("Cookie" to cookie))
                val qr = png?.let { BitmapFactory.decodeByteArray(it, 0, it.size) }
                ui.post {
                    if (finished) return@post
                    phoneCode = code
                    findViewById<ImageView>(R.id.phone_qr).setImageBitmap(qr)
                    findViewById<TextView>(R.id.phone_code).text = getString(R.string.phone_code, server, code)
                    phoneStatus.setText(R.string.phone_waiting)
                    ui.postDelayed(phonePoll, PHONE_POLL_MS)
                }
            } catch (e: Exception) {
                ui.post { if (!finished) phoneStatus.text = getString(R.string.phone_no_server, e.message ?: "") }
            }
        }
    }

    private val phonePoll: Runnable = Runnable { pollPhone() }

    private fun pollPhone() {
        if (finished || phoneCode.isEmpty()) return
        val code = phoneCode
        val current = deviceDelayMs
        io.execute {
            val st = try {
                val r = Http.request("$server/api/lipsync/sessions/$code?deviceMs=$current", headers = mapOf("Cookie" to cookie))
                if (r.code == 404) null else if (r.code == 200) JSONObject(r.body) else JSONObject()
            } catch (_: Exception) {
                JSONObject() // a blip: keep trying
            }
            ui.post {
                if (finished) return@post
                if (st == null) {
                    phoneStatus.setText(R.string.phone_expired)
                    return@post
                }
                onPhoneState(st)
                ui.postDelayed(phonePoll, PHONE_POLL_MS)
            }
        }
    }

    private fun onPhoneState(st: JSONObject) {
        when (st.optString("state")) {
            "uploading" -> phoneStatus.setText(R.string.phone_uploading)
            "analysing" -> phoneStatus.setText(R.string.phone_analysing)
            "failed" -> phoneStatus.text = getString(R.string.phone_failed, st.optString("error"))
            "done" -> {
                val round = st.optInt("round")
                if (round <= phoneRound) return
                phoneRound = round
                val measured = st.optJSONObject("result")?.optInt("offsetMs") ?: return
                deviceDelayMs = st.optInt("newDeviceMs", deviceDelayMs - measured).coerceIn(-Prefs.MAX_DELAY, Prefs.MAX_DELAY)
                Prefs.setAudioDelay(this, deviceDelayMs)
                applyAudioDelay()
                val now = describe(deviceDelayMs).replaceFirstChar { it.lowercase() }
                phoneStatus.text = when {
                    abs(measured) <= PHONE_IN_SYNC_MS -> getString(R.string.phone_in_sync, abs(measured), now)
                    measured > 0 -> getString(R.string.phone_fixed_late, measured, now)
                    else -> getString(R.string.phone_fixed_early, -measured, now)
                }
                val code = phoneCode
                val saved = deviceDelayMs
                io.execute {
                    try {
                        Http.request("$server/api/lipsync/sessions/$code/applied", "POST", JSONObject().put("deviceMs", saved).toString(), mapOf("Cookie" to cookie))
                    } catch (_: Exception) {
                    }
                }
            }
        }
    }

    private fun step() = if (calibrating) CALIBRATION_STEP_MS else SYNC_STEP_MS
    private fun bigStep() = step() * if (calibrating) 5 else 4

    /** A button on a touch screen moved the sound by this much. */
    private fun adjust(by: Int) {
        if (by == 0) return
        if (calibrating) nudgeCalibration(by) else if (syncMode) nudgeSync(by)
    }

    /** Shows the touch buttons and lifts [above] clear of them. */
    private fun showAdjustButtons(above: View) {
        if (adjustButtons.visibility != View.VISIBLE) {
            findViewById<Button>(R.id.adjust_big_down).text = "« ${bigStep()} ms"
            findViewById<Button>(R.id.adjust_down).text = "‹ ${step()} ms"
            findViewById<Button>(R.id.adjust_up).text = "${step()} ms ›"
            findViewById<Button>(R.id.adjust_big_up).text = "${bigStep()} ms »"
            adjustButtons.visibility = View.VISIBLE
        }
        adjustButtons.post {
            val lp = above.layoutParams as android.widget.FrameLayout.LayoutParams
            val want = adjustButtons.height + (adjustButtons.layoutParams as android.widget.FrameLayout.LayoutParams).bottomMargin +
                (12 * resources.displayMetrics.density).toInt()
            if (lp.bottomMargin != want) {
                lp.bottomMargin = want
                above.layoutParams = lp
            }
        }
    }

    /**
     * The per-file control: a small see-through bar at the bottom while the
     * film keeps playing, so you can watch the lips as you adjust. Left/right
     * or rewind/fast-forward move the sound, OK keeps it, Back undoes it.
     */
    private fun showAudioSync() {
        syncMode = true
        syncStart = fileDelayMs
        setControls(false)
        updateSyncBar()
    }

    private fun nudgeSync(by: Int) {
        fileDelayMs = (fileDelayMs + by).coerceIn(-Prefs.MAX_DELAY, Prefs.MAX_DELAY)
        applyAudioDelay()
        updateSyncBar()
    }

    private fun updateSyncBar() {
        val device = if (deviceDelayMs == 0) "" else
            "   ·   " + getString(if (touchScreen) R.string.audio_sync_device_touch else R.string.audio_sync_device, deviceDelayMs)
        syncBar.text = getString(if (touchScreen) R.string.audio_sync_bar_touch else R.string.audio_sync_bar, describe(fileDelayMs)) + device
        syncBar.visibility = View.VISIBLE
        if (touchScreen) showAdjustButtons(syncBar)
    }

    private fun closeSync(keep: Boolean) {
        syncMode = false
        syncBar.visibility = View.GONE
        adjustButtons.visibility = View.GONE
        if (!keep) {
            fileDelayMs = syncStart
            applyAudioDelay()
        } else if (fileDelayMs != syncStart) {
            saveFileDelay(fileId, fileDelayMs)
        }
    }

    private fun saveFileDelay(id: Long, ms: Int) {
        if (id == 0L || server.isEmpty()) return
        io.execute {
            val err = try {
                val r = Http.request("$server/api/files/$id/audio-offset", "PUT", "{\"offsetMs\":$ms}", mapOf("Cookie" to cookie))
                if (r.code == 200) null else "HTTP ${r.code}"
            } catch (e: Exception) {
                e.message ?: e.toString()
            }
            ui.post {
                if (isFinishing) return@post
                val msg = if (err == null) getString(R.string.audio_sync_saved) else getString(R.string.audio_sync_failed, err)
                Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
            }
        }
    }

    // ---- keyboard / remote control ----

    // Any button press while the controls are up keeps them up, so they don't
    // vanish while you're moving between buttons with the remote.
    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.action == KeyEvent.ACTION_DOWN) {
            autoPlays = 0
            // Volume isn't a choice about the next episode.
            val volume = event.keyCode == KeyEvent.KEYCODE_VOLUME_UP || event.keyCode == KeyEvent.KEYCODE_VOLUME_DOWN || event.keyCode == KeyEvent.KEYCODE_VOLUME_MUTE
            if (!volume) stopCountdown()
            if (controlsVisible) scheduleHide()
        }
        return super.dispatchKeyEvent(event)
    }

    override fun dispatchTouchEvent(ev: MotionEvent): Boolean {
        if (ev.actionMasked == MotionEvent.ACTION_DOWN) {
            autoPlays = 0
            stopCountdown()
        }
        return super.dispatchTouchEvent(ev)
    }

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        if (syncMode) {
            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_LEFT, KeyEvent.KEYCODE_MEDIA_REWIND -> nudgeSync(-SYNC_STEP_MS)
                KeyEvent.KEYCODE_DPAD_RIGHT, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> nudgeSync(SYNC_STEP_MS)
                KeyEvent.KEYCODE_DPAD_UP -> nudgeSync(SYNC_STEP_MS * 4)
                KeyEvent.KEYCODE_DPAD_DOWN -> nudgeSync(-SYNC_STEP_MS * 4)
                KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER -> closeSync(keep = true)
                KeyEvent.KEYCODE_BACK -> closeSync(keep = false)
                KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE, KeyEvent.KEYCODE_SPACE -> togglePlay()
                KeyEvent.KEYCODE_MEDIA_PLAY -> player.play()
                KeyEvent.KEYCODE_MEDIA_PAUSE -> player.pause()
                else -> return super.onKeyDown(keyCode, event)
            }
            return true
        }
        if (phoneMode) {
            // Whatever the phone measured is already saved: OK and Back both finish.
            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_BACK -> endCalibration(save = true)
                KeyEvent.KEYCODE_VOLUME_UP, KeyEvent.KEYCODE_VOLUME_DOWN, KeyEvent.KEYCODE_VOLUME_MUTE -> return super.onKeyDown(keyCode, event)
            }
            return true
        }
        if (calibrating) {
            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_LEFT -> nudgeCalibration(-CALIBRATION_STEP_MS)
                KeyEvent.KEYCODE_DPAD_RIGHT -> nudgeCalibration(CALIBRATION_STEP_MS)
                KeyEvent.KEYCODE_DPAD_UP -> nudgeCalibration(CALIBRATION_STEP_MS * 5)
                KeyEvent.KEYCODE_DPAD_DOWN -> nudgeCalibration(-CALIBRATION_STEP_MS * 5)
                KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER -> endCalibration(save = true)
                KeyEvent.KEYCODE_BACK -> endCalibration(save = false)
                else -> return super.onKeyDown(keyCode, event)
            }
            return true
        }
        if (upNextVisible) {
            // The card has the focus: the D-pad moves between its buttons.
            when (keyCode) {
                KeyEvent.KEYCODE_BACK -> {
                    if (askingStillWatching) finishPlayback(false) else dismissUpNext()
                    return true
                }
                KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_DPAD_LEFT,
                KeyEvent.KEYCODE_DPAD_RIGHT, KeyEvent.KEYCODE_DPAD_UP, KeyEvent.KEYCODE_DPAD_DOWN -> {
                    if (currentFocus != upNextPlay && currentFocus != upNextCancel) {
                        upNextPlay.requestFocus()
                        return true
                    }
                    return super.onKeyDown(keyCode, event)
                }
            }
        }
        when (keyCode) {
            KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE, KeyEvent.KEYCODE_SPACE -> togglePlay()
            KeyEvent.KEYCODE_MEDIA_PLAY -> player.play()
            KeyEvent.KEYCODE_MEDIA_PAUSE -> player.pause()
            KeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> seekBy(30_000)
            KeyEvent.KEYCODE_MEDIA_REWIND -> seekBy(-30_000)
            KeyEvent.KEYCODE_DPAD_RIGHT, KeyEvent.KEYCODE_DPAD_LEFT -> {
                val dir = if (keyCode == KeyEvent.KEYCODE_DPAD_RIGHT) 1 else -1
                when {
                    !controlsVisible -> seekBy(dir * 10_000L)
                    // The seek bar has the focus: move it, jump when the presses stop.
                    currentFocus == seek -> scrubBy(dir, event.repeatCount)
                    else -> return super.onKeyDown(keyCode, event)
                }
            }
            KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_DPAD_UP, KeyEvent.KEYCODE_DPAD_DOWN -> {
                if (controlsVisible && scrubMs >= 0 && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
                    commitSeek()
                    return true
                }
                if (controlsVisible && currentFocus == seek && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
                    // OK on the progress line plays or pauses, like Apple TV.
                    togglePlay()
                    return true
                }
                if (scrubMs >= 0) commitSeek()
                if (controlsVisible) return super.onKeyDown(keyCode, event)
                val ok = keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER
                if (ok && skipButton.visibility == View.VISIBLE) {
                    // "Skip intro" / "Skip recap" is on screen: OK presses it, like Plex.
                    skipMarker()
                } else if (keyCode == KeyEvent.KEYCODE_DPAD_DOWN && panelHasContent()) {
                    // One Down while watching: the Audio & Subtitles panel, nothing in between.
                    showTrackPanel(subtitles = subsButton.visibility == View.VISIBLE)
                } else {
                    setControls(true)
                    if (keyCode == KeyEvent.KEYCODE_DPAD_DOWN) syncButton.takeIf { it.visibility == View.VISIBLE }?.requestFocus() ?: focusHome()
                    else focusHome()
                }
            }
            // The remote's Menu (≡) or a captions button: the panel, from anywhere.
            KeyEvent.KEYCODE_MENU, KeyEvent.KEYCODE_CAPTIONS -> {
                if (panelHasContent()) {
                    showTrackPanel(subtitles = keyCode == KeyEvent.KEYCODE_CAPTIONS || subsButton.visibility == View.VISIBLE)
                } else {
                    setControls(true)
                    focusHome()
                }
            }
            KeyEvent.KEYCODE_BACK -> when {
                scrubMs >= 0 -> {
                    // Back while moving the seek bar: stay where the film is.
                    ui.removeCallbacks(commitScrub)
                    scrubMs = -1L
                    seeking = false
                    updateTime()
                    scheduleHide()
                }
                controlsVisible && playing -> setControls(false)
                else -> finishPlayback(false)
            }
            else -> return super.onKeyDown(keyCode, event)
        }
        return true
    }

    // ---- progress reporting, same contract as the web player ----

    private fun report(force: Boolean = false, positionMs: Long = timeMs, stopped: Boolean = false) {
        if (calibrating || lengthMs <= 0 || itemId == 0L || !started) return
        val now = System.currentTimeMillis()
        if (!force && now - lastReport < 10_000) return
        lastReport = now
        // state feeds the server's Now watching page.
        val state = if (finished || stopped) "stopped" else if (playing) "playing" else "paused"
        val body = "{\"position\":${positionMs / 1000.0},\"duration\":${lengthMs / 1000.0},\"state\":\"$state\"}"
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
        // Name the item the page started, so it closes its player page. Once
        // this player has offered the next episode itself, report ended=false
        // so the page doesn't offer it a second time.
        val result = JSONObject()
            .put("itemId", startItemId)
            .put("lastItemId", itemId)
            .put("position", position / 1000.0)
            .put("duration", lengthMs / 1000.0)
            .put("ended", ended && !handledNext)
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
        if (hdmiSurround) (getSystemService(Context.AUDIO_SERVICE) as AudioManager).unregisterAudioDeviceCallback(deviceWatch)
        if (::player.isInitialized) {
            player.setEventListener(null)
            player.stop()
            player.detachViews()
            // Stopping closes the audio output, and Android then reports the
            // device change to libVLC on the main thread; if the player is
            // already released by then, libVLC throws and the app crashes
            // ("can't get VLCObject instance"). Release once that has passed.
            val p = player
            val vlc = libVlc
            Handler(Looper.getMainLooper()).postDelayed({
                p.release()
                vlc.release()
            }, RELEASE_DELAY_MS)
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
        /**
         * Android 9 (Fire OS 7 on the Fire TV Cube) can't close a passthrough stream.
         * libVLC ends its IEC61937 AudioTrack (end of an episode, Back, Stop) with play() and
         * then stop() after the data has run out; the platform has already paused the 192 kHz
         * direct HDMI output for lack of data, so the stopped track never finishes and the
         * output stays open in bitstream mode, even after this app is killed. From then on every
         * app's sound is taken at about a third of real time and not heard, and video follows
         * that clock: everything plays in slow motion with no sound until HDMI audio is
         * re-plugged. Seen on the Cube on 6 and 13 Nov 2026 (dumpsys media.audio_flinger: a
         * DIRECT IEC61937 thread, Standby: no, holding a terminated Plexodus track; Fire OS
         * logging "AUDIO_CRITICAL: Playback drop samples=15360 expected=~49400" every second).
         * Decoding to PCM goes through the normal mixer, which closes cleanly. Passthrough stays
         * on from Android 11 (the SHIELD), which hasn't shown this; Android 10 decodes to be safe.
         */
        private val PASSTHROUGH_CLOSES_CLEANLY = Build.VERSION.SDK_INT >= Build.VERSION_CODES.R

        /**
         * libVLC's audio device for headphones: up to 2 channels, libVLC mixes 5.1/7.1 down itself.
         * Left alone, libVLC picks "pcm" (up to 8 channels) whenever the HDMI sink lists a surround
         * format, even while Bluetooth headphones are the route, and Fire OS plays a 5.1 PCM track
         * to A2DP as silence (Fire TV Cube, 15 Nov 2026: track 0x3F at 0 dB on the A2DP mixer,
         * nothing heard; YouTube's stereo fine).
         */
        private const val STEREO_FOR_HEADPHONES = "stereo"

        const val EXTRA_RESULT = "result"
        private const val SYNC_STEP_MS = 25
        private const val CALIBRATION_STEP_MS = 10
        private const val EXTRA_FILE_ID = "fileId"
        private const val EXTRA_AUDIO_OFFSET_MS = "audioOffsetMs"
        private const val EXTRA_CALIBRATE = "calibrate"
        private const val EXTRA_PHONE = "phone"
        private const val RELEASE_DELAY_MS = 3000L
        private const val PHONE_POLL_MS = 1500L
        /** A measured gap this small is left as "in sync" in the wording (still applied). */
        private const val PHONE_IN_SYNC_MS = 20

        /** The stream URL names the file: /api/stream/{fileId}/{name}. */
        fun fileIdFromUrl(url: String): Long =
            Regex("/api/stream/(\\d+)/").find(url)?.groupValues?.get(1)?.toLongOrNull() ?: 0L

        /** Loops the server's flash-and-beep clip to set this device's offset. */
        fun calibrationIntent(ctx: Context, server: String, cookie: String = "", phone: Boolean = false): Intent =
            Intent(ctx, PlayerActivity::class.java)
                .putExtra(EXTRA_SERVER, server)
                .putExtra(EXTRA_COOKIE, cookie)
                .putExtra(EXTRA_URL, "$server/api/lipsync-calibration.mkv")
                .putExtra(EXTRA_CALIBRATE, true)
                .putExtra(EXTRA_PHONE, phone)
        private const val COUNTDOWN_S = 10
        /** Ask "still watching?" instead of counting down after this many episodes in a row with no button pressed. */
        private const val STILL_WATCHING_AFTER = 3
        private const val GIVE_UP_MS = 30 * 60_000L
        private const val EXTRA_SERVER = "server"
        private const val EXTRA_COOKIE = "cookie"
        private const val EXTRA_URL = "url"
        private const val EXTRA_TITLE = "title"
        private const val EXTRA_SUBTITLE = "subtitle"
        private const val EXTRA_OVERVIEW = "overview"
        private const val EXTRA_ITEM_ID = "itemId"
        private const val EXTRA_KIND = "kind"
        private const val EXTRA_START_MS = "startMs"
        /** How long after the last press on the seek bar the player jumps. */
        private const val SCRUB_SETTLE_MS = 700L
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
                .putExtra(EXTRA_OVERVIEW, req.optString("overview"))
                .putExtra(EXTRA_ITEM_ID, req.optLong("itemId"))
                .putExtra(EXTRA_KIND, req.optString("kind"))
                .putExtra(EXTRA_START_MS, (req.optDouble("startSeconds", 0.0) * 1000).toLong())
                .putExtra(EXTRA_DURATION_MS, (req.optDouble("durationSeconds", 0.0) * 1000).toLong())
                .putExtra(EXTRA_MARKERS, req.optJSONArray("markers")?.toString() ?: "[]")
                .putExtra(EXTRA_FILE_ID, req.optLong("fileId"))
                .putExtra(EXTRA_AUDIO_OFFSET_MS, req.optInt("audioOffsetMs", 0))
    }
}
