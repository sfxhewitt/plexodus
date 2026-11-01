package app.plexodus

import android.app.Activity
import android.app.UiModeManager
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.util.Log
import android.view.KeyEvent
import android.view.View
import android.webkit.ConsoleMessage
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.TextView
import org.json.JSONObject

/**
 * The Plexodus web UI in a WebView. The page talks to us through the
 * `PlexodusNative` object (see web/src/native.ts) to start the native player.
 */
class MainActivity : Activity() {
    private lateinit var web: WebView
    private lateinit var errorView: View
    private lateinit var server: String

    /** Fire TV, Android TV, Shield: the page shows its 10-foot interface. */
    private val isTv: Boolean by lazy {
        val ui = getSystemService(UI_MODE_SERVICE) as UiModeManager
        ui.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION ||
            packageManager.hasSystemFeature(PackageManager.FEATURE_LEANBACK) ||
            packageManager.hasSystemFeature("amazon.hardware.fire_tv")
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val saved = Prefs.server(this)
        if (saved == null) {
            startActivity(Intent(this, ConnectActivity::class.java))
            finish()
            return
        }
        server = saved
        setContentView(R.layout.activity_main)
        web = findViewById(R.id.web)
        errorView = findViewById(R.id.error)
        web.setBackgroundColor(BG)

        with(web.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            mediaPlaybackRequiresUserGesture = false
            useWideViewPort = true
            loadWithOverviewMode = true
            cacheMode = WebSettings.LOAD_DEFAULT
            userAgentString = "$userAgentString Plexodus/${BuildConfig.VERSION_NAME} Android${if (isTv) " TV" else ""}"
            // The TV interface sizes itself to the screen; the system font
            // scale (often raised on TVs) would otherwise blow every word up.
            if (isTv) textZoom = 100
        }
        if (isTv) {
            web.isVerticalScrollBarEnabled = false
            web.isHorizontalScrollBarEnabled = false
            web.overScrollMode = View.OVER_SCROLL_NEVER
            web.requestFocus()
        }
        CookieManager.getInstance().setAcceptCookie(true)
        CookieManager.getInstance().setAcceptThirdPartyCookies(web, true)
        web.addJavascriptInterface(Bridge(), "PlexodusNative")

        web.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val u = request.url
                if ((u.scheme == "http" || u.scheme == "https") && sameServer(u)) return false
                // vlc://, mailto:, other sites: hand to the system.
                try {
                    startActivity(Intent(Intent.ACTION_VIEW, u))
                } catch (_: ActivityNotFoundException) {
                }
                return true
            }

            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame) showError(error.description?.toString() ?: getString(R.string.error_unreachable))
            }

            override fun onReceivedHttpError(view: WebView, request: WebResourceRequest, errorResponse: WebResourceResponse) {
                if (request.isForMainFrame && errorResponse.statusCode >= 500) {
                    showError(getString(R.string.error_server, errorResponse.statusCode))
                }
            }

            override fun onPageFinished(view: WebView, url: String) {
                CookieManager.getInstance().flush()
            }
        }
        web.webChromeClient = object : WebChromeClient() {
            override fun onConsoleMessage(m: ConsoleMessage): Boolean {
                Log.d("PlexodusWeb", "${m.message()} (${m.sourceId()}:${m.lineNumber()})")
                return true
            }
        }

        findViewById<View>(R.id.retry).setOnClickListener {
            errorView.visibility = View.GONE
            web.loadUrl(server)
        }
        findViewById<View>(R.id.change_server).setOnClickListener { changeServer() }

        if (savedInstanceState == null) web.loadUrl(server) else web.restoreState(savedInstanceState)
    }

    private fun sameServer(u: Uri): Boolean {
        val s = Uri.parse(server)
        return u.host.equals(s.host, ignoreCase = true) && u.port == s.port
    }

    private fun showError(message: String) {
        findViewById<TextView>(R.id.error_text).text = message
        errorView.visibility = View.VISIBLE
    }

    private fun changeServer() {
        startActivity(Intent(this, ConnectActivity::class.java).putExtra(ConnectActivity.EXTRA_CHANGE, true))
        finish()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        if (::web.isInitialized) web.saveState(outState)
    }

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        if (keyCode == KeyEvent.KEYCODE_BACK && isTv && ::web.isInitialized && errorView.visibility != View.VISIBLE) {
            if (event.repeatCount > 0) return true
            // The TV page decides: close a dialog, go back a page, or leave the app.
            web.evaluateJavascript(
                "(function(){try{return window.__plexodusBack?window.__plexodusBack():'none'}catch(e){return 'none'}})()",
            ) { result ->
                when (result?.trim('"')) {
                    "handled" -> {}
                    "exit" -> finish()
                    else -> if (web.canGoBack()) web.goBack() else finish()
                }
            }
            return true
        }
        if (keyCode == KeyEvent.KEYCODE_BACK && ::web.isInitialized && errorView.visibility != View.VISIBLE && web.canGoBack()) {
            web.goBack()
            return true
        }
        return super.onKeyDown(keyCode, event)
    }

    override fun onResume() {
        super.onResume()
        if (::web.isInitialized) web.onResume()
    }

    override fun onPause() {
        if (::web.isInitialized) web.onPause()
        CookieManager.getInstance().flush()
        super.onPause()
    }

    override fun onDestroy() {
        if (::web.isInitialized) web.destroy()
        super.onDestroy()
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != REQ_PLAY) return
        // Tell the page how playback went so it can resume, show "up next", etc.
        val detail = data?.getStringExtra(PlayerActivity.EXTRA_RESULT) ?: "{}"
        web.evaluateJavascript(
            "window.dispatchEvent(new CustomEvent('plexodus:native-ended',{detail:$detail}))", null,
        )
    }

    /** Methods the web UI can call. Each runs on a WebView thread, so hop to the UI thread. */
    inner class Bridge {
        @JavascriptInterface
        fun play(json: String) {
            val req = try {
                JSONObject(json)
            } catch (e: Exception) {
                Log.w("Plexodus", "bad play request: $json")
                return
            }
            runOnUiThread {
                val cookie = CookieManager.getInstance().getCookie(server) ?: ""
                startActivityForResult(PlayerActivity.intent(this@MainActivity, server, cookie, req), REQ_PLAY)
            }
        }

        @JavascriptInterface
        fun changeServer() {
            runOnUiThread { this@MainActivity.changeServer() }
        }

        @JavascriptInterface
        fun version(): String = BuildConfig.VERSION_NAME

        @JavascriptInterface
        fun platform(): String = "android"

        @JavascriptInterface
        fun isTv(): Boolean = this@MainActivity.isTv

        @JavascriptInterface
        fun exit() {
            runOnUiThread { finish() }
        }
    }

    companion object {
        private const val REQ_PLAY = 1
        const val BG = 0xFF050505.toInt()
    }
}
