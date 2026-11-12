package app.plexodus

import android.app.Activity
import android.app.AlertDialog
import android.content.ActivityNotFoundException
import android.content.ContentProvider
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.OpenableColumns
import android.provider.Settings
import android.util.Log
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest

/**
 * Keeps the app up to date by itself. Every build of main publishes
 * `android-version.json` next to `plexodus.apk` in the rolling "latest"
 * release; the app reads it on start (at most every few hours) and from
 * "Check for updates" in TV Settings. When the build there is newer, it
 * offers it, downloads the APK into its own cache, checks the SHA-256 and
 * that it really is a newer Plexodus, and hands it to the system installer.
 * Every release is signed with the same key, so it installs over the top and
 * keeps the server address and sign-in.
 */
class Updater(private val activity: Activity) {
    class Release(val code: Int, val name: String, val url: String, val size: Long, val sha256: String)

    private var dialog: AlertDialog? = null
    @Volatile private var cancelled = false
    @Volatile private var busy = false
    /** Waiting for the user to allow installing apps in system settings. */
    private var pendingInstall: Release? = null

    /** On start and resume: looks quietly, and only speaks up when there is an update. */
    fun autoCheck() {
        if (!enabled() || busy || dialog?.isShowing == true) return
        pendingInstall?.let { r ->
            // Back from the "install unknown apps" screen.
            pendingInstall = null
            if (activity.packageManager.canRequestPackageInstalls()) download(r)
            return
        }
        val p = prefs(activity)
        val now = System.currentTimeMillis()
        if (now - p.getLong(KEY_LAST_CHECK, 0) < CHECK_EVERY_MS) return
        p.edit().putLong(KEY_LAST_CHECK, now).apply()
        busy = true
        Thread {
            val r = try {
                fetch()
            } catch (e: Exception) {
                Log.i(TAG, "update check failed: $e")
                null
            }
            activity.runOnUiThread {
                busy = false
                if (r != null && r.code > BuildConfig.VERSION_CODE && alive()) offer(r)
            }
        }.start()
    }

    /** "Check for updates" in Settings: always answers. */
    fun manualCheck() {
        if (busy) return
        if (!enabled()) {
            show(alert().setTitle(R.string.update_title).setMessage(R.string.update_dev_build).setPositiveButton(android.R.string.ok, null))
            return
        }
        busy = true
        show(alert().setTitle(R.string.update_title).setMessage(R.string.update_checking).setNegativeButton(R.string.cancel, null))
        Thread {
            val result = runCatching { fetch() }
            activity.runOnUiThread {
                busy = false
                if (!alive()) return@runOnUiThread
                val checking = dialog
                // Closed with Cancel while we looked: say nothing.
                if (checking == null || !checking.isShowing) return@runOnUiThread
                checking.dismiss()
                val r = result.getOrNull()
                when {
                    r == null -> show(
                        alert().setTitle(R.string.update_title)
                            .setMessage(activity.getString(R.string.update_check_failed, result.exceptionOrNull()?.message ?: "?"))
                            .setPositiveButton(android.R.string.ok, null),
                    )
                    r.code > BuildConfig.VERSION_CODE -> offer(r)
                    else -> show(
                        alert().setTitle(R.string.update_up_to_date)
                            .setMessage(activity.getString(R.string.update_up_to_date_text, BuildConfig.VERSION_NAME))
                            .setPositiveButton(android.R.string.ok, null),
                    )
                }
                prefs(activity).edit().putLong(KEY_LAST_CHECK, System.currentTimeMillis()).apply()
            }
        }.start()
    }

    fun dismiss() {
        cancelled = true
        dialog?.dismiss()
        dialog = null
    }

    private fun offer(r: Release) {
        val mb = (r.size / 1_000_000).coerceAtLeast(1)
        show(
            alert().setTitle(R.string.update_available)
                .setMessage(activity.getString(R.string.update_available_text, r.name, BuildConfig.VERSION_NAME, mb))
                .setPositiveButton(R.string.update_now) { _, _ -> start(r) }
                .setNegativeButton(R.string.update_later, null),
        )
    }

    private fun start(r: Release) {
        if (activity.packageManager.canRequestPackageInstalls()) {
            download(r)
            return
        }
        // Android asks once per app before it may install updates. Send the
        // user to that switch; when they come back we carry on.
        show(
            alert().setTitle(R.string.update_allow_title)
                .setMessage(R.string.update_allow_text)
                .setPositiveButton(R.string.update_open_settings) { _, _ ->
                    pendingInstall = r
                    if (!openUnknownSourcesSettings()) {
                        pendingInstall = null
                        download(r)
                    }
                }
                // The system installer asks too, so going ahead also works.
                .setNeutralButton(R.string.update_continue) { _, _ -> download(r) }
                .setNegativeButton(R.string.cancel, null),
        )
    }

