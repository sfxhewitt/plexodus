package app.plexodus

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.webkit.CookieManager
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import java.util.concurrent.Executors

/**
 * First screen. "Sign in with Plex" finds the person's Plexodus servers
 * through their Plex account; typing a server address is the fallback.
 * Once connected, later launches go straight to the web UI.
 */
class ConnectActivity : Activity() {
    private val io = Executors.newSingleThreadExecutor()
    private val ui = Handler(Looper.getMainLooper())
    private lateinit var plex: PlexLink

    /** Bumped on cancel so a running Plex sign-in stops quietly. */
    @Volatile private var plexRun = 0

    private lateinit var plexButton: Button
    private lateinit var panel: View
    private lateinit var step: TextView
    private lateinit var link: TextView
    private lateinit var enter: TextView
    private lateinit var code: TextView
    private lateinit var status: TextView
    private lateinit var servers: LinearLayout
    private lateinit var openLink: Button
    private lateinit var cancel: Button
    private lateinit var manual: View

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val saved = Prefs.server(this)
        if (saved != null && !intent.getBooleanExtra(EXTRA_CHANGE, false)) {
            startActivity(Intent(this, MainActivity::class.java))
            finish()
            return
        }
        setContentView(R.layout.activity_connect)
        plex = PlexLink(Prefs.clientId(this))

        val field = findViewById<EditText>(R.id.server)
        val button = findViewById<Button>(R.id.connect)
        val error = findViewById<TextView>(R.id.error)
        val spinner = findViewById<ProgressBar>(R.id.spinner)
        saved?.let { field.setText(it) }

        plexButton = findViewById(R.id.plex_signin)
        panel = findViewById(R.id.plex_panel)
        step = findViewById(R.id.plex_step)
        link = findViewById(R.id.plex_link)
        enter = findViewById(R.id.plex_enter)
        code = findViewById(R.id.plex_code)
        status = findViewById(R.id.plex_status)
        servers = findViewById(R.id.plex_servers)
        openLink = findViewById(R.id.plex_open)
        cancel = findViewById(R.id.plex_cancel)
        manual = findViewById(R.id.manual)

        plexButton.setOnClickListener { startPlex() }
        cancel.setOnClickListener { stopPlex() }
        openLink.setOnClickListener {
            try {
                startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://plex.tv/link")))
            } catch (_: ActivityNotFoundException) {
            }
        }
        plexButton.requestFocus()

