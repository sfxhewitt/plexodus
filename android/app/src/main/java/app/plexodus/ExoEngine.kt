package app.plexodus

import android.content.Context
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.view.View
import android.widget.FrameLayout
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.common.text.Cue
import androidx.media3.common.text.CueGroup
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.datasource.TransferListener
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.audio.AudioCapabilities
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.DefaultAudioSink
import androidx.media3.exoplayer.audio.ForwardingAudioSink
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.CaptionStyleCompat
import androidx.media3.ui.PlayerView
import androidx.media3.ui.SubtitleView
import java.util.Locale

/**
 * ExoPlayer (media3) behind [Engine]: the engine Plex's TV app plays through.
 *
 * Why it stays in sync where libVLC drifts:
 *  - Its clock is the sound card's own position (AudioTrack timestamps), so the
 *    picture follows what is actually being heard.
 *  - Each frame is queued ahead with its presentation time, snapped to the
 *    display's refresh, so it reaches the TV exactly when it's due.
 * Dolby, DTS and TrueHD the TV box can't decode itself go through FFmpeg
 * (Jellyfin's build of the media3 FFmpeg extension).
 *
 * Audio delay: ExoPlayer has none, so the sink reports its position moved on by
 * the delay. The picture follows that clock, so with a positive delay each frame
 * is shown that much before its sound is heard, i.e. the sound plays later. It
 * applies live, with no seek. Subtitles are drawn here (not by PlayerView) so they can be held back
 * by the film's own offset, keeping them on the dialogue.
 */
