package app.plexodus

import android.content.Context
import android.net.Uri
import android.view.View
import android.widget.FrameLayout
import org.videolan.libvlc.LibVLC
import org.videolan.libvlc.Media
import org.videolan.libvlc.MediaPlayer
import org.videolan.libvlc.util.VLCVideoLayout

/** libVLC behind [Engine]; see Engine for why there are two. [options] are libVLC start-up options. */
class VlcEngine(context: Context, options: List<String>) : Engine {
    private val libVlc = LibVLC(context, options)
    private val player = MediaPlayer(libVlc)
    private var layout: VLCVideoLayout? = null
    private var scaleIndex = 0

    override val name = "libVLC"
    override val framesOnTime = false
    override val delayNeedsResync = true
    override var listener: Engine.Listener? = null

    init {
        player.setEventListener { e ->
            val l = listener ?: return@setEventListener
            when (e.type) {
                MediaPlayer.Event.Playing -> l.onPlaying()
                MediaPlayer.Event.Paused -> l.onPaused()
                MediaPlayer.Event.Buffering -> l.onBuffering(e.buffering)
                MediaPlayer.Event.TimeChanged -> l.onTime(e.timeChanged)
                MediaPlayer.Event.LengthChanged -> l.onLength(e.lengthChanged)
                MediaPlayer.Event.ESAdded, MediaPlayer.Event.ESSelected -> l.onTracksChanged()
                MediaPlayer.Event.Vout -> if (e.voutCount > 0) l.onVideoOut()
                MediaPlayer.Event.EndReached -> l.onEnded()
                MediaPlayer.Event.EncounteredError -> l.onError("libVLC error")
            }
        }
    }

    override fun attach(container: FrameLayout) {
        val v = VLCVideoLayout(container.context)
        container.addView(v, FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT))
        layout = v
        player.attachViews(v, null, true, false)
    }

    override fun detach() {
        player.detachViews()
        layout?.let { (it.parent as? FrameLayout)?.removeView(it) }
        layout = null
    }

    override fun load(url: String, startMs: Long) {
        val media = Media(libVlc, Uri.parse(url))
        media.setHWDecoderEnabled(true, false)
        media.addOption(":network-caching=4000")
        if (startMs > 5_000) media.addOption(":start-time=${startMs / 1000}")
        player.setMedia(media)
        media.release()
        player.play()
    }

    override fun play() = player.play()
    override fun pause() = player.pause()
    override fun stop() = player.stop()

    override fun release(later: (Runnable) -> Unit) {
        player.setEventListener(null)
        listener = null
        player.stop()
        if (layout != null) detach()
        // Stopping closes the audio output, and Android then reports the
        // device change to libVLC on the main thread; if the player is
        // already released by then, libVLC throws and the app crashes
        // ("can't get VLCObject instance"). Release once that has passed.
        later(Runnable {
            player.release()
            libVlc.release()
        })
    }

    override val isPlaying get() = player.isPlaying
    override val time get() = player.time
    override fun setTime(ms: Long) { player.time = ms }
    override val rate get() = player.rate

    override var audioDelayUs: Long
        get() = player.audioDelay
        set(v) { player.setAudioDelay(v) }
    override var spuDelayUs: Long
        get() = player.spuDelay
        set(v) { player.setSpuDelay(v) }

    override val audioTracks: List<Engine.Track>
        get() = player.audioTracks?.map { Engine.Track(it.id, it.name) } ?: emptyList()
    override var audioTrack: Int
        get() = player.audioTrack
        set(v) { player.setAudioTrack(v) }
    override val spuTracks: List<Engine.Track>
        get() = player.spuTracks?.map { Engine.Track(it.id, it.name) } ?: emptyList()
    override var spuTrack: Int
        get() = player.spuTrack
        set(v) { player.setSpuTrack(v) }

    override fun videoInfo(): Engine.VideoInfo? {
        val v = player.currentVideoTrack ?: return null
        val fps = if (v.frameRateNum > 0 && v.frameRateDen > 0) v.frameRateNum.toFloat() / v.frameRateDen else 0f
        return Engine.VideoInfo(v.codec?.trim() ?: "?", v.width, v.height, fps)
    }

    override fun stats(): Engine.Stats? {
        val media = player.media ?: return null
        try {
            val st = media.stats ?: return null
            return Engine.Stats(
                st.readBytes.toLong() and 0xffffffffL, st.demuxBitrate * 8.0,
                st.displayedPictures, st.lostPictures, st.playedAbuffers, st.lostAbuffers,
            )
        } catch (_: Exception) {
            return null
        } finally {
            media.release()
        }
    }

    override fun cycleScale(): String {
        val types = MediaPlayer.ScaleType.getMainScaleTypes()
        scaleIndex = (scaleIndex + 1) % types.size
        player.videoScale = types[scaleIndex]
        return types[scaleIndex].name.removePrefix("SURFACE_").replace('_', ' ').lowercase()
    }

    override fun setDigitalOutput(enabled: Boolean) {
        player.setAudioDigitalOutputEnabled(enabled)
    }

    override fun subtitleView(): View? = layout?.findViewById(org.videolan.R.id.surface_subtitles)
}