        field.setOnEditorActionListener { _, _, _ -> button.performClick(); true }
        button.setOnClickListener {
            val typed = field.text.toString().trim()
            InviteCodes.normalize(typed)?.let { inviteCode ->
                button.isEnabled = false
                spinner.visibility = View.VISIBLE
                error.text = ""
                io.execute {
                    val found = try {
                        InviteCodes.resolve(inviteCode)?.let { u -> Prefs.normalize(u) }
                    } catch (e: Exception) {
                        runOnUiThread {
                            button.isEnabled = true
                            spinner.visibility = View.GONE
                            error.text = getString(R.string.invite_lookup_failed, e.message ?: e.javaClass.simpleName)
                        }
                        return@execute
                    }
                    val problem = if (found == null) getString(R.string.invite_unknown) else check(found)
                    runOnUiThread {
                        button.isEnabled = true
                        spinner.visibility = View.GONE
                        if (found != null && problem == null) {
                            Prefs.setServer(this, found)
                            startActivity(Intent(this, MainActivity::class.java).putExtra(MainActivity.EXTRA_START_PATH, "/invite/$inviteCode"))
                            finish()
                        } else {
                            error.text = problem
                        }
                    }
                }
                return@setOnClickListener
            }
            val url = Prefs.normalize(typed)
            if (url.isEmpty() || url.endsWith("://")) {
                error.text = getString(R.string.connect_enter_address)
                return@setOnClickListener
            }
            button.isEnabled = false
            spinner.visibility = View.VISIBLE
            error.text = ""
            io.execute {
                val problem = check(url)
                runOnUiThread {
                    button.isEnabled = true
                    spinner.visibility = View.GONE
                    if (problem == null) {
                        Prefs.setServer(this, url)
                        startActivity(Intent(this, MainActivity::class.java))
                        finish()
                    } else {
                        error.text = problem
                    }
                }
            }
        }
    }

    // ---- Sign in with Plex ----

    private fun showCode(visible: Boolean) {
        val v = if (visible) View.VISIBLE else View.GONE
        step.visibility = v
        link.visibility = v
        enter.visibility = v
        code.visibility = v
        // Phones can open plex.tv/link themselves; on a TV the person uses another device.
        openLink.visibility = if (visible && !isTv()) View.VISIBLE else View.GONE
    }

    private fun isTv(): Boolean = packageManager.hasSystemFeature("android.software.leanback") ||
        packageManager.hasSystemFeature("amazon.hardware.fire_tv")

    private fun startPlex() {
        val run = ++plexRun
        plexButton.visibility = View.GONE
        manual.visibility = View.GONE
        panel.visibility = View.VISIBLE
        servers.removeAllViews()
        showCode(false)
        status.text = getString(R.string.plex_starting)
        cancel.requestFocus()
        io.execute {
            try {
                val pin = plex.newPin()
                post(run) {
                    code.text = pin.code
                    showCode(true)
                    status.text = getString(R.string.plex_waiting)
                }
                val deadline = System.currentTimeMillis() + 15 * 60_000
                var got: String? = null
                while (got == null) {
                    if (run != plexRun) return@execute
                    if (System.currentTimeMillis() > deadline) throw PlexLink.ExpiredException()
                    Thread.sleep(2000)
                    got = plex.token(pin)
                }
                val token = got ?: return@execute
                val name = plex.username(token)
                post(run) {
                    showCode(false)
                    status.text = getString(R.string.plex_finding, name)
                }
                val found = plex.findServers(token)
                post(run) { showServers(found, token) }
            } catch (_: InterruptedException) {
            } catch (_: PlexLink.ExpiredException) {
                post(run) { failPlex(getString(R.string.plex_expired)) }
            } catch (e: Exception) {
                post(run) { failPlex(getString(R.string.plex_failed, e.message ?: e.javaClass.simpleName)) }
            }
        }
    }

    private fun post(run: Int, block: () -> Unit) {
        ui.post { if (run == plexRun && !isFinishing) block() }
    }

    private fun showServers(found: List<PlexLink.Found>, token: String) {
        when (found.size) {
            0 -> {
                status.text = getString(R.string.plex_none)
                manual.visibility = View.VISIBLE
                cancel.requestFocus()
            }
            1 -> connectWithPlex(found[0], token)
            else -> {
                status.text = getString(R.string.plex_pick)
                for (f in found) {
                    val b = Button(this).apply {
                        text = if (f.owner != null) "${f.name}\n${getString(R.string.plex_shared_by, f.owner)}" else f.name
                        isAllCaps = false
                        textSize = 16f
                        setTextColor(getColorStateList(R.color.control_fg))
                        setBackgroundResource(R.drawable.bg_server_row)
                        stateListAnimator = null
                        val pad = (14 * resources.displayMetrics.density).toInt()
                        setPadding(pad, pad, pad, pad)
                        setOnClickListener { connectWithPlex(f, token) }
                    }
                    val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
                    lp.topMargin = (10 * resources.displayMetrics.density).toInt()
                    servers.addView(b, lp)
                }
                servers.getChildAt(0)?.requestFocus()
            }
        }
    }

    private fun connectWithPlex(server: PlexLink.Found, token: String) {
        val run = plexRun
        servers.removeAllViews()
        status.text = getString(R.string.plex_signing_in, server.name)
        io.execute {
            try {
                val cookies = plex.signIn(server.url, token)
                post(run) {
                    val cm = CookieManager.getInstance()
                    cm.setAcceptCookie(true)
                    for (c in cookies) cm.setCookie(server.url, c)
                    cm.flush()
                    Prefs.setServer(this, server.url)
                    startActivity(Intent(this, MainActivity::class.java))
                    finish()
                }
            } catch (e: Exception) {
                post(run) { failPlex(e.message ?: e.javaClass.simpleName) }
            }
        }
    }

    private fun failPlex(message: String) {
        showCode(false)
        servers.removeAllViews()
        status.text = message
        manual.visibility = View.VISIBLE
        plexButton.visibility = View.VISIBLE
        plexButton.requestFocus()
    }

    private fun stopPlex() {
        plexRun++
        panel.visibility = View.GONE
        manual.visibility = View.VISIBLE
        plexButton.visibility = View.VISIBLE
        plexButton.requestFocus()
    }

    /** Returns null when the address is a reachable Plexodus server, else what went wrong. */
    private fun check(url: String): String? = try {
        val r = Http.request("$url/api/status")
        if (r.code == 200 && r.body.contains("setupRequired")) null
        else getString(R.string.connect_not_plexodus)
    } catch (e: Exception) {
        getString(R.string.connect_unreachable, url, e.message ?: e.javaClass.simpleName)
    }

    override fun onDestroy() {
        plexRun++
        io.shutdownNow()
        super.onDestroy()
    }

    companion object {
        const val EXTRA_CHANGE = "change"
    }
}
