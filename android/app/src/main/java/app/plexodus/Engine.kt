package app.plexodus

import android.view.View
import android.widget.FrameLayout

/**
 * The playback engine under the TV player. PlayerActivity drives one of these
 * and never talks to libVLC or ExoPlayer directly, so the two can be swapped
 * (Settings → Player engine) without touching the controls, sync, markers,
 * up-next and the rest.
 *
 *  - [ExoEngine]: Google's ExoPlayer (media3), the engine Plex's own TV app uses.
 *    It queues every frame ahead against the display's refresh, so picture and
 *    sound land on time with no compensation, and its clock is the sound card's.
 *  - [VlcEngine]: libVLC, the original engine. Plays anything, but hands frames
 *    over late and needs the display-lag and slow-motion workarounds.
 */
interface Engine {
    /** A selectable audio or subtitle track. Subtitles list "off" as id -1 first. */
    data class Track(val id: Int, val name: String)

    data class VideoInfo(val codec: String, val width: Int, val height: Int, val fps: Float)

    data class Stats(
        val readBytes: Long,
        val bitrateMbps: Double,
        val shownPictures: Int,
        val lostPictures: Int,
        val playedAudio: Int,
        val lostAudio: Int,
    )

    /** What the activity hears back, always on the main thread. */
    interface Listener {
        fun onPlaying()
        fun onPaused()
        fun onBuffering(percent: Float)
        fun onTime(ms: Long)
        fun onLength(ms: Long)
        fun onTracksChanged()
        fun onVideoOut()
        fun onEnded()
        fun onError(message: String)
    }

    /** Short name for logs and the stats overlay. */
    val name: String

    /** True when the engine already times frames to the display (no lag to make up). */
    val framesOnTime: Boolean

    /** True when a new audio delay only takes effect after a seek. */
    val delayNeedsResync: Boolean

    /** Puts the picture (and subtitles) inside [container]. */
    fun attach(container: FrameLayout)
    fun detach()

    fun load(url: String, startMs: Long)
    fun play()
    fun pause()
    fun stop()

    /** Frees everything. [later] runs the final release when it is safe to. */
    fun release(later: (Runnable) -> Unit)

    var listener: Listener?

    val isPlaying: Boolean
    val time: Long
    fun setTime(ms: Long)
    val rate: Float

    /** Sound offset in microseconds; positive plays it later. */
    var audioDelayUs: Long
    /** Subtitle offset in microseconds; positive shows them later. */
    var spuDelayUs: Long

    val audioTracks: List<Track>
    var audioTrack: Int
    val spuTracks: List<Track>
    var spuTrack: Int

    fun videoInfo(): VideoInfo?
    fun stats(): Stats?

    /** Next picture fit (fit, fill, stretch...). Returns its name. */
    fun cycleScale(): String

    /** Send Dolby/DTS untouched to the receiver (true) or decode to PCM. */
    fun setDigitalOutput(enabled: Boolean)

    /** The view the subtitles are drawn in, to move them about. */
    fun subtitleView(): View?
}
