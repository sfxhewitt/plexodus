package app.plexodus

import android.Manifest
import android.annotation.SuppressLint
import android.bluetooth.BluetoothAdapter
import android.bluetooth.BluetoothDevice
import android.bluetooth.BluetoothGatt
import android.bluetooth.BluetoothGattCallback
import android.bluetooth.BluetoothGattCharacteristic
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothProfile
import android.bluetooth.le.ScanCallback
import android.bluetooth.le.ScanResult
import android.bluetooth.le.ScanSettings
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.os.Handler
import android.os.SystemClock
import java.util.UUID

/**
 * Drives a Govee light over Bluetooth LE, for lights without "LAN Control"
 * (e.g. the H6008 bulb on older firmware).
 *
 * Govee's BLE protocol: 20-byte packets written to one characteristic,
 * 0x33 + command + payload, zero padded, last byte the XOR of the other 19.
 * Colour is sent in both the old (0x02) and new (0x0D) colour-mode forms;
 * a light ignores the one it doesn't know. Brightness is folded into the
 * RGB values, so the light's own brightness setting doesn't need touching
 * (and its 0-100 vs 0-255 scale doesn't matter).
 *
 * Only the newest colour is kept while a write is in flight, so a slow link
 * drops stale frames instead of building a backlog.
 */
@SuppressLint("MissingPermission")
class GoveeBle(private val ctx: Context, private val mac: String, private val bg: Handler) {
    private var gatt: BluetoothGatt? = null
    private var ch: BluetoothGattCharacteristic? = null
    private var busy = false
    private var pending: ByteArray? = null
    private var pending2: ByteArray? = null
    private var lastWrite = 0L
    private var closed = false

    val ready get() = ch != null

    fun connect(): Boolean {
        if (!hasPermission(ctx, scan = false)) return false
        val adapter = adapter(ctx) ?: return false
        if (!adapter.isEnabled) return false
        val dev = try { adapter.getRemoteDevice(mac) } catch (_: IllegalArgumentException) { return false }
        gatt = dev.connectGatt(ctx, false, callback, BluetoothDevice.TRANSPORT_LE)
        return gatt != null
    }

    /** Sets the colour; r/g/b already scaled for brightness. */
    fun colour(r: Int, g: Int, b: Int) {
        val rr = r.coerceIn(0, 255); val gg = g.coerceIn(0, 255); val bb = b.coerceIn(0, 255)
        queue(packet(0x05, 0x0D, rr, gg, bb), packet(0x05, 0x02, rr, gg, bb))
    }

    fun power(on: Boolean) = queue(packet(0x01, if (on) 1 else 0), null)

    fun keepAlive() = queue(packet2(0xAA, 0x01), null)

    fun close() {
        closed = true
        bg.postDelayed({
            try { gatt?.disconnect(); gatt?.close() } catch (_: Exception) {}
            gatt = null; ch = null
        }, 400) // let the last write (the restore) go out first
    }

    private fun queue(a: ByteArray, b: ByteArray?) {
        bg.post {
            pending = a; pending2 = b
            if (!busy || SystemClock.uptimeMillis() - lastWrite > 1000) next()
        }
    }

    private fun next() {
        val c = ch ?: return
        val g = gatt ?: return
        val p = pending ?: pending2 ?: run { busy = false; return }
        if (pending != null) pending = null else pending2 = null
        busy = true
        lastWrite = SystemClock.uptimeMillis()
        try {
            if (Build.VERSION.SDK_INT >= 33) {
                g.writeCharacteristic(c, p, BluetoothGattCharacteristic.WRITE_TYPE_NO_RESPONSE)
            } else {
                @Suppress("DEPRECATION")
                c.writeType = BluetoothGattCharacteristic.WRITE_TYPE_NO_RESPONSE
                @Suppress("DEPRECATION")
                c.value = p
                @Suppress("DEPRECATION")
                g.writeCharacteristic(c)
            }
        } catch (_: Exception) { busy = false }
    }