@OptIn(UnstableApi::class)
class ExoEngine(
    private val context: Context,
    private val decodeSurround: Boolean,
    private val subtitleStyle: SubtitleStyle,
) : Engine {
    override val name = "ExoPlayer"
    override val framesOnTime = true
    override val delayNeedsResync = false
    override var listener: Engine.Listener? = null

    private val main = Handler(Looper.getMainLooper())
    @Volatile private var delayUs = 0L
    private var subsDelayUs = 0L
    private var readBytes = 0L
    private var lastLength = -1L
    private var announcedVideo = false
    private var wasPlaying = false
    private var view: PlayerView? = null
    private var subs: SubtitleView? = null
    private var resize = 0

    /** Shifts the clock by [delayUs]: see the class comment. */
    private inner class DelayedSink(sink: AudioSink) : ForwardingAudioSink(sink) {
        override fun getCurrentPositionUs(sourceEnded: Boolean): Long {
            val p = super.getCurrentPositionUs(sourceEnded)
            return if (p == AudioSink.CURRENT_POSITION_NOT_SET) p else p + delayUs
        }
    }

    private val renderers = object : DefaultRenderersFactory(context) {
        override fun buildAudioSink(context: Context, enableFloatOutput: Boolean, enableAudioTrackPlaybackParams: Boolean): AudioSink {
            val b = DefaultAudioSink.Builder(context)
                .setEnableFloatOutput(enableFloatOutput)
                .setEnableAudioTrackPlaybackParams(enableAudioTrackPlaybackParams)
            // Android 9/10 (the Fire TV Cube) can't close a bitstream output cleanly
            // (see PlayerActivity.PASSTHROUGH_CLOSES_CLEANLY): decode everything to PCM there.
            @Suppress("DEPRECATION")
            if (decodeSurround) b.setAudioCapabilities(AudioCapabilities.DEFAULT_AUDIO_CAPABILITIES)
            return DelayedSink(b.build())
        }
    }.setExtensionRendererMode(DefaultRenderersFactory.EXTENSION_RENDERER_MODE_ON)
        .setEnableDecoderFallback(true)

    private val counting = object : TransferListener {
        override fun onTransferInitializing(source: DataSource, dataSpec: DataSpec, isNetwork: Boolean) {}
        override fun onTransferStart(source: DataSource, dataSpec: DataSpec, isNetwork: Boolean) {}
        override fun onBytesTransferred(source: DataSource, dataSpec: DataSpec, isNetwork: Boolean, bytesTransferred: Int) {
            readBytes += bytesTransferred
        }
        override fun onTransferEnd(source: DataSource, dataSpec: DataSpec, isNetwork: Boolean) {}
    }

    private val http = DefaultHttpDataSource.Factory()
        .setUserAgent("Plexodus")
        .setAllowCrossProtocolRedirects(true)
        .setConnectTimeoutMs(15_000)
        .setReadTimeoutMs(30_000)
        .setTransferListener(counting)

    private val player: ExoPlayer = ExoPlayer.Builder(context, renderers)
        .setMediaSourceFactory(DefaultMediaSourceFactory(DefaultDataSource.Factory(context, http)))  // http(s), plus file:// and content://
        .setLoadControl(
            DefaultLoadControl.Builder()
                // Big remuxed films over the internet: keep a good cushion.
                .setBufferDurationsMs(30_000, 120_000, 2_500, 5_000)
                .setPrioritizeTimeOverSizeThresholds(true)
                .build(),
        )
        .setSeekBackIncrementMs(10_000)
        .setSeekForwardIncrementMs(30_000)
        .build()

    private val tick = object : Runnable {
        override fun run() {
            // Like libVLC's TimeChanged: only while the film moves.
            if (player.isPlaying) listener?.onTime(player.currentPosition)
            val d = player.duration
            if (d != C.TIME_UNSET && d > 0 && d != lastLength) {
                lastLength = d
                listener?.onLength(d)
            }
            main.postDelayed(this, 250)
        }
    }

    init {
        player.addListener(object : Player.Listener {
            override fun onIsPlayingChanged(isPlaying: Boolean) {
                if (isPlaying) {
                    wasPlaying = true
                    listener?.onPlaying()
                } else if (!player.playWhenReady) {
                    listener?.onPaused()
                }
            }

            override fun onPlaybackStateChanged(state: Int) {
                when (state) {
                    Player.STATE_BUFFERING -> listener?.onBuffering(0f)
                    Player.STATE_READY -> listener?.onBuffering(100f)
                    Player.STATE_ENDED -> listener?.onEnded()
                }
            }

            override fun onTracksChanged(tracks: Tracks) {
                listener?.onTracksChanged()
            }

            override fun onRenderedFirstFrame() {
                if (!announcedVideo) {
                    announcedVideo = true
                    listener?.onVideoOut()
                }
            }

            override fun onCues(cueGroup: CueGroup) {
                showCues(cueGroup.cues)
            }

            override fun onPlayerError(error: PlaybackException) {
                android.util.Log.w("Plexodus", "ExoPlayer error ${error.errorCodeName}", error)
                listener?.onError(error.errorCodeName)
            }
        })
        main.post(tick)
    }

    // Subtitles: held back by the film's offset so they stay on the dialogue.
    private fun showCues(cues: List<Cue>) {
        val delayMs = subsDelayUs / 1000
        if (delayMs <= 0) subs?.setCues(cues)
        else main.postDelayed({ subs?.setCues(cues) }, delayMs)
    }

    override fun attach(container: FrameLayout) {
        val pv = PlayerView(container.context).apply {
            useController = false
            setShutterBackgroundColor(android.graphics.Color.BLACK)
            setKeepContentOnPlayerReset(true)
            resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
            subtitleView?.visibility = View.GONE // ours below, with the offset
            isFocusable = false
            descendantFocusability = android.view.ViewGroup.FOCUS_BLOCK_DESCENDANTS
        }
        pv.player = player
        container.addView(pv, FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT))
        val sv = SubtitleView(container.context)
        styleSubtitles(sv, container.resources.displayMetrics.heightPixels)
        container.addView(sv, FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT))
        view = pv
        subs = sv
    }

    private fun styleSubtitles(sv: SubtitleView, screenHeightPx: Int) {
        val st = subtitleStyle
        val fg = SubtitleStyle.COLOURS[st.colour.coerceIn(SubtitleStyle.COLOURS.indices)].second or 0xFF000000.toInt()
        val box = SubtitleStyle.BACKGROUNDS[st.background.coerceIn(SubtitleStyle.BACKGROUNDS.indices)].second
        val edge = when (st.edge) {
            SubtitleStyle.EDGE_NONE -> CaptionStyleCompat.EDGE_TYPE_NONE
            SubtitleStyle.EDGE_SHADOW -> CaptionStyleCompat.EDGE_TYPE_DROP_SHADOW
            SubtitleStyle.EDGE_OUTLINE -> CaptionStyleCompat.EDGE_TYPE_OUTLINE
            else -> CaptionStyleCompat.EDGE_TYPE_DROP_SHADOW
        }
        val face = android.graphics.Typeface.create(
            SubtitleStyle.FONTS[st.font.coerceIn(SubtitleStyle.FONTS.indices)].second,
            if (st.bold) android.graphics.Typeface.BOLD else android.graphics.Typeface.NORMAL,
        )
        sv.setStyle(CaptionStyleCompat(fg, android.graphics.Color.argb(box, 0, 0, 0), android.graphics.Color.TRANSPARENT, edge, android.graphics.Color.BLACK, face))
        // Same size as libVLC's relative font size: the screen height divided by it.
        val rel = SubtitleStyle.SIZES[st.size.coerceIn(SubtitleStyle.SIZES.indices)].second
        sv.setFractionalTextSize(0.92f / rel, true)
        sv.setApplyEmbeddedStyles(true)
        sv.setApplyEmbeddedFontSizes(false)
        sv.setBottomPaddingFraction(0.02f)
    }

    override fun detach() {
        view?.player = null
        (view?.parent as? FrameLayout)?.let { p ->
            p.removeView(view)
            p.removeView(subs)
        }
        view = null
        subs = null
    }

    // Subtitle files ride along with the film as extra text tracks. ExoPlayer
    // takes them when the film is opened, so a change reopens it where it was.
    private var extSubs: List<Engine.ExtSub> = emptyList()
    private var loadedUrl: String? = null

    override fun setExternalSubtitles(subs: List<Engine.ExtSub>): Boolean {
        if (subs == extSubs) return true
        extSubs = subs
        val url = loadedUrl ?: return true
        val playing = player.playWhenReady
        player.setMediaItem(mediaItem(url), player.currentPosition)
        player.prepare()
        player.playWhenReady = playing
        return true
    }

    private fun mediaItem(url: String): MediaItem {
        val configs = extSubs.map { sub ->
            MediaItem.SubtitleConfiguration.Builder(android.net.Uri.fromFile(java.io.File(sub.path)))
                .setMimeType(
                    when (sub.codec) {
                        "ass", "ssa" -> MimeTypes.TEXT_SSA
                        "vtt", "webvtt" -> MimeTypes.TEXT_VTT
                        else -> MimeTypes.APPLICATION_SUBRIP
                    },
                )
                .setLanguage(sub.lang.ifEmpty { null })
                .setLabel(sub.name)
                .build()
        }
        return MediaItem.Builder().setUri(url).setSubtitleConfigurations(configs).build()
    }

    override fun load(url: String, startMs: Long) {
        announcedVideo = false
        lastLength = -1L
        loadedUrl = url
        player.setMediaItem(mediaItem(url), if (startMs > 5_000) startMs else 0L)
        player.prepare()
        player.playWhenReady = true
    }

    override fun play() {
        if (player.playbackState == Player.STATE_ENDED) player.seekTo(0)
        player.play()
    }

    override fun pause() = player.pause()
    override fun stop() = player.stop()

    override fun release(later: (Runnable) -> Unit) {
        main.removeCallbacksAndMessages(null)
        listener = null
        detach()
        player.release()
    }

    override val isPlaying get() = player.isPlaying
    override val time get() = player.currentPosition
    override fun setTime(ms: Long) = player.seekTo(ms.coerceAtLeast(0))
    override val rate get() = player.playbackParameters.speed

    override var audioDelayUs: Long
        get() = delayUs
        set(v) { delayUs = v }
    override var spuDelayUs: Long
        get() = subsDelayUs
        set(v) { subsDelayUs = v }

    // ---- tracks: ids are (group << 8) | index within the group ----

    private fun groups(type: Int) = player.currentTracks.groups.withIndex().filter { it.value.type == type }

    private fun list(type: Int): List<Engine.Track> {
        val out = ArrayList<Engine.Track>()
        var n = 0
        for ((gi, g) in groups(type)) {
            for (i in 0 until g.length) {
                if (!g.isTrackSupported(i)) continue
                n++
                out += Engine.Track((gi shl 8) or i, describe(g.getTrackFormat(i), n, type))
            }
        }
        return out
    }

    private fun selected(type: Int): Int {
        for ((gi, g) in groups(type)) for (i in 0 until g.length) if (g.isTrackSelected(i)) return (gi shl 8) or i
        return -1
    }

    private fun select(type: Int, id: Int) {
        val b = player.trackSelectionParameters.buildUpon()
        if (id < 0) {
            b.setTrackTypeDisabled(type, true)
        } else {
            val g = player.currentTracks.groups.getOrNull(id shr 8) ?: return
            b.setTrackTypeDisabled(type, false)
            b.setOverrideForType(TrackSelectionOverride(g.mediaTrackGroup, id and 0xff))
        }
        player.trackSelectionParameters = b.build()
    }

    override val audioTracks get() = list(C.TRACK_TYPE_AUDIO)
    override var audioTrack: Int
        get() = selected(C.TRACK_TYPE_AUDIO)
        set(v) = select(C.TRACK_TYPE_AUDIO, v)
    override val spuTracks: List<Engine.Track>
        get() {
            val l = list(C.TRACK_TYPE_TEXT)
            return if (l.isEmpty()) l else listOf(Engine.Track(-1, "Disable")) + l
        }
    override var spuTrack: Int
        get() = if (player.trackSelectionParameters.disabledTrackTypes.contains(C.TRACK_TYPE_TEXT)) -1 else selected(C.TRACK_TYPE_TEXT)
        set(v) = select(C.TRACK_TYPE_TEXT, v)

    private fun describe(f: Format, n: Int, type: Int): String {
        val parts = ArrayList<String>()
        f.label?.takeIf { it.isNotBlank() }?.let { parts += it }
        f.language?.takeIf { it != "und" && it.isNotBlank() }?.let { lang ->
            val name = Locale.forLanguageTag(lang).getDisplayLanguage(Locale.ENGLISH)
            if (parts.none { it.contains(name, ignoreCase = true) }) parts += name
        }
        if (type == C.TRACK_TYPE_AUDIO) {
            codecName(f.sampleMimeType)?.let { parts += it }
            when (f.channelCount) {
                1 -> parts += "Mono"
                2 -> parts += "Stereo"
                6 -> parts += "5.1"
                8 -> parts += "7.1"
                else -> if (f.channelCount > 0) parts += "${f.channelCount} ch"
            }
        } else if (f.sampleMimeType == MimeTypes.TEXT_SSA) {
            parts += "ASS"
        } else if (f.sampleMimeType == MimeTypes.APPLICATION_PGS) {
            parts += "PGS"
        }
        if ((f.selectionFlags and C.SELECTION_FLAG_FORCED) != 0) parts += "Forced"
        return if (parts.isEmpty()) "Track $n" else "Track $n - " + parts.joinToString(" · ")
    }

    private fun codecName(mime: String?) = when (mime) {
        MimeTypes.AUDIO_AC3 -> "Dolby Digital"
        MimeTypes.AUDIO_E_AC3, MimeTypes.AUDIO_E_AC3_JOC -> "Dolby Digital+"
        MimeTypes.AUDIO_TRUEHD -> "TrueHD"
        MimeTypes.AUDIO_DTS -> "DTS"
        MimeTypes.AUDIO_DTS_HD -> "DTS-HD"
        MimeTypes.AUDIO_AAC -> "AAC"
        MimeTypes.AUDIO_FLAC -> "FLAC"
        MimeTypes.AUDIO_OPUS -> "Opus"
        MimeTypes.AUDIO_MPEG -> "MP3"
        null -> null
        else -> mime.substringAfter('/').uppercase(Locale.US)
    }

    override fun videoInfo(): Engine.VideoInfo? {
        val f = player.videoFormat ?: return null
        val codec = when (f.sampleMimeType) {
            MimeTypes.VIDEO_H265 -> "HEVC"
            MimeTypes.VIDEO_H264 -> "H264"
            MimeTypes.VIDEO_AV1 -> "AV1"
            MimeTypes.VIDEO_VP9 -> "VP9"
            else -> f.sampleMimeType?.substringAfter('/')?.uppercase(Locale.US) ?: "?"
        }
        return Engine.VideoInfo(codec, f.width, f.height, if (f.frameRate > 0) f.frameRate else 0f)
    }

    override fun stats(): Engine.Stats? {
        val v = player.videoDecoderCounters
        val a = player.audioDecoderCounters
        val mbps = (player.videoFormat?.bitrate?.takeIf { it > 0 } ?: 0) / 1e6 +
            (player.audioFormat?.bitrate?.takeIf { it > 0 } ?: 0) / 1e6
        return Engine.Stats(
            readBytes, mbps,
            v?.renderedOutputBufferCount ?: 0, (v?.droppedBufferCount ?: 0) + (v?.skippedOutputBufferCount ?: 0),
            a?.renderedOutputBufferCount ?: 0, (a?.droppedBufferCount ?: 0) + (a?.skippedOutputBufferCount ?: 0),
        )
    }

    override fun cycleScale(): String {
        val modes = listOf(
            AspectRatioFrameLayout.RESIZE_MODE_FIT to "fit",
            AspectRatioFrameLayout.RESIZE_MODE_ZOOM to "fill",
            AspectRatioFrameLayout.RESIZE_MODE_FILL to "stretch",
        )
        resize = (resize + 1) % modes.size
        view?.resizeMode = modes[resize].first
        return modes[resize].second
    }

    // Passthrough follows the HDMI sink's capabilities by itself (headphones
    // connected or not), so there is nothing to switch here.
    override fun setDigitalOutput(enabled: Boolean) {}

    override fun subtitleView(): View? = subs

    companion object {
        /** Whether the device decodes surround itself rather than passing it through (see PlayerActivity). */
        fun decodeSurroundOn(sdk: Int = Build.VERSION.SDK_INT) = sdk < Build.VERSION_CODES.R
    }
}
