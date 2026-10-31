package app.plexodus

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.ProgressBar
import android.widget.TextView
import java.util.concurrent.Executors

/**
 * First screen: asks for the server address once, then jumps straight to the
 * web UI on every later launch.
 */
class ConnectActivity : Activity() {
    private val io = Executors.newSingleThreadExecutor()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val saved = Prefs.server(this)
        if (saved != null && !intent.getBooleanExtra(EXTRA_CHANGE, false)) {
            startActivity(Intent(this, MainActivity::class.java))
            finish()
            return
        }
        setContentView(R.layout.activity_connect)

        val field = findViewById<EditText>(R.id.server)
        val button = findViewById<Button>(R.id.connect)
        val error = findViewById<TextView>(R.id.error)
        val spinner = findViewById<ProgressBar>(R.id.spinner)
        saved?.let { field.setText(it) }

        field.setOnEditorActionListener { _, _, _ -> button.performClick(); true }
        button.setOnClickListener {
            val url = Prefs.normalize(field.text.toString())
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

    /** Returns null when the address is a reachable Plexodus server, else what went wrong. */
    private fun check(url: String): String? = try {
        val r = Http.request("$url/api/status")
        if (r.code == 200 && r.body.contains("setupRequired")) null
        else getString(R.string.connect_not_plexodus)
    } catch (e: Exception) {
        getString(R.string.connect_unreachable, url, e.message ?: e.javaClass.simpleName)
    }

    override fun onDestroy() {
        io.shutdownNow()
        super.onDestroy()
    }

    companion object {
        const val EXTRA_CHANGE = "change"
    }
}
