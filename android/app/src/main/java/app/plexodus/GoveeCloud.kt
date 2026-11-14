package app.plexodus

import android.content.Context
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * Reads the light names ("Living Room", "Kids Room"...) from the Govee
 * account, so the light picker can say which room a bulb is in. Only names
 * come from the cloud; control stays local (LAN or Bluetooth).
 */
object GoveeCloud {
    private const val DEVICES = "https://openapi.api.govee.com/router/api/v1/user/devices"

    /** (MAC as 6 bytes, name) for every device on the account. Blocks; null on failure. */
    fun devices(key: String): List<Pair<IntArray, String>>? = try {
        val c = URL(DEVICES).openConnection() as HttpURLConnection
        c.connectTimeout = 4000; c.readTimeout = 4000
        c.setRequestProperty("Govee-API-Key", key)
        c.setRequestProperty("Content-Type", "application/json")
        val body = c.inputStream.bufferedReader().use { it.readText() }
        c.disconnect()
        val arr = JSONObject(body).optJSONArray("data")
        (0 until (arr?.length() ?: 0)).mapNotNull { i ->
            val d = arr!!.getJSONObject(i)
            val mac = mac(d.optString("device")) ?: return@mapNotNull null
            val name = d.optString("deviceName").takeIf { it.isNotBlank() } ?: return@mapNotNull null
            mac to name
        }
    } catch (_: Exception) { null }

    /** The last six bytes of a Govee device id or MAC ("8C:2E:D0:C9:07:C8:F8:16"). */
    fun mac(id: String): IntArray? {
        val parts = id.split(':', '-').mapNotNull { it.toIntOrNull(16) }
        return if (parts.size >= 6) parts.takeLast(6).toIntArray() else null
    }

    /**
     * Whether a light seen on the LAN or over Bluetooth is this account device.
     * A Govee light's Bluetooth address is its Wi-Fi MAC with the last byte
     * one or two higher, so that byte is allowed to differ a little.
     */
    fun same(account: IntArray, seen: IntArray): Boolean =
        (0 until 5).all { account[it] == seen[it] } && kotlin.math.abs(account[5] - seen[5]) <= 2

    /**
     * Fills in names for any of [lights] that don't have one yet, from the
     * Govee account if a key is set. Saves them, so this only runs once.
     */
    fun nameLights(ctx: Context, lights: List<RoomLight.Companion.Found>) {
        val missing = lights.filter { Prefs.lightName(ctx, it.ip) == null }
        if (missing.isEmpty()) return
        val key = Prefs.goveeKey(ctx) ?: return
        val devices = devices(key) ?: return
        for (l in missing) {
            val seen = mac(if (l.bluetooth) l.ip.removePrefix(RoomLight.BLE_PREFIX) else l.id) ?: continue
            devices.firstOrNull { same(it.first, seen) }?.let { Prefs.setLightName(ctx, l.ip, it.second) }
        }
    }
}