    private val callback = object : BluetoothGattCallback() {
        override fun onConnectionStateChange(g: BluetoothGatt, status: Int, state: Int) {
            bg.post {
                if (state == BluetoothProfile.STATE_CONNECTED && !closed) g.discoverServices()
                else if (state == BluetoothProfile.STATE_DISCONNECTED) {
                    ch = null; busy = false
                    // Dropped (out of range, light power-cycled): keep trying while in use.
                    if (!closed) bg.postDelayed({ if (!closed) try { g.connect() } catch (_: Exception) {} }, 2000)
                }
            }
        }

        override fun onServicesDiscovered(g: BluetoothGatt, status: Int) {
            bg.post {
                ch = g.getService(SERVICE)?.getCharacteristic(CONTROL)
                busy = false
                next()
            }
        }

        override fun onCharacteristicWrite(g: BluetoothGatt, c: BluetoothGattCharacteristic, status: Int) {
            bg.post { busy = false; next() }
        }
    }

    companion object {
        private val SERVICE = UUID.fromString("00010203-0405-0607-0809-0a0b0c0d1910")
        private val CONTROL = UUID.fromString("00010203-0405-0607-0809-0a0b0c0d2b11")

        private fun packet(vararg body: Int) = packet2(0x33, *body)

        private fun packet2(head: Int, vararg body: Int): ByteArray {
            val p = ByteArray(20)
            p[0] = head.toByte()
            body.forEachIndexed { i, v -> p[i + 1] = v.toByte() }
            var x = 0
            for (i in 0 until 19) x = x xor (p[i].toInt() and 0xFF)
            p[19] = x.toByte()
            return p
        }

        fun adapter(ctx: Context): BluetoothAdapter? =
            (ctx.getSystemService(Context.BLUETOOTH_SERVICE) as? BluetoothManager)?.adapter

        /** Runtime permissions Bluetooth needs on this Android version. */
        fun permissions(): Array<String> = if (Build.VERSION.SDK_INT >= 31)
            arrayOf(Manifest.permission.BLUETOOTH_SCAN, Manifest.permission.BLUETOOTH_CONNECT)
        else arrayOf(Manifest.permission.ACCESS_FINE_LOCATION)

        fun hasPermission(ctx: Context, scan: Boolean = true): Boolean {
            val need = if (Build.VERSION.SDK_INT >= 31) {
                if (scan) permissions().toList() else listOf(Manifest.permission.BLUETOOTH_CONNECT)
            } else if (scan) permissions().toList() else emptyList()
            return need.all { ctx.checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
        }

        /** True when the device has Bluetooth LE at all. */
        fun available(ctx: Context) =
            ctx.packageManager.hasSystemFeature(PackageManager.FEATURE_BLUETOOTH_LE) && adapter(ctx) != null

        /**
         * Govee lights advertising nearby, as (mac, name). Blocks for [ms];
         * call off the main thread. Govee names look like "ihoment_H6008_F816"
         * or "Govee_H6008_F816".
         */
        fun scan(ctx: Context, ms: Long): List<Pair<String, String>> {
            if (!available(ctx) || !hasPermission(ctx)) return emptyList()
            val scanner = adapter(ctx)?.takeIf { it.isEnabled }?.bluetoothLeScanner ?: return emptyList()
            val found = LinkedHashMap<String, String>()
            val cb = object : ScanCallback() {
                override fun onScanResult(type: Int, r: ScanResult) {
                    val name = r.scanRecord?.deviceName ?: r.device.name ?: return
                    if (name.startsWith("ihoment_", true) || name.startsWith("Govee_", true) || name.startsWith("GBK_", true)) {
                        synchronized(found) { found[r.device.address] = name }
                    }
                }
            }
            return try {
                scanner.startScan(null, ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build(), cb)
                Thread.sleep(ms)
                scanner.stopScan(cb)
                synchronized(found) { found.map { it.key to it.value } }
            } catch (_: Exception) {
                try { scanner.stopScan(cb) } catch (_: Exception) {}
                emptyList()
            }
        }
    }
}
