package com.clipcascade.android

import android.Manifest
import android.app.Activity
import android.content.BroadcastReceiver
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.BitmapFactory
import android.net.Uri
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.text.InputType
import android.util.Base64
import android.util.Log
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.PopupMenu
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import com.google.android.material.button.MaterialButton
import com.google.android.material.card.MaterialCardView
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.FileOutputStream
import java.util.UUID
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import java.text.SimpleDateFormat
import java.util.Date
import java.util.LinkedHashSet
import java.util.Locale

class MainActivity : Activity() {

    private val tag = "ClipCascade_UI"
    private val reqPostNotifications = 1002
    private val uiHandler = Handler(Looper.getMainLooper())

    private var nsdManager: NsdManager? = null
    private var discoveryListener: NsdManager.DiscoveryListener? = null
    private val discoveredServers = LinkedHashSet<String>()

    private lateinit var clipboardList: LinearLayout
    private lateinit var deferredFileListView: LinearLayout
    private lateinit var clipboardTabButton: MaterialButton
    private lateinit var filesTabButton: MaterialButton
    private lateinit var statusValueText: TextView
    private lateinit var statusDetailText: TextView
    private lateinit var statusTimeText: TextView
    private lateinit var deviceCountText: TextView
    private lateinit var lastEventText: TextView
    private lateinit var overlayPermissionRow: PermissionRow
    private lateinit var accessibilityPermissionRow: PermissionRow
    private lateinit var batteryPermissionRow: PermissionRow
    private var historyItems: List<ClipboardHistoryItem> = emptyList()
    private var deferredFileItems: List<DeferredFileItem> = emptyList()
    private var activeHistoryTab: HistoryTab = HistoryTab.CLIPBOARD
    private var historyRefreshTask: Runnable? = null
    private var deferredRefreshTask: Runnable? = null
    @Volatile
    private var deferredRefreshInFlight = false
    @Volatile
    private var deviceRefreshInFlight = false
    private val statusReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (intent?.action != ClipCascadeBackgroundService.ACTION_STATUS_CHANGED) {
                return
            }
            refreshConnectionStatus()
            refreshDeviceStatusFromPrefs()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            window.statusBarColor = color(R.color.cc_background)
            window.navigationBarColor = color(R.color.cc_background)
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val nightMode = resources.configuration.uiMode and android.content.res.Configuration.UI_MODE_NIGHT_MASK
            if (nightMode != android.content.res.Configuration.UI_MODE_NIGHT_YES) {
                window.decorView.systemUiVisibility = View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR
            }
        }

        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)

        val root = ScrollView(this).apply {
            setBackgroundColor(color(R.color.cc_background))
            isFillViewport = true
        }
        val layout = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(20), dp(28), dp(20), dp(28))
            root.addView(
                this,
                ViewGroup.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.WRAP_CONTENT
                )
            )
        }

        val titleText = TextView(this).apply {
            text = "ClipCascade"
            textSize = 30f
            setTextColor(color(R.color.cc_text_primary))
            typeface = android.graphics.Typeface.DEFAULT_BOLD
        }
        val subtitleText = TextView(this).apply {
            text = "跨设备剪贴板同步 · v${appVersionName()}"
            textSize = 15f
            setTextColor(color(R.color.cc_text_secondary))
            setPadding(0, dp(4), 0, dp(18))
        }

        val serverInput = EditText(this).apply {
            hint = "服务器地址，例如 http://192.168.1.8:8080"
            setText(normalizeServerUrl(prefs.getString("ServerURL", "") ?: ""))
            setSingleLine(true)
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            setTextColor(color(R.color.cc_text_primary))
            setHintTextColor(color(R.color.cc_text_secondary))
            layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f)
        }
        val discoverMenuBtn = MaterialButton(this).apply {
            text = "发现"
            minWidth = dp(72)
            setOnClickListener { showDiscoveredServersMenu(this, serverInput) }
            setOnLongClickListener {
                restartServerDiscovery(serverInput, this)
                Toast.makeText(this@MainActivity, "正在重新发现局域网服务", Toast.LENGTH_SHORT).show()
                true
            }
        }
        val serverRow = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(serverInput)
            addView(discoverMenuBtn)
        }

        val userInput = EditText(this).apply {
            hint = "用户名"
            setText(prefs.getString("Username", "") ?: "")
            setSingleLine(true)
            setTextColor(color(R.color.cc_text_primary))
            setHintTextColor(color(R.color.cc_text_secondary))
        }
        val passInput = EditText(this).apply {
            hint = "密码"
            setText(prefs.getString("Password", "") ?: "")
            setSingleLine(true)
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            setTextColor(color(R.color.cc_text_primary))
            setHintTextColor(color(R.color.cc_text_secondary))
        }
        val e2eeCheck = CheckBox(this).apply {
            text = "启用端到端加密"
            isChecked = prefs.getBoolean("E2EE", true)
            setTextColor(color(R.color.cc_text_primary))
            buttonTintList = android.content.res.ColorStateList.valueOf(color(R.color.cc_primary))
        }

        val btnSaveCfg = MaterialButton(this).apply {
            text = "保存配置"
            setOnClickListener {
                saveConfig(prefs, serverInput, userInput, passInput, e2eeCheck)
                Toast.makeText(this@MainActivity, "配置已保存", Toast.LENGTH_SHORT).show()
            }
        }

        val btnStartSvc = MaterialButton(this).apply {
            text = "启动同步服务"
            setOnClickListener {
                saveConfig(prefs, serverInput, userInput, passInput, e2eeCheck)
                setStatusViews("connecting", "正在启动同步服务...", System.currentTimeMillis())
                startCoreService()
            }
        }

        statusValueText = TextView(this).apply {
            textSize = 22f
            typeface = android.graphics.Typeface.DEFAULT_BOLD
            setTextColor(color(R.color.cc_warning))
        }
        statusDetailText = TextView(this).apply {
            textSize = 13f
            setTextColor(color(R.color.cc_text_primary))
            setPadding(0, dp(6), 0, 0)
        }
        statusTimeText = TextView(this).apply {
            textSize = 12f
            setTextColor(color(R.color.cc_text_secondary))
            setPadding(0, dp(4), 0, 0)
        }
        deviceCountText = TextView(this).apply {
            text = "在线设备：暂无"
            textSize = 12f
            setTextColor(color(R.color.cc_text_secondary))
            setPadding(0, dp(4), 0, 0)
        }
        lastEventText = TextView(this).apply {
            textSize = 12f
            setTextColor(color(R.color.cc_text_secondary))
            setPadding(0, dp(8), 0, 0)
        }

        overlayPermissionRow = permissionButton("悬浮窗权限", "用于兼容部分后台剪贴板读取场景") {
            requestOverlayPermission()
        }
        accessibilityPermissionRow = permissionButton("无障碍服务", "监听复制事件，国产 ROM 需允许后台运行") {
            startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS))
        }
        batteryPermissionRow = batteryPermissionButton()

        val btnRefreshHistory = MaterialButton(this).apply {
            text = "刷新"
            minWidth = dp(64)
            setOnClickListener {
                if (activeHistoryTab == HistoryTab.FILES) {
                    refreshDeferredFiles()
                } else {
                    refreshHistoryList()
                }
                refreshDeviceStatus()
            }
        }
        val btnClearHistory = MaterialButton(this).apply {
            text = "清空"
            minWidth = dp(64)
            setOnClickListener {
                if (activeHistoryTab == HistoryTab.CLIPBOARD) {
                    ClipboardHistoryStore.clear(this@MainActivity)
                    refreshHistoryList()
                } else {
                    Toast.makeText(this@MainActivity, "大文件记录由服务端管理，可在网页端删除", Toast.LENGTH_SHORT).show()
                }
            }
        }

        clipboardTabButton = MaterialButton(this).apply {
            text = "剪贴板"
            setOnClickListener { setHistoryTab(HistoryTab.CLIPBOARD) }
        }
        filesTabButton = MaterialButton(this).apply {
            text = "大文件"
            setOnClickListener { setHistoryTab(HistoryTab.FILES) }
        }
        val historyActionHeader = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(TextView(this@MainActivity).apply {
                text = "历史记录"
                textSize = 18f
                typeface = android.graphics.Typeface.DEFAULT_BOLD
                setTextColor(color(R.color.cc_text_primary))
            }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(btnRefreshHistory, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(40)))
            addView(btnClearHistory, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                marginStart = dp(8)
                height = dp(40)
            })
        }
        val historyTabRow = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            background = android.graphics.drawable.GradientDrawable().apply {
                cornerRadius = dp(8).toFloat()
                setColor(color(R.color.cc_surface_variant))
            }
            setPadding(dp(4), dp(4), dp(4), dp(4))
            addView(clipboardTabButton, LinearLayout.LayoutParams(0, dp(40), 1f))
            addView(filesTabButton, LinearLayout.LayoutParams(0, dp(40), 1f).apply {
                marginStart = dp(4)
            })
        }

        clipboardList = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
        }
        deferredFileListView = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
        }

        layout.addView(titleText)
        layout.addView(subtitleText)
        layout.addView(card("状态", "实时连接状态和最近同步事件").apply {
            addView(statusValueText)
            addView(statusDetailText)
            addView(statusTimeText)
            addView(deviceCountText)
            addView(lastEventText)
        }, blockParams())
        layout.addView(card("连接", "填写服务端和账号，启动后通知栏会显示同步状态").apply {
            addView(serverRow)
            addView(userInput, blockParams(top = 10))
            addView(passInput, blockParams(top = 10))
            addView(e2eeCheck, blockParams(top = 8))
            addView(LinearLayout(this@MainActivity).apply {
                orientation = LinearLayout.HORIZONTAL
                addView(btnSaveCfg, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
                addView(btnStartSvc, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
                    marginStart = dp(10)
                })
            }, blockParams(top = 12))
        }, blockParams())
        layout.addView(card("权限", "Android 对后台剪贴板限制很强，这三项决定稳定性").apply {
            addView(overlayPermissionRow.root)
            addView(accessibilityPermissionRow.root, blockParams(top = 10))
            addView(batteryPermissionRow.root, blockParams(top = 10))
        }, blockParams(top = 14))
        layout.addView(card("", "剪贴板和大文件分开查看", showHeader = false).apply {
            addView(historyActionHeader)
            addView(historyTabRow, blockParams(top = 10))
            addView(clipboardList, blockParams(top = 8))
            addView(deferredFileListView, blockParams(top = 8))
        }, blockParams(top = 14))

        setContentView(root)

        addDiscoveredServer(normalizeServerUrl(prefs.getString("ServerURL", "") ?: ""), serverInput, discoverMenuBtn)
        startServerDiscovery(serverInput, discoverMenuBtn)
        refreshConnectionStatus()
        refreshPermissionStatus()
        refreshHistoryList()
        updateFileList()
        setHistoryTab(HistoryTab.CLIPBOARD)

        ensureNotificationPermissionIfNeeded()
        handleShareIntent(intent)
        if ((prefs.getString("ServerURL", "") ?: "").isNotBlank()
            && (prefs.getString("Username", "") ?: "").isNotBlank()
            && (prefs.getString("Password", "") ?: "").isNotBlank()
        ) {
            startCoreService()
        }
    }

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleShareIntent(intent)
    }

    override fun onResume() {
        super.onResume()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            registerReceiver(
                statusReceiver,
                IntentFilter(ClipCascadeBackgroundService.ACTION_STATUS_CHANGED),
                Context.RECEIVER_NOT_EXPORTED
            )
        } else {
            @Suppress("DEPRECATION")
            registerReceiver(statusReceiver, IntentFilter(ClipCascadeBackgroundService.ACTION_STATUS_CHANGED))
        }
        refreshConnectionStatus()
        refreshPermissionStatus()
        refreshDeviceStatus()
        startHistoryRefreshLoop()
        startDeferredRefreshLoop()
    }

    override fun onPause() {
        stopHistoryRefreshLoop()
        stopDeferredRefreshLoop()
        try {
            unregisterReceiver(statusReceiver)
        } catch (_: Exception) {
        }
        super.onPause()
    }

    override fun onDestroy() {
        stopServerDiscovery()
        stopHistoryRefreshLoop()
        stopDeferredRefreshLoop()
        super.onDestroy()
    }

    private fun handleShareIntent(intent: Intent?) {
        if (intent == null) {
            return
        }
        val sharedText = when (intent.action) {
            Intent.ACTION_SEND -> {
                if (intent.type == "text/plain") intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString() else null
            }
            Intent.ACTION_PROCESS_TEXT -> intent.getCharSequenceExtra(Intent.EXTRA_PROCESS_TEXT)?.toString()
            else -> null
        }?.trim()

        if (!sharedText.isNullOrBlank()) {
            startCoreService(sharedText = sharedText)
            Toast.makeText(this, "已接收分享文本，正在同步", Toast.LENGTH_SHORT).show()
            intent.action = Intent.ACTION_MAIN
            intent.removeExtra(Intent.EXTRA_TEXT)
            intent.removeExtra(Intent.EXTRA_PROCESS_TEXT)
            return
        }

        if (intent.action == Intent.ACTION_SEND && intent.type?.startsWith("image/") == true) {
            val uri = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
            } else {
                @Suppress("DEPRECATION")
                intent.getParcelableExtra(Intent.EXTRA_STREAM)
            }
            val encoded = uri?.let { readSharedImageAsBase64(it) }
            if (!encoded.isNullOrBlank()) {
                startCoreService(sharedImageBase64 = encoded)
                Toast.makeText(this, "已接收分享图片，正在同步", Toast.LENGTH_SHORT).show()
                intent.action = Intent.ACTION_MAIN
                intent.removeExtra(Intent.EXTRA_STREAM)
            }
        }
    }

    private fun startCoreService(sharedText: String? = null, sharedImageBase64: String? = null) {
        val intent = Intent(this, ClipCascadeBackgroundService::class.java).apply {
            if (!sharedText.isNullOrBlank()) {
                action = ClipCascadeBackgroundService.ACTION_SEND_SHARED_TEXT
                putExtra(ClipCascadeBackgroundService.EXTRA_SHARED_TEXT, sharedText)
            } else if (!sharedImageBase64.isNullOrBlank()) {
                action = ClipCascadeBackgroundService.ACTION_SEND_SHARED_IMAGE
                putExtra(ClipCascadeBackgroundService.EXTRA_SHARED_IMAGE, sharedImageBase64)
            }
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(intent)
        } else {
            startService(intent)
        }
        Toast.makeText(this, "服务已启动，请检查通知栏", Toast.LENGTH_SHORT).show()
    }

    private fun requestDeviceStatusRefresh() {
        val intent = Intent(this, ClipCascadeBackgroundService::class.java).apply {
            action = ClipCascadeBackgroundService.ACTION_REFRESH_DEVICE_COUNTS
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(intent)
        } else {
            startService(intent)
        }
        uiHandler.postDelayed({ refreshDeviceStatusFromPrefs() }, 500L)
        uiHandler.postDelayed({ refreshDeviceStatusFromPrefs() }, 1800L)
    }

    private fun refreshConnectionStatus() {
        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        setStatusViews(
            prefs.getString("ConnectionStatus", "disconnected") ?: "disconnected",
            prefs.getString("ConnectionDetail", "尚未连接") ?: "尚未连接",
            prefs.getLong("ConnectionUpdatedAt", 0L)
        )
        val latest = ClipboardHistoryStore.list(this, limit = 1).firstOrNull()
        lastEventText.text = if (latest == null) {
            "最近事件：暂无同步记录"
        } else {
            val direction = if (latest.direction == "received") "接收" else "发送"
            "最近事件：$direction ${formatTime(latest.timestamp)}"
        }
        refreshDeviceStatusFromPrefs()
    }

    private fun setStatusViews(status: String, detail: String, timestamp: Long) {
        statusValueText.text = when (status) {
            "connected" -> "已连接"
            "connecting" -> "连接中"
            "reconnecting" -> "重连中"
            "error" -> "连接失败"
            else -> "未连接"
        }
        statusValueText.setTextColor(when (status) {
            "connected" -> color(R.color.cc_success)
            "connecting", "reconnecting" -> color(R.color.cc_warning)
            "error" -> color(android.R.color.holo_red_dark)
            else -> color(R.color.cc_text_secondary)
        })
        statusDetailText.text = detail
        statusTimeText.text = if (timestamp > 0L) {
            "更新时间：${formatTime(timestamp)}"
        } else {
            "更新时间：暂无"
        }
    }

    private fun refreshDeviceStatus() {
        if (deviceRefreshInFlight) {
            return
        }
        refreshDeviceStatusFromPrefs()
        requestDeviceStatusRefresh()
        deviceRefreshInFlight = true
        uiHandler.postDelayed({
            deviceRefreshInFlight = false
            refreshDeviceStatusFromPrefs()
        }, 2200L)
    }

    private fun refreshDeviceStatusFromPrefs() {
        if (!::deviceCountText.isInitialized) {
            return
        }
        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        val updatedAt = prefs.getLong("DeviceCountsUpdatedAt", 0L)
        if (updatedAt <= 0L) {
            return
        }
        val onlineDevices = prefs.getInt("OnlineDevices", 0)
        val syncTargets = prefs.getInt("SyncTargets", 0)
        deviceCountText.text = "在线设备：$onlineDevices 台；本次复制会同步给 $syncTargets 台"
    }

    private fun readSharedImageAsBase64(uri: Uri): String? {
        return try {
            contentResolver.openInputStream(uri)?.use { input ->
                val out = ByteArrayOutputStream()
                val buffer = ByteArray(16 * 1024)
                var total = 0
                while (true) {
                    val read = input.read(buffer)
                    if (read <= 0) {
                        break
                    }
                    total += read
                    if (total > MAX_DIRECT_IMAGE_BYTES) {
                        Toast.makeText(this, "图片超过 8 MiB，当前 Android 端暂不直传", Toast.LENGTH_LONG).show()
                        return null
                    }
                    out.write(buffer, 0, read)
                }
                val bytes = out.toByteArray()
                saveHistoryImage(bytes, "sent")?.let {
                    ClipboardHistoryStore.appendImage(this, it.absolutePath, "sent")
                    refreshHistoryList()
                }
                Base64.encodeToString(bytes, Base64.NO_WRAP)
            }
        } catch (e: Exception) {
            Log.e(tag, "读取分享图片失败", e)
            Toast.makeText(this, "读取分享图片失败", Toast.LENGTH_SHORT).show()
            null
        }
    }

    private fun saveHistoryImage(bytes: ByteArray, direction: String): File? {
        return try {
            val dir = File(cacheDir, "history-images").apply { mkdirs() }
            val file = File(dir, "${System.currentTimeMillis()}-$direction-${UUID.randomUUID()}.bin")
            FileOutputStream(file).use { it.write(bytes) }
            file
        } catch (e: Exception) {
            Log.w(tag, "保存图片历史失败", e)
            null
        }
    }

    private fun saveConfig(
        prefs: android.content.SharedPreferences,
        serverInput: EditText,
        userInput: EditText,
        passInput: EditText,
        e2eeCheck: CheckBox
    ) {
        val normalizedServerURL = normalizeServerUrl(serverInput.text?.toString() ?: "")
        serverInput.setText(normalizedServerURL)
        prefs.edit()
            .putString("ServerURL", normalizedServerURL)
            .putString("Username", userInput.text?.toString()?.trim() ?: "")
            .putString("Password", passInput.text?.toString() ?: "")
            .putBoolean("E2EE", e2eeCheck.isChecked)
            .apply()
    }

    private fun requestOverlayPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            if (!Settings.canDrawOverlays(this)) {
                Log.i(tag, "申请悬浮窗权限...")
                val intent = Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION).apply {
                    data = Uri.parse("package:$packageName")
                }
                startActivity(intent)
            } else {
                Toast.makeText(this, "已拥有悬浮窗权限", Toast.LENGTH_SHORT).show()
                refreshPermissionStatus()
            }
        }
    }

    private fun requestVendorBatterySettings() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val intent = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS).apply {
                data = Uri.parse("package:$packageName")
            }
            if (openIntent(intent)) {
                return
            }
        }
        Toast.makeText(this, "无法打开单应用省电策略，请使用系统底层入口", Toast.LENGTH_LONG).show()
    }

    private fun requestSystemBatterySettings() {
        if (openIntent(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))) {
            return
        }
        Toast.makeText(this, "无法打开系统电池优化设置", Toast.LENGTH_SHORT).show()
    }

    private fun openIntent(intent: Intent?): Boolean {
        if (intent == null) {
            return false
        }
        return try {
            startActivity(intent)
            true
        } catch (e: Exception) {
            Log.d(tag, "无法打开设置页: ${intent.action ?: intent.component}", e)
            false
        }
    }

    private fun refreshPermissionStatus() {
        if (!::overlayPermissionRow.isInitialized) {
            return
        }
        val overlayGranted = Build.VERSION.SDK_INT < Build.VERSION_CODES.M || Settings.canDrawOverlays(this)
        applyPermissionState(
            overlayPermissionRow,
            overlayGranted,
            if (overlayGranted) "已授权，可使用悬浮窗辅助读取剪贴板" else "未授权，后台读取剪贴板可能不可用"
        )

        val accessibilityEnabled = isAccessibilityServiceEnabled()
        applyPermissionState(
            accessibilityPermissionRow,
            accessibilityEnabled,
            if (accessibilityEnabled) "已开启 ClipCascade Listener" else "未开启，无法稳定监听复制事件"
        )

        val standardIgnored = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val powerManager = getSystemService(Context.POWER_SERVICE) as PowerManager
            powerManager.isIgnoringBatteryOptimizations(packageName)
        } else {
            true
        }
        batteryPermissionRow.statusText.text = if (standardIgnored) {
            "Android 标准电池优化已忽略；厂商后台策略仍需手动确认"
        } else {
            "建议进入系统页设为无限制/允许后台运行"
        }
        batteryPermissionRow.statusText.setTextColor(if (standardIgnored) color(R.color.cc_success) else color(R.color.cc_warning))
    }

    private fun applyPermissionState(row: PermissionRow, granted: Boolean, status: String) {
        row.statusText.text = status
        row.statusText.setTextColor(if (granted) color(R.color.cc_success) else color(R.color.cc_warning))
        row.actionButton.text = if (granted) "已开启" else "去设置"
        row.actionButton.isEnabled = !granted
    }

    private fun isAccessibilityServiceEnabled(): Boolean {
        val enabled = Settings.Secure.getInt(
            contentResolver,
            Settings.Secure.ACCESSIBILITY_ENABLED,
            0
        ) == 1
        if (!enabled) {
            return false
        }
        val expectedSuffix = "/${ClipCascadeAccessibilityService::class.java.name}"
        val enabledServices = Settings.Secure.getString(
            contentResolver,
            Settings.Secure.ENABLED_ACCESSIBILITY_SERVICES
        ) ?: return false
        return enabledServices.split(':').any { service ->
            service.equals("$packageName$expectedSuffix", ignoreCase = true) ||
            service.endsWith(expectedSuffix, ignoreCase = true)
        }
    }

    private fun ensureNotificationPermissionIfNeeded() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) {
            return
        }
        val granted = ContextCompat.checkSelfPermission(
            this,
            Manifest.permission.POST_NOTIFICATIONS
        ) == PackageManager.PERMISSION_GRANTED
        if (!granted) {
            ActivityCompat.requestPermissions(
                this,
                arrayOf(Manifest.permission.POST_NOTIFICATIONS),
                reqPostNotifications
            )
        }
    }

    private fun startServerDiscovery(serverInput: EditText, discoverMenuBtn: Button) {
        stopServerDiscovery()
        nsdManager = getSystemService(Context.NSD_SERVICE) as? NsdManager ?: return

        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) {
                Log.i(tag, "mDNS discovery started: $serviceType")
            }

            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                Log.w(tag, "mDNS discovery failed to start: $errorCode")
            }

            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {
                Log.w(tag, "mDNS discovery failed to stop: $errorCode")
            }

            override fun onDiscoveryStopped(serviceType: String) {
                Log.i(tag, "mDNS discovery stopped: $serviceType")
            }

            override fun onServiceLost(serviceInfo: NsdServiceInfo) {
                Log.d(tag, "mDNS service lost: ${serviceInfo.serviceName}")
            }

            override fun onServiceFound(serviceInfo: NsdServiceInfo) {
                val serviceType = serviceInfo.serviceType ?: return
                if (!serviceType.equals("_clipcascade._tcp.", ignoreCase = true)) {
                    return
                }
                nsdManager?.resolveService(serviceInfo, object : NsdManager.ResolveListener {
                    override fun onResolveFailed(info: NsdServiceInfo, errorCode: Int) {
                        Log.d(tag, "mDNS resolve failed: $errorCode")
                    }

                    override fun onServiceResolved(info: NsdServiceInfo) {
                        val host = info.host?.hostAddress ?: info.host?.hostName ?: return
                        val url = buildHttpUrl(host, info.port)
                        addDiscoveredServer(url, serverInput, discoverMenuBtn)
                    }
                })
            }
        }

        discoveryListener = listener
        try {
            nsdManager?.discoverServices("_clipcascade._tcp.", NsdManager.PROTOCOL_DNS_SD, listener)
        } catch (e: Exception) {
            Log.w(tag, "mDNS discover start failed", e)
        }
    }

    private fun restartServerDiscovery(serverInput: EditText, discoverMenuBtn: Button) {
        synchronized(discoveredServers) {
            discoveredServers.clear()
        }
        updateDiscoverButton(discoverMenuBtn)
        addDiscoveredServer(serverInput.text?.toString()?.trim().orEmpty(), serverInput, discoverMenuBtn)
        startServerDiscovery(serverInput, discoverMenuBtn)
    }

    private fun stopServerDiscovery() {
        val manager = nsdManager ?: return
        val listener = discoveryListener ?: return
        try {
            manager.stopServiceDiscovery(listener)
        } catch (_: Exception) {
        } finally {
            discoveryListener = null
        }
    }

    private fun addDiscoveredServer(url: String, serverInput: EditText, discoverMenuBtn: Button) {
        val normalized = url.trim()
        if (normalized.isBlank() || !isUsableDiscoveryUrl(normalized)) {
            return
        }
        val added = synchronized(discoveredServers) {
            discoveredServers.add(normalized)
        }
        if (!added) {
            return
        }
        runOnUiThread {
            updateDiscoverButton(discoverMenuBtn)
            val current = serverInput.text?.toString()?.trim().orEmpty()
            if (current.isBlank() || current == "http://localhost:8080" || !isUsableDiscoveryUrl(current)) {
                serverInput.setText(normalized)
            }
        }
    }

    private fun updateDiscoverButton(btn: Button) {
        val count = synchronized(discoveredServers) { discoveredServers.size }
        btn.text = if (count > 0) "发现($count)" else "发现"
        btn.isEnabled = count > 0
    }

    private fun showDiscoveredServersMenu(anchor: Button, serverInput: EditText) {
        val options = synchronized(discoveredServers) { discoveredServers.toList().sortedWith(discoveryUrlComparator) }
        if (options.isEmpty()) {
            restartServerDiscovery(serverInput, anchor)
            Toast.makeText(this, "正在发现局域网服务，请稍后再点", Toast.LENGTH_SHORT).show()
            return
        }
        val menu = PopupMenu(this, anchor)
        options.forEachIndexed { index, url ->
            menu.menu.add(0, index, index, url)
        }
        menu.setOnMenuItemClickListener { item ->
            val selected = options.getOrNull(item.itemId) ?: return@setOnMenuItemClickListener false
            serverInput.setText(selected)
            true
        }
        menu.show()
    }

    private fun buildHttpUrl(host: String, port: Int): String {
        val cleanHost = host.trim().trimEnd('.')
        return if (cleanHost.contains(":") && !cleanHost.startsWith("[")) {
            "http://[$cleanHost]:$port"
        } else {
            "http://$cleanHost:$port"
        }
    }

    private fun isUsableDiscoveryUrl(url: String): Boolean {
        val host = try {
            Uri.parse(url).host ?: return true
        } catch (_: Exception) {
            return true
        }
        if (host == "localhost" || host == "127.0.0.1") {
            return false
        }
        val parts = host.split(".").mapNotNull { it.toIntOrNull() }
        if (parts.size != 4) {
            return true
        }
        if (parts[0] == 192 && parts[1] == 168 && (parts[2] == 52 || parts[2] == 56 || parts[2] == 64)) {
            return false
        }
        if (parts[0] == 172 && parts[1] in 16..31) {
            return false
        }
        if (parts[0] == 100 && parts[1] in 64..127) {
            return false
        }
        return true
    }

    private fun refreshHistoryList() {
        historyItems = ClipboardHistoryStore.list(this, limit = 20)
        updateClipboardList()
    }

    private fun updateClipboardList() {
        if (::clipboardList.isInitialized) {
            clipboardList.removeAllViews()
            if (historyItems.isEmpty()) {
                clipboardList.addView(emptyText("暂无剪贴板记录"))
            } else {
                clipboardList.addView(TextView(this).apply {
                    text = "共 ${historyItems.size} 条"
                    textSize = 12f
                    setTextColor(color(R.color.cc_text_secondary))
                    setPadding(dp(4), dp(2), dp(4), dp(8))
                })
                historyItems.forEachIndexed { index, item ->
                    clipboardList.addView(clipboardHistoryRow(item), blockParams(top = if (index == 0) 0 else 6))
                }
            }
        }
        if (::lastEventText.isInitialized) {
            val latest = historyItems.firstOrNull()
            lastEventText.text = if (latest == null) {
                "最近事件：暂无同步记录"
            } else {
                val direction = if (latest.direction == "received") "接收" else "发送"
                val type = if (latest.type == "image") "图片" else "文本"
                "最近事件：$direction$type ${formatTime(latest.timestamp)}"
            }
        }
    }

    private fun updateFileList() {
        if (!::deferredFileListView.isInitialized) {
            return
        }
        deferredFileListView.removeAllViews()
        if (deferredFileItems.isEmpty()) {
            deferredFileListView.addView(emptyText("暂无大文件记录"))
        } else {
            deferredFileListView.addView(TextView(this).apply {
                text = "共 ${deferredFileItems.size} 条"
                textSize = 12f
                setTextColor(color(R.color.cc_text_secondary))
                setPadding(dp(4), dp(2), dp(4), dp(8))
            })
            deferredFileItems.forEachIndexed { index, item ->
                deferredFileListView.addView(fileHistoryRow(item), blockParams(top = if (index == 0) 0 else 6))
            }
        }
    }

    private fun refreshDeferredFiles() {
        if (deferredRefreshInFlight) {
            return
        }
        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        val serverURL = normalizeServerUrl(prefs.getString("ServerURL", "") ?: "")
        val username = prefs.getString("Username", "")?.trim().orEmpty()
        val password = prefs.getString("Password", "").orEmpty()
        if (serverURL.isBlank() || username.isBlank() || password.isBlank()) {
            return
        }

        deferredRefreshInFlight = true
        Thread {
            try {
                val cookie = loginForApi(serverURL, username, password)
                val items = fetchDeferredFiles(serverURL, cookie)
                runOnUiThread {
                    deferredFileItems = items
                    updateFileList()
                }
            } catch (e: Exception) {
                Log.d(tag, "刷新服务端文件列表失败", e)
            } finally {
                deferredRefreshInFlight = false
            }
        }.start()
    }

    private fun loginForApi(serverURL: String, username: String, password: String): String {
        val baseURL = normalizeServerUrl(serverURL)
        val body = "username=${urlEncode(username)}&password=${urlEncode(password)}"
        val conn = (URL("${baseURL.trimEnd('/')}/login").openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            instanceFollowRedirects = false
            connectTimeout = 6000
            readTimeout = 6000
            doOutput = true
            setRequestProperty("Content-Type", "application/x-www-form-urlencoded")
        }
        conn.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
        val status = conn.responseCode
        if (status !in 300..399 && status != HttpURLConnection.HTTP_OK) {
            throw IllegalStateException("login failed: $status")
        }
        return conn.headerFields["Set-Cookie"]
            ?.joinToString("; ") { it.substringBefore(";") }
            .orEmpty()
            .also {
                if (it.isBlank()) {
                    throw IllegalStateException("login did not return session cookie")
                }
            }
    }

    private fun fetchDeferredFiles(serverURL: String, cookie: String): List<DeferredFileItem> {
        val baseURL = normalizeServerUrl(serverURL)
        val conn = (URL("${baseURL.trimEnd('/')}/api/files/deferred").openConnection() as HttpURLConnection).apply {
            requestMethod = "GET"
            connectTimeout = 6000
            readTimeout = 6000
            setRequestProperty("Cookie", cookie)
        }
        if (conn.responseCode != HttpURLConnection.HTTP_OK) {
            throw IllegalStateException("list deferred failed: ${conn.responseCode}")
        }
        val raw = conn.inputStream.bufferedReader().use { it.readText() }
        val arr = JSONArray(raw)
        val out = mutableListOf<DeferredFileItem>()
        for (i in 0 until arr.length()) {
            val obj = arr.optJSONObject(i) ?: continue
            out.add(parseDeferredFile(obj))
        }
        return out
    }

    private fun fetchDeviceCounts(serverURL: String, cookie: String): DeviceCounts {
        val baseURL = normalizeServerUrl(serverURL)
        val conn = (URL("${baseURL.trimEnd('/')}/api/devices/online").openConnection() as HttpURLConnection).apply {
            requestMethod = "GET"
            connectTimeout = 6000
            readTimeout = 6000
            setRequestProperty("Cookie", cookie)
        }
        if (conn.responseCode != HttpURLConnection.HTTP_OK) {
            throw IllegalStateException("device status failed: ${conn.responseCode}")
        }
        val raw = conn.inputStream.bufferedReader().use { it.readText() }
        val obj = JSONObject(raw)
        return DeviceCounts(
            onlineDevices = obj.optInt("online_devices", 0),
            syncTargets = obj.optInt("sync_targets", 0),
            username = obj.optString("username").takeIf { it.isNotBlank() }
        )
    }

    private fun parseDeferredFile(obj: JSONObject): DeferredFileItem {
        return DeferredFileItem(
            deferredID = obj.optString("deferred_id"),
            fileName = obj.optString("file_name"),
            fileSizeHuman = obj.optString("file_size_human"),
            mimeType = obj.optString("mime_type", "application/octet-stream"),
            status = obj.optString("status"),
            statusDetail = obj.optString("status_detail"),
            downloadURL = obj.optString("download_url"),
            uploadPercent = obj.optInt("upload_percent", 0),
            relayID = obj.optString("relay_id")
        )
    }

    private fun handleDeferredFileClick(item: DeferredFileItem) {
        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        val serverURL = normalizeServerUrl(prefs.getString("ServerURL", "") ?: "")
        val username = prefs.getString("Username", "")?.trim().orEmpty()
        val password = prefs.getString("Password", "").orEmpty()
        if (serverURL.isBlank() || username.isBlank() || password.isBlank()) {
            Toast.makeText(this, "请先保存服务器账号", Toast.LENGTH_SHORT).show()
            return
        }
        Toast.makeText(this, "正在处理文件：${item.fileName}", Toast.LENGTH_SHORT).show()
        Thread {
            try {
                val cookie = loginForApi(serverURL, username, password)
                if (item.status == "ready" && item.downloadURL.isNotBlank()) {
                    val file = downloadDeferredFile(serverURL, cookie, item)
                    markFileDownloaded(item, file)
                    runOnUiThread {
                        updateFileList()
                        Toast.makeText(this, "已下载到：${file.absolutePath}", Toast.LENGTH_LONG).show()
                        openDownloadedFile(file, item.mimeType)
                    }
                } else {
                    requestDeferredFile(serverURL, cookie, item.deferredID)
                    runOnUiThread {
                        Toast.makeText(this, "已请求源设备上传，稍后刷新查看进度", Toast.LENGTH_LONG).show()
                        refreshDeferredFiles()
                    }
                }
            } catch (e: Exception) {
                Log.e(tag, "处理服务端文件失败", e)
                runOnUiThread {
                    Toast.makeText(this, "文件处理失败：${e.message}", Toast.LENGTH_LONG).show()
                }
            }
        }.start()
    }

    private fun openHistoryImage(item: ClipboardHistoryItem) {
        val path = item.imagePath
        if (path.isNullOrBlank()) {
            Toast.makeText(this, "图片文件不存在", Toast.LENGTH_SHORT).show()
            return
        }
        val file = File(path)
        if (!file.exists()) {
            Toast.makeText(this, "图片文件已被系统清理", Toast.LENGTH_SHORT).show()
            return
        }
        openDownloadedFile(file, "image/*")
    }

    private fun requestDeferredFile(serverURL: String, cookie: String, deferredID: String) {
        val baseURL = normalizeServerUrl(serverURL)
        val conn = (URL("${baseURL.trimEnd('/')}/api/files/deferred/$deferredID/request").openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            connectTimeout = 6000
            readTimeout = 6000
            doOutput = true
            setRequestProperty("Cookie", cookie)
            setRequestProperty("Content-Length", "0")
        }
        conn.outputStream.use { }
        if (conn.responseCode != HttpURLConnection.HTTP_OK) {
            throw IllegalStateException("request failed: ${conn.responseCode}")
        }
    }

    private fun downloadDeferredFile(serverURL: String, cookie: String, item: DeferredFileItem): File {
        val baseURL = normalizeServerUrl(serverURL)
        val url = if (item.downloadURL.startsWith("http")) {
            item.downloadURL
        } else {
            "${baseURL.trimEnd('/')}/${item.downloadURL.trimStart('/')}"
        }
        val conn = (URL(url).openConnection() as HttpURLConnection).apply {
            requestMethod = "GET"
            connectTimeout = 6000
            readTimeout = 60000
            setRequestProperty("Cookie", cookie)
        }
        if (conn.responseCode != HttpURLConnection.HTTP_OK) {
            throw IllegalStateException("download failed: ${conn.responseCode}")
        }
        val dir = File(cacheDir, "downloads").apply { mkdirs() }
        val file = File(dir, safeFileName(item.fileName))
        conn.inputStream.use { input ->
            file.outputStream().use { output ->
                input.copyTo(output)
            }
        }
        return file
    }

    private fun openDownloadedFile(file: File, mimeType: String) {
        val uri = FileProvider.getUriForFile(this, "$packageName.fileprovider", file)
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, mimeType.ifBlank { "application/octet-stream" })
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        try {
            startActivity(Intent.createChooser(intent, "打开文件"))
        } catch (_: Exception) {
            Toast.makeText(this, "文件已下载，但没有可打开的应用：${file.name}", Toast.LENGTH_LONG).show()
        }
    }

    private fun deferredStatusLabel(item: DeferredFileItem): String {
        if (downloadedFileFor(item)?.exists() == true) {
            return "已下载"
        }
        return when (item.status) {
            "pending" -> "待请求"
            "requested" -> "已请求"
            "uploading" -> "上传中"
            "ready" -> "可下载"
            "failed" -> "失败"
            else -> item.status.ifBlank { "文件" }
        }
    }

    private fun downloadedFileFor(item: DeferredFileItem): File? {
        val key = item.relayID.ifBlank { item.deferredID }
        if (key.isBlank()) {
            return null
        }
        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        val path = prefs.getString("DownloadedFile_$key", "")?.trim().orEmpty()
        if (path.isBlank()) {
            return null
        }
        return File(path)
    }

    private fun markFileDownloaded(item: DeferredFileItem, file: File) {
        val key = item.relayID.ifBlank { item.deferredID }
        if (key.isBlank()) {
            return
        }
        getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
            .edit()
            .putString("DownloadedFile_$key", file.absolutePath)
            .apply()
    }

    private fun clipboardHistoryRow(item: ClipboardHistoryItem): View {
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(10), dp(8), dp(10), dp(8))
            background = android.graphics.drawable.GradientDrawable().apply {
                cornerRadius = dp(8).toFloat()
                setColor(color(R.color.cc_surface_variant))
            }
            setOnClickListener {
                if (item.type == "image") {
                    openHistoryImage(item)
                } else {
                    val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                    clipboard.setPrimaryClip(ClipData.newPlainText("ClipCascade", item.text))
                    Toast.makeText(this@MainActivity, "已回填到系统剪贴板", Toast.LENGTH_SHORT).show()
                }
            }
        }
        if (item.type == "image") {
            row.addView(ImageView(this).apply {
                scaleType = ImageView.ScaleType.CENTER_CROP
                val bitmap = item.imagePath?.let { BitmapFactory.decodeFile(it, BitmapFactory.Options().apply { inSampleSize = 4 }) }
                if (bitmap != null) {
                    setImageBitmap(bitmap)
                } else {
                    setImageResource(android.R.drawable.ic_menu_gallery)
                }
            }, LinearLayout.LayoutParams(dp(52), dp(52)))
        }
        row.addView(LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            val direction = if (item.direction == "received") "接收" else "发送"
            val type = if (item.type == "image") "图片" else "文本"
            addView(TextView(this@MainActivity).apply {
                text = "$direction$type · ${formatTime(item.timestamp)}"
                textSize = 12f
                setTextColor(color(R.color.cc_text_secondary))
            })
            addView(TextView(this@MainActivity).apply {
                text = if (item.type == "image") "点击打开图片" else item.text
                textSize = 15f
                maxLines = 2
                setTextColor(color(R.color.cc_text_primary))
            })
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
            if (item.type == "image") marginStart = dp(12)
        })
        return row
    }

    private fun fileHistoryRow(item: DeferredFileItem): View {
        val status = deferredStatusLabel(item)
        val progress = if (item.uploadPercent > 0 && item.uploadPercent < 100) " ${item.uploadPercent}%" else ""
        return LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(10), dp(8), dp(10), dp(8))
            background = android.graphics.drawable.GradientDrawable().apply {
                cornerRadius = dp(8).toFloat()
                setColor(color(R.color.cc_surface_variant))
            }
            setOnClickListener { handleDeferredFileClick(item) }
            addView(TextView(this@MainActivity).apply {
                text = "$status$progress · ${item.fileSizeHuman}"
                textSize = 12f
                setTextColor(color(R.color.cc_text_secondary))
            })
            addView(TextView(this@MainActivity).apply {
                text = item.fileName
                textSize = 15f
                maxLines = 2
                setTextColor(color(R.color.cc_text_primary))
            })
        }
    }

    private fun emptyText(textValue: String): TextView {
        return TextView(this).apply {
            text = textValue
            textSize = 15f
            setTextColor(color(R.color.cc_text_secondary))
            setPadding(dp(12), dp(18), dp(12), dp(18))
        }
    }

    private fun setHistoryTab(tab: HistoryTab) {
        activeHistoryTab = tab
        clipboardList.visibility = if (tab == HistoryTab.CLIPBOARD) View.VISIBLE else View.GONE
        deferredFileListView.visibility = if (tab == HistoryTab.FILES) View.VISIBLE else View.GONE
        clipboardTabButton.alpha = if (tab == HistoryTab.CLIPBOARD) 1f else 0.62f
        filesTabButton.alpha = if (tab == HistoryTab.FILES) 1f else 0.62f
        styleTabButton(clipboardTabButton, tab == HistoryTab.CLIPBOARD)
        styleTabButton(filesTabButton, tab == HistoryTab.FILES)
        if (tab == HistoryTab.FILES) {
            refreshDeferredFiles()
        }
    }

    private fun styleTabButton(button: MaterialButton, selected: Boolean) {
        button.setTextColor(color(if (selected) R.color.cc_primary else R.color.cc_text_secondary))
        button.setBackgroundColor(color(if (selected) R.color.cc_surface else R.color.cc_surface_variant))
        button.strokeWidth = 0
    }

    private fun startHistoryRefreshLoop() {
        stopHistoryRefreshLoop()
        historyRefreshTask = object : Runnable {
            override fun run() {
                refreshHistoryList()
                uiHandler.postDelayed(this, 1500L)
            }
        }
        uiHandler.post(historyRefreshTask!!)
    }

    private fun stopHistoryRefreshLoop() {
        historyRefreshTask?.let { uiHandler.removeCallbacks(it) }
        historyRefreshTask = null
    }

    private fun startDeferredRefreshLoop() {
        stopDeferredRefreshLoop()
        deferredRefreshTask = object : Runnable {
            override fun run() {
                refreshDeferredFiles()
                uiHandler.postDelayed(this, 8000L)
            }
        }
        uiHandler.post(deferredRefreshTask!!)
    }

    private fun stopDeferredRefreshLoop() {
        deferredRefreshTask?.let { uiHandler.removeCallbacks(it) }
        deferredRefreshTask = null
    }

    private fun card(title: String, subtitle: String, showHeader: Boolean = true): LinearLayout {
        val card = MaterialCardView(this).apply {
            radius = dp(8).toFloat()
            cardElevation = dp(1).toFloat()
            setCardBackgroundColor(color(R.color.cc_surface))
            strokeColor = color(R.color.cc_outline)
            strokeWidth = 1
        }
        val inner = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(14), dp(16), dp(16))
        }
        if (showHeader) {
            inner.addView(TextView(this).apply {
                text = title
                textSize = 18f
                typeface = android.graphics.Typeface.DEFAULT_BOLD
                setTextColor(color(R.color.cc_text_primary))
            })
            inner.addView(TextView(this).apply {
                text = subtitle
                textSize = 13f
                setTextColor(color(R.color.cc_text_secondary))
                setPadding(0, dp(4), 0, dp(12))
            })
        }
        card.addView(inner)
        return object : LinearLayout(this) {
            init {
                orientation = VERTICAL
                super.addView(card, ViewGroup.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.WRAP_CONTENT
                ))
            }

            override fun addView(child: View?) {
                inner.addView(child)
            }

            override fun addView(child: View?, params: ViewGroup.LayoutParams?) {
                inner.addView(child, params)
            }
        }
    }

    private fun permissionButton(title: String, subtitle: String, action: () -> Unit): PermissionRow {
        lateinit var statusText: TextView
        lateinit var actionButton: MaterialButton
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(12), dp(10), dp(12), dp(10))
            background = android.graphics.drawable.GradientDrawable().apply {
                cornerRadius = dp(8).toFloat()
                setColor(color(R.color.cc_surface_variant))
            }
            addView(LinearLayout(this@MainActivity).apply {
                orientation = LinearLayout.VERTICAL
                addView(TextView(this@MainActivity).apply {
                    text = title
                    textSize = 15f
                    typeface = android.graphics.Typeface.DEFAULT_BOLD
                    setTextColor(color(R.color.cc_text_primary))
                })
                addView(TextView(this@MainActivity).apply {
                    text = subtitle
                    textSize = 12f
                    setTextColor(color(R.color.cc_text_secondary))
                })
                statusText = TextView(this@MainActivity).apply {
                    text = "检查中"
                    textSize = 12f
                    setTextColor(color(R.color.cc_warning))
                    setPadding(0, dp(4), 0, 0)
                }
                addView(statusText)
            }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            actionButton = MaterialButton(this@MainActivity).apply {
                text = "设置"
                minWidth = dp(64)
                setOnClickListener { action() }
            }
            addView(actionButton)
        }
        return PermissionRow(root, statusText, actionButton)
    }

    private fun batteryPermissionButton(): PermissionRow {
        lateinit var statusText: TextView
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(12), dp(10), dp(12), dp(10))
            background = android.graphics.drawable.GradientDrawable().apply {
                cornerRadius = dp(8).toFloat()
                setColor(color(R.color.cc_surface_variant))
            }
            addView(LinearLayout(this@MainActivity).apply {
                orientation = LinearLayout.VERTICAL
                addView(TextView(this@MainActivity).apply {
                    text = "后台保活设置"
                    textSize = 15f
                    typeface = android.graphics.Typeface.DEFAULT_BOLD
                    setTextColor(color(R.color.cc_text_primary))
                })
                addView(TextView(this@MainActivity).apply {
                    text = "单应用省电策略和 Android 底层电池优化分开设置"
                    textSize = 12f
                    setTextColor(color(R.color.cc_text_secondary))
                })
                statusText = TextView(this@MainActivity).apply {
                    text = "检查中"
                    textSize = 12f
                    setTextColor(color(R.color.cc_warning))
                    setPadding(0, dp(4), 0, 0)
                }
                addView(statusText)
            }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(LinearLayout(this@MainActivity).apply {
                orientation = LinearLayout.VERTICAL
                addView(MaterialButton(this@MainActivity).apply {
                    text = "省电策略"
                    minWidth = dp(92)
                    setOnClickListener { requestVendorBatterySettings() }
                }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(40)))
                addView(MaterialButton(this@MainActivity).apply {
                    text = "系统底层"
                    minWidth = dp(92)
                    setOnClickListener { requestSystemBatterySettings() }
                }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(40)).apply {
                    topMargin = dp(6)
                })
            })
        }
        return PermissionRow(root, statusText, MaterialButton(this))
    }

    private fun blockParams(top: Int = 0): LinearLayout.LayoutParams {
        return LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT
        ).apply {
            topMargin = dp(top)
        }
    }

    private fun dp(value: Int): Int {
        return (value * resources.displayMetrics.density).toInt()
    }

    private fun color(resId: Int): Int {
        return ContextCompat.getColor(this, resId)
    }

    private fun formatTime(timestamp: Long): String {
        return SimpleDateFormat("HH:mm:ss", Locale.getDefault()).format(Date(timestamp))
    }

    private fun urlEncode(value: String): String {
        return URLEncoder.encode(value, "UTF-8")
    }

    private fun normalizeServerUrl(raw: String): String {
        val trimmed = raw.trim().trimEnd('/')
        if (trimmed.isBlank()) {
            return ""
        }
        return if (trimmed.contains("://")) trimmed else "http://$trimmed"
    }

    private fun safeFileName(name: String): String {
        val cleaned = name.trim()
            .replace("/", "_")
            .replace("\\", "_")
            .replace("\u0000", "")
        return cleaned.ifBlank { "clipcascade-file" }
    }

    private fun appVersionName(): String {
        return try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                packageManager.getPackageInfo(packageName, PackageManager.PackageInfoFlags.of(0)).versionName
            } else {
                @Suppress("DEPRECATION")
                packageManager.getPackageInfo(packageName, 0).versionName
            } ?: "unknown"
        } catch (_: Exception) {
            "unknown"
        }
    }

    companion object {
        private const val MAX_DIRECT_IMAGE_BYTES = 8 * 1024 * 1024
        private val discoveryUrlComparator = Comparator<String> { left, right ->
            discoveryRank(left).compareTo(discoveryRank(right))
        }

        private fun discoveryRank(url: String): Int {
            val host = Uri.parse(url).host ?: return 50
            val parts = host.split(".").mapNotNull { it.toIntOrNull() }
            if (parts.size != 4) {
                return 50
            }
            if (parts[0] == 192 && parts[1] == 168 && parts[2] !in setOf(52, 56, 64)) {
                return 0
            }
            if (parts[0] == 10) {
                return 1
            }
            return 10
        }
    }

    private data class PermissionRow(
        val root: LinearLayout,
        val statusText: TextView,
        val actionButton: MaterialButton
    )

    private enum class HistoryTab {
        CLIPBOARD,
        FILES
    }

    private data class DeferredFileItem(
        val deferredID: String,
        val fileName: String,
        val fileSizeHuman: String,
        val mimeType: String,
        val status: String,
        val statusDetail: String,
        val downloadURL: String,
        val uploadPercent: Int,
        val relayID: String
    )

    private data class DeviceCounts(
        val onlineDevices: Int,
        val syncTargets: Int,
        val username: String?
    )
}
