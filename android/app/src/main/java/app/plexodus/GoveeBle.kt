package app.plexodus

import android.Manifest
import android.annotation.SuppressLint
import android.bluetooth.BluetoothAdapter
import android.bluetooth.BluetoothDevice
import android.bluetooth.BluetoothGatt
import android.bluetooth.BluetoothGattCallback
import android.bluetooth.BluetoothGattCharacteristic
import android.bluetooth.BluetoothGattDescriptor
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothProfile
import android.bluetooth.le.ScanCallback
import android.bluetooth.le.ScanResult
import android.bluetooth.le.ScanSettings
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.os.SystemClock
import android.util.Log
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
 * drops stale frames instead of building a backlog. On connect it reads the
 * power state, switches the light on, and on close switches it back off if
 * it was off.
 */
@SuppressLint("MissingPermission")
class GoveeBle(private val ctx: Context, private val mac: String) {
    // Own thread: GATT callbacks keep arriving after the player's thread has gone.
    private val thread = HandlerThread("govee-ble").apply { start() }
    private val bg = Handler(thread.looper)
    private var gatt: BluetoothGatt? = null
    private var ch: BluetoothGattCharacteristic? = null
    private var busy = false
    /** Commands that must all go out, in order (power, restore), before any colour. */
    private val urgent = ArrayDeque<ByteArray>()
    private var pending: ByteArray? = null
    private var pending2: ByteArray? = null
    private var lastWrite = 0L
    private var closing = false
    private var closed = false
    private var wantOn = true
    /** Whether the light was on when we first heard from it; null until known. */
    @Volatile var wasOn: Boolean? = null
        private set

    fun connect(): Boolean {
        if (!hasPermission(ctx, scan = false)) return false
        val adapter = adapter(ctx) ?: return false
        if (!adapter.isEnabled) return false
        val dev = try { adapter.getRemoteDevice(mac) } catch (_: IllegalArgumentException) { return false }
        Log.d(TAG, "connecting to $mac")
        gatt = dev.connectGatt(ctx, false, callback, BluetoothDevice.TRANSPORT_LE)
        return gatt != null
    }

    /** Sets the colour; r/g/b already scaled for brightness. */
    fun colour(r: Int, g: Int, b: Int) {
        val rr = r.coerceIn(0, 255); val gg = g.coerceIn(0, 255); val bb = b.coerceIn(0, 255)
        val a = packet(0x05, 0x02, rr, gg, bb); val c = packet(0x05, 0x0D, rr, gg, bb)
        bg.post {
            if (closing) return@post
            pending = a; pending2 = c
            pump()
        }
    }

    /** Asks for the power state (the answer comes back as a notification) and keeps the link alive. */
    fun keepAlive() = bg.post { if (!closing) { urgent.add(packet2(0xAA, 0x01)); pump() } }

    /**
     * Hands the light back: warm white, and off again if it was off before
     * we started. Then disconnects.
     */
    fun close(r: Int, g: Int, b: Int) {
        bg.post {
            closing = true
            pending = null; pending2 = null
            urgent.add(packet(0x05, 0x02, r, g, b)); urgent.add(packet(0x05, 0x0D, r, g, b))
            if (wasOn == false) urgent.add(packet(0x01, 0))
            pump()
            bg.postDelayed({ shutdown() }, 1500)
        }
    }

    private fun shutdown() {
        if (closed) return
        closed = true
        try { gatt?.disconnect(); gatt?.close() } catch (_: Exception) {}
        gatt = null; ch = null
        thread.quitSafely()
    }

