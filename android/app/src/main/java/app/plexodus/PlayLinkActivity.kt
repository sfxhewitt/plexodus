package app.plexodus

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Bundle

/**
 * plexodus://play?q=<title>[&from=start]: other apps and voice assistants
 * (LUMEN's Alexa skill) start a show or film by name. The web UI finds it and
 * plays it like pressing Play: a show carries on from where you left off.
 * Says "season 2 episode 3" or "from the start" in q are understood too.
 */
class PlayLinkActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val link = intent?.data
        val said = link?.getQueryParameter("q")?.trim()?.take(200).orEmpty()
        if (Prefs.server(this) == null || said.isEmpty()) {
            startActivity(Intent(this, ConnectActivity::class.java))
        } else {
            val path = Uri.Builder().path("/voice").appendQueryParameter("q", said).apply {
                if (link?.getQueryParameter("from") == "start") appendQueryParameter("from", "start")
            }.build().toString()
            // The app's own screen, reused if it's open (anything playing closes for the new title).
            startActivity(
                Intent(this, MainActivity::class.java)
                    .putExtra(MainActivity.EXTRA_START_PATH, path)
                    .addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP),
            )
        }
        finish()
    }
}
