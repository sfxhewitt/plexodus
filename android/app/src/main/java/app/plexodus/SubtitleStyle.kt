package app.plexodus

import android.content.Context
import android.graphics.Color
import android.graphics.Typeface
import android.widget.TextView
import org.json.JSONObject

/**
 * How text subtitles (SRT and other plain text tracks) look on this device.
 * libVLC draws them with its freetype renderer, so the style becomes
 * start-up options for libVLC; changing it rebuilds the player.
 *
 * The presets follow what people rate as the best-looking subtitles on TVs:
 * white or off-white text, a moderate size, a thin outline or soft shadow
 * (Netflix, Infuse), or a dark see-through box (Apple TV's default).
 * Styled .ass tracks keep their own look; picture subtitles (PGS) can't be styled.
 */
data class SubtitleStyle(
    val size: Int = 2,
    val font: Int = 0,
    val bold: Boolean = false,
    val colour: Int = 0,
    val edge: Int = 3,
    val background: Int = 0,
    val position: Int = 0,
) {
    /** libVLC options for this style. [screenHeightPx] places raised subtitles. */
    fun vlcOptions(screenHeightPx: Int): List<String> {
        val opts = arrayListOf(
            "--freetype-rel-fontsize=" + SIZES[size.coerceIn(SIZES.indices)].second,
            "--freetype-font=" + FONTS[font.coerceIn(FONTS.indices)].second,
            if (bold) "--freetype-bold" else "--no-freetype-bold",
            "--freetype-color=" + COLOURS[colour.coerceIn(COLOURS.indices)].second,
            "--freetype-opacity=255",
        )
        when (edge) {
            EDGE_NONE -> opts += listOf("--freetype-outline-thickness=0", "--freetype-shadow-opacity=0")
            EDGE_SHADOW -> opts += listOf(
                "--freetype-outline-thickness=0",
                "--freetype-shadow-opacity=210", "--freetype-shadow-color=0",
                "--freetype-shadow-angle=-60", "--freetype-shadow-distance=0.05",
            )
            EDGE_OUTLINE -> opts += listOf(
                "--freetype-outline-thickness=4", "--freetype-outline-opacity=255", "--freetype-outline-color=0",
                "--freetype-shadow-opacity=0",
            )
            else -> opts += listOf(
                // Thin outline plus a soft drop shadow: crisp on bright scenes, not heavy.
                "--freetype-outline-thickness=2", "--freetype-outline-opacity=220", "--freetype-outline-color=0",
                "--freetype-shadow-opacity=150", "--freetype-shadow-color=0",
                "--freetype-shadow-angle=-60", "--freetype-shadow-distance=0.06",
            )
        }
        val box = BACKGROUNDS[background.coerceIn(BACKGROUNDS.indices)].second
        opts += if (box > 0) listOf("--freetype-background-opacity=$box", "--freetype-background-color=0")
        else listOf("--freetype-background-opacity=0")
        val margin = (screenHeightPx * POSITIONS[position.coerceIn(POSITIONS.indices)].second).toInt()
        if (margin != 0) opts += "--sub-margin=$margin"
        return opts
    }

    /** The preset this matches, or -1 for a custom mix. */
    fun preset(): Int = PRESETS.indexOfFirst { it.second == this }

    /** Draws [view] the way libVLC will draw this style, at the real size on a screen [screenHeightDp] tall. */
    fun preview(view: TextView, screenHeightDp: Float) {
        val dp = view.resources.displayMetrics.density
        view.textSize = screenHeightDp / SIZES[size.coerceIn(SIZES.indices)].second * 0.92f
        view.typeface = Typeface.create(FONTS[font.coerceIn(FONTS.indices)].second, if (bold) Typeface.BOLD else Typeface.NORMAL)
        view.setTextColor(Color.rgb(COLOURS[colour.coerceIn(COLOURS.indices)].second shr 16 and 0xFF,
            COLOURS[colour.coerceIn(COLOURS.indices)].second shr 8 and 0xFF,
            COLOURS[colour.coerceIn(COLOURS.indices)].second and 0xFF))
        when (edge) {
            EDGE_NONE -> view.setShadowLayer(0f, 0f, 0f, 0)
            EDGE_SHADOW -> view.setShadowLayer(3f * dp, 1f * dp, 1.5f * dp, Color.argb(220, 0, 0, 0))
            EDGE_OUTLINE -> view.setShadowLayer(1.2f * dp, 0f, 0f, Color.BLACK)
            else -> view.setShadowLayer(2f * dp, 0.8f * dp, 1.2f * dp, Color.argb(230, 0, 0, 0))
        }
        val box = BACKGROUNDS[background.coerceIn(BACKGROUNDS.indices)].second
        view.setBackgroundColor(if (box > 0) Color.argb(box, 0, 0, 0) else Color.TRANSPARENT)
    }

    fun toJson(): String = JSONObject()
        .put("size", size).put("font", font).put("bold", bold).put("colour", colour)
        .put("edge", edge).put("background", background).put("position", position)
        .toString()

    companion object {
        const val EDGE_NONE = 0
        const val EDGE_SHADOW = 1
        const val EDGE_OUTLINE = 2
        const val EDGE_BOTH = 3

        /** Label and libVLC's --freetype-rel-fontsize: the text is 1/N of the picture's height. */
        val SIZES = listOf("Tiny" to 36, "Small" to 31, "Medium" to 27, "Large" to 23, "Huge" to 19)
        /** Label and an Android system font family (libVLC finds it in the system's font list). */
        val FONTS = listOf("Sans" to "sans-serif", "Condensed" to "sans-serif-condensed", "Serif" to "serif", "Typewriter" to "monospace")
        /** Label and colour (0xRRGGBB). Soft white is easier on the eyes in HDR. */
        val COLOURS = listOf("White" to 0xFFFFFF, "Soft white" to 0xE6E3DC, "Yellow" to 0xFFE04A, "Cyan" to 0x9BE7FF)
        val EDGES = listOf("None", "Drop shadow", "Outline", "Outline + shadow")
        /** Label and the box's opacity (0–255). */
        val BACKGROUNDS = listOf("None" to 0, "Soft box" to 120, "Solid box" to 215)
        /**
         * Label and the share of the screen height the subtitles are moved up by
         * (--sub-margin). libVLC already leaves 4% of the picture below them, so
         * "Very bottom" pulls them down into that gap: libVLC stops them at the
         * picture's edge, leaving just the outline's few pixels.
         */
        val POSITIONS = listOf("Very bottom" to -0.036f, "Bottom" to 0f, "Raised" to 0.05f, "High" to 0.1f)

        val DEFAULT = SubtitleStyle()

        val PRESETS = listOf(
            // Netflix / Infuse: white, thin outline and a soft shadow.
            "Cinema" to DEFAULT,
            // Apple TV's default: white on a dark see-through box.
            "Boxed" to SubtitleStyle(size = 2, edge = EDGE_NONE, background = 1),
            // Small, soft white with only a shadow: barely there.
            "Minimal" to SubtitleStyle(size = 1, colour = 1, edge = EDGE_SHADOW),
            // DVD era: yellow with a bold outline.
            "Classic" to SubtitleStyle(size = 2, bold = true, colour = 2, edge = EDGE_OUTLINE),
            // Easiest to read: bigger, bold, on a solid box.
            "Large print" to SubtitleStyle(size = 3, bold = true, edge = EDGE_NONE, background = 2),
        )

        private const val KEY = "subtitleStyle"

        fun load(ctx: Context): SubtitleStyle {
            val p = ctx.getSharedPreferences("plexodus", Context.MODE_PRIVATE)
            val s = p.getString(KEY, null) ?: return DEFAULT
            return try {
                val o = JSONObject(s)
                SubtitleStyle(
                    size = o.optInt("size", 2).coerceIn(SIZES.indices),
                    font = o.optInt("font", 0).coerceIn(FONTS.indices),
                    bold = o.optBoolean("bold", false),
                    colour = o.optInt("colour", 0).coerceIn(COLOURS.indices),
                    edge = o.optInt("edge", EDGE_BOTH).coerceIn(EDGES.indices),
                    background = o.optInt("background", 0).coerceIn(BACKGROUNDS.indices),
                    position = o.optInt("position", 0).coerceIn(POSITIONS.indices),
                )
            } catch (_: Exception) {
                DEFAULT
            }
        }

        fun save(ctx: Context, style: SubtitleStyle) {
            ctx.getSharedPreferences("plexodus", Context.MODE_PRIVATE).edit().putString(KEY, style.toJson()).apply()
        }
    }
}