    private fun pump() {
        if (busy && SystemClock.uptimeMillis() - lastWrite < 1000) return
        val c = ch ?: return
        val g = gatt ?: return
        val p = urgent.removeFirstOrNull() ?: pending?.also { pending = null } ?: pending2?.also { pending2 = null }
        if (p == null) {
            busy = false
            if (closing && urgent.isEmpty()) bg.postDelayed({ shutdown() }, 200)
            return
        }
        busy = true
        lastWrite = SystemClock.uptimeMillis()
        val ok = try {
            if (Build.VERSION.SDK_INT >= 33) {
                g.writeCharacteristic(c, p, BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT) == BluetoothGatt.GATT_SUCCESS
            } else {
                @Suppress("DEPRECATION")
                c.writeType = BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT
                @Suppress("DEPRECATION")
                c.value = p
                @Suppress("DEPRECATION")
                g.writeCharacteristic(c)
            }
        } catch (_: Exception) { false }
        if (!ok) { busy = false; Log.w(TAG, "write refused: ${hex(p)}") }
    }

    private val callback = object : BluetoothGattCallback() {
        override fun onConnectionStateChange(g: BluetoothGatt, status: Int, state: Int) {
            bg.post {
                Log.d(TAG, "connection state $state status $status")
                if (closed) return@post
                if (state == BluetoothProfile.STATE_CONNECTED) g.discoverServices()
                else if (state == BluetoothProfile.STATE_DISCONNECTED) {
                    ch = null; busy = false
                    // Dropped (out of range, light power-cycled): keep trying while in use.
                    if (!closing) bg.postDelayed({ if (!closing && !closed) try { g.connect() } catch (_: Exception) {} }, 2000)
                }
            }
        }

        override fun onServicesDiscovered(g: BluetoothGatt, status: Int) {
            bg.post {
                val svc = g.getService(SERVICE)
                ch = svc?.getCharacteristic(CONTROL)
                Log.d(TAG, "services discovered, control ${if (ch != null) "found" else "MISSING"}")
                // Replies (power state) come back as notifications.
                svc?.getCharacteristic(NOTIFY)?.let { n ->
                    g.setCharacteristicNotification(n, true)
                    n.getDescriptor(CCCD)?.let { d ->
                        @Suppress("DEPRECATION")
                        d.value = BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE
                        @Suppress("DEPRECATION")
                        if (g.writeDescriptor(d)) { busy = true; lastWrite = SystemClock.uptimeMillis() }
                    }
                }
                // Ask whether it's on (so closing can put it back), then switch it on.
                urgent.addFirst(packet(0x01, 1))
                urgent.addFirst(packet2(0xAA, 0x01))
                pump()
            }
        }

        override fun onDescriptorWrite(g: BluetoothGatt, d: BluetoothGattDescriptor, status: Int) {
            bg.post { busy = false; pump() }
        }

        override fun onCharacteristicWrite(g: BluetoothGatt, c: BluetoothGattCharacteristic, status: Int) {
            bg.post {
                if (status != BluetoothGatt.GATT_SUCCESS) Log.w(TAG, "write status $status")
                busy = false; pump()
            }
        }

        @Deprecated("Deprecated in API 33")
        override fun onCharacteristicChanged(g: BluetoothGatt, c: BluetoothGattCharacteristic) {
            @Suppress("DEPRECATION")
            val v = c.value ?: return
            if (v.size >= 3 && (v[0].toInt() and 0xFF) == 0xAA && v[1].toInt() == 0x01 && wasOn == null) {
                wasOn = v[2].toInt() == 1
                Log.d(TAG, "light was ${if (wasOn == true) "on" else "off"}")
            }
        }
    }

    companion object {
        private val SERVICE = UUID.fromString("00010203-0405-0607-0809-0a0b0c0d1910")
        private val CONTROL = UUID.fromString("00010203-0405-0607-0809-0a0b0c0d2b11")
        private val NOTIFY = UUID.fromString("00010203-0405-0607-0809-0a0b0c0d2b10")
        private val CCCD = UUID.fromString("00002902-0000-1000-8000-00805f9b34fb")
        private const val TAG = "RoomLight"

        private fun hex(p: ByteArray) = p.joinToString("") { "%02x".format(it) }

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