    private fun openUnknownSourcesSettings(): Boolean {
        val tries = listOf(
            Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${activity.packageName}")),
            Intent(Settings.ACTION_SECURITY_SETTINGS),
        )
        for (i in tries) {
            try {
                activity.startActivity(i)
                return true
            } catch (_: ActivityNotFoundException) {
            } catch (_: SecurityException) {
            }
        }
        return false
    }

    private fun download(r: Release) {
        cancelled = false
        busy = true
        val pad = (24 * activity.resources.displayMetrics.density).toInt()
        val text = TextView(activity).apply { text = activity.getString(R.string.update_downloading, 0) }
        val bar = ProgressBar(activity, null, android.R.attr.progressBarStyleHorizontal).apply { max = 1000 }
        val box = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad / 2, pad, 0)
            addView(text)
            addView(bar)
        }
        show(
            alert().setTitle(activity.getString(R.string.update_title_version, r.name)).setView(box)
                .setNegativeButton(R.string.cancel) { _, _ -> cancelled = true }
                .setOnCancelListener { cancelled = true },
        )
        Thread {
            val result = runCatching { fetchApk(r) { done, total ->
                val frac = if (total > 0) done.toDouble() / total else 0.0
                activity.runOnUiThread {
                    bar.progress = (frac * 1000).toInt()
                    text.text = activity.getString(R.string.update_downloading, (frac * 100).toInt())
                }
            } }
            activity.runOnUiThread {
                busy = false
                if (!alive()) return@runOnUiThread
                dialog?.dismiss()
                dialog = null
                if (cancelled) return@runOnUiThread
                val e = result.exceptionOrNull()
                if (e != null) {
                    Log.w(TAG, "update download failed", e)
                    show(
                        alert().setTitle(R.string.update_title)
                            .setMessage(activity.getString(R.string.update_failed, e.message ?: e.toString()))
                            .setPositiveButton(R.string.retry) { _, _ -> download(r) }
                            .setNegativeButton(R.string.cancel, null),
                    )
                } else {
                    install()
                }
            }
        }.start()
    }

    /** Downloads (or reuses an earlier download of) this release and checks it. */
    private fun fetchApk(r: Release, progress: (Long, Long) -> Unit) {
        val file = apkFile(activity)
        file.parentFile?.mkdirs()
        if (file.exists() && file.length() == r.size && sha256(file) == r.sha256) {
            verify(file, r)
            return
        }
        file.delete()
        val dir = file.parentFile ?: activity.cacheDir
        if (r.size > 0 && dir.usableSpace < r.size + 32L * 1024 * 1024) {
            throw IOException(activity.getString(R.string.update_no_space, (r.size / 1_000_000)))
        }
        val part = File(dir, "$APK_NAME.part")
        val digest = MessageDigest.getInstance("SHA-256")
        val c = URL(r.url).openConnection() as HttpURLConnection
        try {
            c.connectTimeout = 20_000
            c.readTimeout = 30_000
            c.instanceFollowRedirects = true
            c.setRequestProperty("User-Agent", "Plexodus/${BuildConfig.VERSION_NAME} Android")
            if (c.responseCode != 200) throw IOException("HTTP ${c.responseCode}")
            val total = if (c.contentLengthLong > 0) c.contentLengthLong else r.size
            var done = 0L
            var lastReport = 0L
            c.inputStream.use { input ->
                part.outputStream().use { out ->
                    val buf = ByteArray(256 * 1024)
                    while (true) {
                        if (cancelled) throw IOException("cancelled")
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        digest.update(buf, 0, n)
                        done += n
                        val now = System.currentTimeMillis()
                        if (now - lastReport > 250) {
                            lastReport = now
                            progress(done, total)
                        }
                    }
                }
            }
            progress(done, total)
        } catch (e: Exception) {
            part.delete()
            throw e
        } finally {
            c.disconnect()
        }
        val got = digest.digest().joinToString("") { "%02x".format(it) }
        if (r.sha256.isNotEmpty() && !got.equals(r.sha256, ignoreCase = true)) {
            part.delete()
            // Usually the release was being replaced mid-download; a retry gets the new one.
            throw IOException(activity.getString(R.string.update_bad_checksum))
        }
        if (!part.renameTo(file)) throw IOException("couldn't save the download")
        verify(file, r)
    }

    /** Never hand the installer anything but a newer build of this app. */
    private fun verify(file: File, r: Release) {
        @Suppress("DEPRECATION")
        val info = activity.packageManager.getPackageArchiveInfo(file.path, 0)
        if (info == null || info.packageName != activity.packageName) {
            file.delete()
            throw IOException(activity.getString(R.string.update_bad_apk))
        }
        @Suppress("DEPRECATION")
        val code = info.versionCode
        if (code <= BuildConfig.VERSION_CODE) {
            file.delete()
            throw IOException(activity.getString(R.string.update_bad_apk))
        }
        Log.i(TAG, "update ${info.versionName} ($code) ready, offered ${r.name} (${r.code})")
    }

    private fun install() {
        val uri = Uri.parse("content://${activity.packageName}${UpdateFileProvider.AUTHORITY_SUFFIX}/$APK_NAME")
        val i = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, APK_MIME)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        try {
            activity.startActivity(i)
        } catch (e: ActivityNotFoundException) {
            show(
                alert().setTitle(R.string.update_title)
                    .setMessage(activity.getString(R.string.update_failed, e.message ?: "no installer"))
                    .setPositiveButton(android.R.string.ok, null),
            )
        }
    }

    private fun alert() = AlertDialog.Builder(activity, android.R.style.Theme_Material_Dialog_Alert)

    private fun show(b: AlertDialog.Builder) {
        if (!alive()) return
        dialog?.dismiss()
        dialog = b.create().also { it.show() }
    }

    private fun alive() = !activity.isFinishing && !activity.isDestroyed

    companion object {
        private const val TAG = "PlexodusUpdate"
        private const val KEY_LAST_CHECK = "updateLastCheck"
        private const val CHECK_EVERY_MS = 6L * 60 * 60 * 1000
        const val APK_NAME = "update.apk"
        const val APK_MIME = "application/vnd.android.package-archive"
        private val MANIFEST_URL =
            "https://github.com/${BuildConfig.UPDATE_REPO}/releases/download/latest/android-version.json"

        /** Only CI release builds update themselves; local and debug builds have version code 1. */
        fun enabled() = !BuildConfig.DEBUG && BuildConfig.VERSION_CODE > 1

        private fun prefs(ctx: Context) = ctx.getSharedPreferences("plexodus", Context.MODE_PRIVATE)

        fun apkFile(ctx: Context) = File(File(ctx.cacheDir, "updates"), APK_NAME)

        /** After an update (or with nothing newer in it), the downloaded APK is just 160 MB of clutter. */
        fun cleanup(ctx: Context) {
            Thread {
                val f = apkFile(ctx)
                if (!f.exists()) return@Thread
                @Suppress("DEPRECATION")
                val code = ctx.packageManager.getPackageArchiveInfo(f.path, 0)?.versionCode ?: 0
                if (code <= BuildConfig.VERSION_CODE) f.delete()
            }.start()
        }

        fun fetch(): Release {
            val res = Http.request(
                MANIFEST_URL,
                headers = mapOf("User-Agent" to "Plexodus/${BuildConfig.VERSION_NAME} Android", "Cache-Control" to "no-cache"),
                timeoutMs = 15_000,
            )
            if (res.code != 200) throw IOException("HTTP ${res.code}")
            val j = JSONObject(res.body)
            return Release(
                code = j.getInt("versionCode"),
                name = j.optString("versionName", j.getInt("versionCode").toString()),
                url = j.optString("url").ifEmpty {
                    "https://github.com/${BuildConfig.UPDATE_REPO}/releases/download/latest/plexodus.apk"
                },
                size = j.optLong("size", 0),
                sha256 = j.optString("sha256", ""),
            )
        }

        private fun sha256(f: File): String {
            val d = MessageDigest.getInstance("SHA-256")
            f.inputStream().use { input ->
                val buf = ByteArray(256 * 1024)
                while (true) {
                    val n = input.read(buf)
                    if (n < 0) break
                    d.update(buf, 0, n)
                }
            }
            return d.digest().joinToString("") { "%02x".format(it) }
        }
    }
}

/**
 * Lets the system installer read the downloaded update (and nothing else)
 * from our cache, without pulling in AndroidX for FileProvider.
 */
class UpdateFileProvider : ContentProvider() {
    override fun onCreate() = true

    private fun fileFor(uri: Uri): File {
        val ctx = context ?: throw IOException("no context")
        if (uri.lastPathSegment != Updater.APK_NAME) throw SecurityException("not shared: $uri")
        return Updater.apkFile(ctx)
    }

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor =
        ParcelFileDescriptor.open(fileFor(uri), ParcelFileDescriptor.MODE_READ_ONLY)

    override fun getType(uri: Uri) = Updater.APK_MIME

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor {
        val f = fileFor(uri)
        val cols = projection ?: arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE)
        val c = MatrixCursor(cols)
        c.addRow(cols.map { col ->
            when (col) {
                OpenableColumns.DISPLAY_NAME -> "plexodus.apk"
                OpenableColumns.SIZE -> f.length()
                else -> null
            }
        })
        return c
    }

    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?) = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?) = 0

    companion object {
        const val AUTHORITY_SUFFIX = ".updates"
    }
}
