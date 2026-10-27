package com.clipcascade.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.PixelFormat
import android.net.ConnectivityManager
import android.net.Network
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import android.util.Base64
import android.util.Log
import android.view.WindowManager
import android.webkit.MimeTypeMap
import android.widget.LinearLayout
import androidx.core.content.ContextCompat
import bridge.bridge.Bridge
import bridge.bridge.Engine
import bridge.bridge.MessageCallback
import java.io.File
import java.io.FileOutputStream
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.RejectedExecutionException

class ClipCascadeBackgroundService : Service(), MessageCallback {

    private val TAG = "ClipCascade_BgSvc"

    companion object {
        const val ACTION_STATUS_CHANGED = "com.clipcascade.android.action.STATUS_CHANGED"
        const val ACTION_SEND_SHARED_TEXT = "com.clipcascade.android.action.SEND_SHARED_TEXT"
        const val ACTION_SEND_SHARED_IMAGE = "com.clipcascade.android.action.SEND_SHARED_IMAGE"
        const val ACTION_SEND_SHARED_FILE_OFFER = "com.clipcascade.android.action.SEND_SHARED_FILE_OFFER"
        const val ACTION_SYNC_CLIPBOARD_NOW = "com.clipcascade.android.action.SYNC_CLIPBOARD_NOW"
        const val ACTION_REFRESH_DEVICE_COUNTS = "com.clipcascade.android.action.REFRESH_DEVICE_COUNTS"
        const val ACTION_HISTORY_CHANGED = "com.clipcascade.android.action.HISTORY_CHANGED"
        const val EXTRA_STATUS = "com.clipcascade.android.extra.STATUS"
        const val EXTRA_DETAIL = "com.clipcascade.android.extra.DETAIL"
        const val EXTRA_SHARED_TEXT = "com.clipcascade.android.extra.SHARED_TEXT"
        const val EXTRA_SHARED_IMAGE = "com.clipcascade.android.extra.SHARED_IMAGE"
        const val EXTRA_SHARED_IMAGE_PATH = "com.clipcascade.android.extra.SHARED_IMAGE_PATH"
        const val EXTRA_SHARED_FILE_OFFER = "com.clipcascade.android.extra.SHARED_FILE_OFFER"
        const val EXTRA_ONLINE_DEVICES = "com.clipcascade.android.extra.ONLINE_DEVICES"
        const val EXTRA_SYNC_TARGETS = "com.clipcascade.android.extra.SYNC_TARGETS"

        // 回环守卫时间窗：本地写入/收到某文本后的短时间内，不再把相同文本重新发出去
        const val LOOPBACK_GUARD_MS = 5_000L
        // 引擎启动失败后的自动重试间隔
        const val ENGINE_RETRY_DELAY_MS = 15_000L
    }
    
    private val notificationId = 1001
    private val channelId = "clipcascade_sync_channel"
    private val mainHandler = Handler(Looper.getMainLooper())
    
    // 后台同步引擎实例
    @Volatile
    private var engine: Engine? = null
    @Volatile
    private var engineStarting = false
    
    private lateinit var clipboardManager: ClipboardManager
    private lateinit var notificationManager: NotificationManager
    private lateinit var windowManager: WindowManager
    
    // 用于透明悬浮窗的视图
    private var overlayLayout: LinearLayout? = null
    private var clipboardReadTask: Runnable? = null
    private var pendingClipboardText: String? = null
    private var destroyed = false
    private val outgoingClipboardExecutor = Executors.newSingleThreadExecutor()
    
    // 记录最后一次本地写入剪贴板的文本与时间，防止回环死循环同步
    @Volatile
    private var lastWrittenText: String? = null
    @Volatile
    private var lastWrittenAt: Long = 0L

    private var sendWakeLock: PowerManager.WakeLock? = null

    // 网络恢复回调：Doze/断网导致 WS 死链后，onAvailable 会主动拉起引擎重连与补发
    private val networkCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            Log.i(TAG, "🌐 网络恢复可用，触发引擎重连/补发")
            mainHandler.post {
                engine?.reconnectNow()
                startEngineIfNeeded()
            }
        }
    }

    private val engineRetryRunnable = Runnable {
        if (engine == null && !engineStarting) {
            Log.i(TAG, "🔄 自动重试启动 Go 引擎...")
            startEngineIfNeeded()
        }
    }

    private val activeDeferredUploads = ConcurrentHashMap.newKeySet<String>()
    private val incomingPayloadExecutor = Executors.newSingleThreadExecutor()
    private val deferredUploadExecutor = Executors.newSingleThreadExecutor()
    private val apiSessionManager by lazy {
        ApiSessionManager(
            context = applicationContext,
            normalizeServerUrl = { normalizeServerUrl(it) },
            login = { serverURL, username, password -> loginForApi(serverURL, username, password) }
        )
    }

    override fun onCreate() {
        super.onCreate()
        Log.i(TAG, "🟢 后台服务正在初始化...")
        clipboardManager = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        notificationManager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        windowManager = getSystemService(Context.WINDOW_SERVICE) as WindowManager
        
        startForegroundService()
        DeferredFileSourceStore.cleanupExpired(this)
        
        // 也可以在这里监听系统的剪贴板回调（虽然 Android 10+ 后台此回调无效，但在前台或悬浮窗期间生效）
        clipboardManager.addPrimaryClipChangedListener(onClipChangeListener)

        // 网络恢复时主动触发引擎重连与补发（否则 Doze/断网后 WS 死链无法自愈）
        try {
            val cm = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
            cm.registerDefaultNetworkCallback(networkCallback)
        } catch (e: Exception) {
            Log.w(TAG, "注册网络回调失败: ${e.message}")
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        Log.i(TAG, "🚀 后台服务已启动 (onStartCommand)")
        startEngineIfNeeded()
        if (intent?.action == ACTION_REFRESH_DEVICE_COUNTS) {
            refreshDeviceCounts()
        } else if (intent?.action == ACTION_SYNC_CLIPBOARD_NOW) {
            requestClipboardReadForSync()
        } else if (intent?.action == ACTION_SEND_SHARED_TEXT) {
            val sharedText = intent.getStringExtra(EXTRA_SHARED_TEXT)?.trim().orEmpty()
            if (sharedText.isNotBlank()) {
                sendSharedPayloadWhenReady(sharedText, "text")
            }
        } else if (intent?.action == ACTION_SEND_SHARED_IMAGE) {
            val imagePath = intent.getStringExtra(EXTRA_SHARED_IMAGE_PATH)?.trim().orEmpty()
            val sharedImage = if (imagePath.isNotBlank()) {
                readImagePayloadFromFile(imagePath)
            } else {
                intent.getStringExtra(EXTRA_SHARED_IMAGE)?.trim().orEmpty()
            }
            if (sharedImage.isNotBlank()) {
                sendSharedPayloadWhenReady(sharedImage, "image", imagePath)
            }
        } else if (intent?.action == ACTION_SEND_SHARED_FILE_OFFER) {
            val sharedFileOffer = intent.getStringExtra(EXTRA_SHARED_FILE_OFFER)?.trim().orEmpty()
            if (sharedFileOffer.isNotBlank()) {
                sendSharedPayloadWhenReady(sharedFileOffer, "file_offer")
            }
        }
        
        // 保证服务被杀后系统自动重启它
        return START_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? {
        return null
    }

    /**
     * Android 10+ 后台读取需要窗口焦点，仅由明确的复制事件触发。
     * addView 后焦点授予是异步的，等窗口拿到焦点再读；窗口不能成为输入法目标。
     */
    fun requestClipboardReadForSync() {
        Log.i(TAG, "🔍 开始请求读取剪贴板...")
        startEngineIfNeeded()
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            // Android 10 以前没有后台读取限制，不需要悬浮窗。
            readClipboardAndSyncToServer()
            return
        }
        mainHandler.post {
            if (destroyed || clipboardReadTask != null) return@post
            try {
                showInvisibleOverlay()
                // 等待悬浮窗获得窗口焦点，最多约 2.4s（16 × 150ms）
                var attempts = 0
                val tryRead = object : Runnable {
                    override fun run() {
                        val view = overlayLayout
                        val hasFocus = view?.hasWindowFocus() == true
                        if (!hasFocus && attempts < 16) {
                            attempts++
                            mainHandler.postDelayed(this, 150L)
                            return
                        }
                        if (!hasFocus) {
                            Log.w(TAG, "⚠️ 悬浮窗 $attempts 次轮询后仍未获得窗口焦点，仍尝试读取（可能失败）")
                        } else {
                            Log.d(TAG, "👻 悬浮窗已获得焦点（第 $attempts 次轮询）")
                        }
                        clipboardReadTask = null
                        try {
                            readClipboardAndSyncToServer()
                        } finally {
                            removeInvisibleOverlay()
                        }
                    }
                }
                clipboardReadTask = tryRead
                mainHandler.postDelayed(tryRead, 50L)
            } catch (e: Exception) {
                Log.e(TAG, "悬浮窗或剪贴板操作失败: ${e.message}", e)
                removeInvisibleOverlay() // 确保即使崩溃也要尝试移掉视图
            }
        }
    }

    /**
     * 将其他设备推过来的文本写入 Android 的剪贴板
     */
    override fun onMessage(payload: String, payloadType: String, fileName: String) {
        Log.i(TAG, "📥 收到远端推送: [类型=$payloadType] 大小=${payload.length} chars")
        when (payloadType) {
            "text" -> mainHandler.post { handleIncomingText(payload) }
            "image" -> queueIncomingPayload { handleIncomingImage(payload) }
            "file_eager" -> queueIncomingPayload { handleIncomingEagerFile(payload, fileName) }
            "file_offer" -> mainHandler.post { handleIncomingFileOffer(payload) }
            "file_request" -> queueDeferredFileRequest(payload)
            else -> Log.d(TAG, "Android 不处理此剪贴板类型: $payloadType")
        }
    }

    private fun queueIncomingPayload(action: () -> Unit) {
        try {
            incomingPayloadExecutor.execute(action)
        } catch (_: RejectedExecutionException) {
            Log.d(TAG, "后台服务正在停止，忽略待处理内容")
        }
    }

    private fun handleIncomingText(payload: String) {
        try {
            // 如果恰好和我们最近写入的一样（时间窗内），证明是自己环路过来的，丢弃；
            // 超过时间窗后允许写入——否则同一内容永远无法再次从其他设备同步过来
            if (payload == lastWrittenText && System.currentTimeMillis() - lastWrittenAt < LOOPBACK_GUARD_MS) {
                Log.d(TAG, "丢弃 ${LOOPBACK_GUARD_MS / 1000}s 内的自回环文本。")
                return
            }

            // 后台写入不要求窗口焦点，不能为接收文本打断用户输入。
            // 写入前记录，防止 OnPrimaryClipChangedListener 再次触发发送。
            markLocalClipboardWrite(payload)
            val clip = ClipData.newPlainText("ClipCascade", payload)
            clipboardManager.setPrimaryClip(clip)
            ClipboardHistoryStore.append(this, payload, "received")
            notifyHistoryChanged()
            Log.i(TAG, "✅ 成功写入 Android 系统剪贴板")
        } catch (e: Exception) {
            Log.e(TAG, "写入剪贴板失败: ${e.message}", e)
        }
    }

    private fun handleIncomingImage(payload: String) {
        try {
            val bytes = Base64.decode(payload, Base64.DEFAULT)
            val file = saveHistoryImage(bytes, "received")
            if (file != null) {
                ClipboardHistoryStore.appendImage(this, file.absolutePath, "received")
                notifyHistoryChanged()
                Log.i(TAG, "✅ 已保存远端图片历史")
            }
        } catch (e: Exception) {
            Log.e(TAG, "保存远端图片失败: ${e.message}", e)
        }
    }

    private fun handleIncomingEagerFile(payload: String, originalFileName: String) {
        try {
            val fileName = safeFileName(originalFileName).ifBlank { "clipcascade-file" }
            val file = EagerFileStore.saveBase64(this, payload, fileName)
            ClipboardHistoryStore.replaceEagerFile(
                context = this,
                localFilePath = file.absolutePath,
                fileName = fileName,
                fileSize = file.length(),
                mimeType = mimeTypeForFileName(fileName),
                direction = "received"
            )
            notifyHistoryChanged()
            Log.i(TAG, "✅ 已缓存远端小文件: $fileName (${file.length()} bytes)")
        } catch (e: Exception) {
            Log.e(TAG, "保存远端小文件失败: ${e.message}", e)
        }
    }

    private fun mimeTypeForFileName(fileName: String): String {
        val extension = fileName.substringAfterLast('.', "").lowercase()
        return MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension)
            ?: "application/octet-stream"
    }

    private fun handleIncomingFileOffer(payload: String) {
        try {
            val fileName = appendFileOfferToHistory(payload, "received")
            Log.i(TAG, "✅ 已记录远端文件分享: $fileName")
        } catch (e: Exception) {
            Log.e(TAG, "处理远端文件分享失败", e)
        }
    }

    private fun appendFileOfferToHistory(payload: String, direction: String): String {
        val obj = org.json.JSONObject(payload)
        val fileName = obj.optString("file_name").ifBlank { "clipcascade-file" }
        ClipboardHistoryStore.appendFile(
            context = this,
            relayID = obj.optString("relay_id"),
            fileName = fileName,
            fileSize = obj.optLong("file_size", 0L),
            mimeType = obj.optString("mime_type").ifBlank { "application/octet-stream" },
            direction = direction
        )
        notifyHistoryChanged()
        return fileName
    }

    private fun queueDeferredFileRequest(payload: String) {
        val deferredID = try {
            org.json.JSONObject(payload).optString("deferred_id").trim()
        } catch (e: Exception) {
            Log.w(TAG, "无法解析延迟文件请求", e)
            return
        }
        if (deferredID.isBlank()) {
            return
        }
        if (!activeDeferredUploads.add(deferredID)) {
            Log.d(TAG, "延迟文件已排队或正在上传，忽略重复请求: $deferredID")
            return
        }
        try {
            deferredUploadExecutor.execute {
                try {
                    handleDeferredFileRequest(deferredID)
                } finally {
                    activeDeferredUploads.remove(deferredID)
                }
            }
        } catch (_: RejectedExecutionException) {
            activeDeferredUploads.remove(deferredID)
            Log.d(TAG, "后台服务正在停止，忽略延迟文件请求: $deferredID")
        }
    }

    private fun handleDeferredFileRequest(deferredID: String) {
        val source = DeferredFileSourceStore.get(this, deferredID) ?: run {
            Log.d(TAG, "本机没有延迟文件源，忽略请求: $deferredID")
            return
        }

        try {
            val file = File(source.path)
            if (!file.isFile || file.length() != source.fileSize) {
                DeferredFileSourceStore.remove(this, deferredID)
                reportDeferredFailure(deferredID, "source file is no longer available")
                return
            }
            val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
            val serverURL = normalizeServerUrl(prefs.getString("ServerURL", "") ?: "")
            val username = prefs.getString("Username", "")?.trim().orEmpty()
            val password = prefs.getString("Password", "").orEmpty()
            if (serverURL.isBlank() || username.isBlank() || password.isBlank()) {
                throw IllegalStateException("服务器账号未配置")
            }

            updateStatus("uploading", "正在上传：${source.fileName}")
            apiSessionManager.withSession(serverURL, username, password) { cookie ->
                markDeferredFileUploading(serverURL, cookie, deferredID, 0L)
            }
            var lastProgressAt = 0L
            val result = apiSessionManager.withSession(serverURL, username, password) { cookie ->
                uploadRelayFile(serverURL, cookie, file, source.fileName, source.mimeType) { sent, total ->
                    val now = System.currentTimeMillis()
                    if (now - lastProgressAt >= 1000L || sent == total) {
                        val percent = if (total > 0L) ((sent.coerceAtMost(total) * 100L) / total).toInt() else 0
                        updateNotificationContent("正在上传 ${source.fileName}：$percent%")
                        try {
                            markDeferredFileUploading(serverURL, cookie, deferredID, sent)
                            lastProgressAt = now
                        } catch (e: Exception) {
                            Log.w(TAG, "回报延迟文件上传进度失败: $deferredID", e)
                        }
                    }
                }
            }
            apiSessionManager.withSession(serverURL, username, password) { cookie ->
                markDeferredFileReady(serverURL, cookie, deferredID, result.relayID)
            }
            DeferredFileSourceStore.remove(this, deferredID)
            updateStatus("connected", "大文件已上传，可供其他设备下载")
            Log.i(TAG, "延迟文件上传完成: id=$deferredID relay=${result.relayID} name=${result.fileName}")
        } catch (e: Exception) {
            Log.e(TAG, "延迟文件上传失败: $deferredID", e)
            reportDeferredFailure(deferredID, e.message ?: "Android source upload failed")
            updateStatus("error", "大文件上传失败：${e.message}")
        }
    }

    private fun reportDeferredFailure(deferredID: String, message: String) {
        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        val serverURL = normalizeServerUrl(prefs.getString("ServerURL", "") ?: "")
        val username = prefs.getString("Username", "")?.trim().orEmpty()
        val password = prefs.getString("Password", "").orEmpty()
        if (serverURL.isBlank() || username.isBlank() || password.isBlank()) {
            return
        }
        try {
            apiSessionManager.withSession(serverURL, username, password) { cookie ->
                markDeferredFileFailed(serverURL, cookie, deferredID, message)
            }
        } catch (reportError: Exception) {
            Log.w(TAG, "回报延迟文件失败状态失败: $deferredID", reportError)
        }
    }

    override fun onStatusChange(status: String) {
        mainHandler.post {
            if (destroyed) return@post
            Log.i(TAG, "网络状态变更: $status")
            when (status) {
                "connected" -> updateStatus("connected", "已连接到服务器，实时同步中")
                "disconnected" -> updateStatus("disconnected", "连接已断开，等待自动重连")
                "reconnecting" -> updateStatus("reconnecting", "正在重连服务器...")
                "error" -> updateStatus("error", "连接错误")
                else -> updateStatus(status, "状态: $status")
            }
        }
    }

    private val onClipChangeListener = ClipboardManager.OnPrimaryClipChangedListener {
        // 前台/悬浮窗期间系统会触发此回调；无障碍仅在明确的复制事件后补充触发。
        // 这里真正执行同步：读取 guard 在 readClipboardAndSyncToServer 内部，
        // 自己写入引发的回调会被 lastWrittenText + 时间窗守卫拦下，不会造成回环。
        Log.d(TAG, "系统 OnPrimaryClipChanged 触发，尝试同步剪贴板")
        readClipboardAndSyncToServer()
    }

    private fun readClipboardAndSyncToServer() {
        val clip = try {
            clipboardManager.primaryClip
        } catch (e: Exception) {
            Log.w(TAG, "读取 primaryClip 异常: ${e.message}")
            null
        }
        if (clip == null || clip.itemCount == 0) {
            // Android 10+ 在无窗口焦点时 primaryClip 静默返回 null —— 打日志便于定位
            Log.w(TAG, "⚠️ primaryClip 不可读（可能未获得窗口焦点或剪贴板为空），本次跳过")
            return
        }
        val textToSync = clip.getItemAt(0).text?.toString()
        if (textToSync.isNullOrEmpty()) {
            Log.d(TAG, "剪贴板为空或非文本")
            return
        }
        if (textToSync == lastWrittenText && System.currentTimeMillis() - lastWrittenAt < LOOPBACK_GUARD_MS) {
            Log.d(TAG, "这是刚才本地写入/收到的文本（${LOOPBACK_GUARD_MS / 1000}s 内），防回环阻断上传。")
            return
        }

        Log.i(TAG, "📤 剪贴板文本等待发送 (${textToSync.length} chars)")
        // 引擎尚未创建时保留最新内容，不能把可空调用误当作发送成功。
        pendingClipboardText = textToSync
        markLocalClipboardWrite(textToSync)
        flushPendingClipboard()
    }

    private fun flushPendingClipboard() {
        val currentEngine = engine ?: return
        val text = pendingClipboardText ?: return
        pendingClipboardText = null
        // 网络写入可能等待超时，禁止阻塞主线程和悬浮窗的移除。
        outgoingClipboardExecutor.execute {
            acquireSendWakeLock()
            try {
                currentEngine.sendClipboard(text, "text")
                ClipboardHistoryStore.append(this, text, "sent")
                notifyHistoryChanged()
            } catch (e: Exception) {
                // Go 引擎的失败发送已入重发队列，避免 Kotlin 再入队造成重复。
                Log.w(TAG, "剪贴板已交给引擎等待重连补发: ${e.message}")
            } finally {
                releaseSendWakeLock()
            }
        }
    }

    private fun markLocalClipboardWrite(text: String) {
        lastWrittenText = text
        lastWrittenAt = System.currentTimeMillis()
    }

    private fun acquireSendWakeLock() {
        try {
            if (sendWakeLock == null) {
                val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
                sendWakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "ClipCascade:send").apply {
                    setReferenceCounted(false)
                }
            }
            // 发送 + 建连补发的短窗口；带超时防泄漏
            sendWakeLock?.acquire(15_000L)
        } catch (e: Exception) {
            Log.w(TAG, "获取 WakeLock 失败: ${e.message}")
        }
    }

    private fun releaseSendWakeLock() {
        try {
            sendWakeLock?.takeIf { it.isHeld }?.release()
        } catch (_: Exception) {
        }
    }

    private fun sendSharedPayloadWhenReady(payload: String, payloadType: String, localImagePath: String? = null) {
        Thread {
            val deadline = System.currentTimeMillis() + 8000L
            while ((engine == null || engine?.isConnected != true) && System.currentTimeMillis() < deadline) {
                try {
                    Thread.sleep(120L)
                } catch (_: InterruptedException) {
                    return@Thread
                }
            }

            val currentEngine = engine
            if (currentEngine == null || !currentEngine.isConnected) {
                updateStatus("error", "分享失败：请先配置并连接服务器")
                Log.w(TAG, "分享失败：engine 尚未连接")
                return@Thread
            }

            try {
                currentEngine.sendClipboard(payload, payloadType)
                if (payloadType == "text") {
                    markLocalClipboardWrite(payload)
                    ClipboardHistoryStore.append(this, payload, "sent")
                    notifyHistoryChanged()
                } else if (payloadType == "image") {
                    val path = localImagePath?.takeIf { File(it).exists() }
                    if (path != null) {
                        ClipboardHistoryStore.appendImage(this, path, "sent")
                        notifyHistoryChanged()
                    } else {
                        val bytes = Base64.decode(payload, Base64.DEFAULT)
                        saveHistoryImage(bytes, "sent")?.let {
                            ClipboardHistoryStore.appendImage(this, it.absolutePath, "sent")
                            notifyHistoryChanged()
                        }
                    }
                } else if (payloadType == "file_offer") {
                    appendFileOfferToHistory(payload, "sent")
                }
                val label = when (payloadType) {
                    "image" -> "图片"
                    "file_offer" -> "文件"
                    else -> "文本"
                }
                updateStatus("connected", "已发送分享$label")
                Log.i(TAG, "✅ 分享内容已发送: $payloadType")
            } catch (e: Exception) {
                updateStatus("error", "分享失败：${e.message}")
                Log.e(TAG, "分享发送失败", e)
            }
        }.start()
    }

    private fun readImagePayloadFromFile(path: String): String {
        return try {
            Base64.encodeToString(File(path).readBytes(), Base64.NO_WRAP)
        } catch (e: Exception) {
            Log.e(TAG, "读取分享图片缓存失败", e)
            ""
        }
    }

    private fun startEngineIfNeeded() {
        if (destroyed) return
        val currentEngine = engine
        if (currentEngine != null) {
            if (currentEngine.isConnected) {
                updateStatus("connected", "已连接到服务器，实时同步中")
            } else {
                updateStatus("reconnecting", "连接已断开，正在自动重连...")
            }
            flushPendingClipboard()
            return
        }
        if (engineStarting) {
            updateStatus("connecting", "正在连接服务器...")
            return
        }
        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        val url = prefs.getString("ServerURL", "") ?: ""
        val user = prefs.getString("Username", "") ?: ""
        val pass = prefs.getString("Password", "") ?: ""
        val e2e = prefs.getBoolean("E2EE", true)

        if (url.isBlank() || user.isBlank() || pass.isBlank()) {
            updateStatus("disconnected", "未配置服务器账号，请先在应用内填写配置")
            return
        }

        engineStarting = true
        updateStatus("connecting", "正在连接 $url")
        Thread {
            try {
                val created = Bridge.newEngine(url, user, pass, e2e)
                created.setCallback(this)
                created.start()
                mainHandler.post {
                    engineStarting = false
                    if (destroyed) {
                        created.stop()
                    } else {
                        engine = created
                        updateStatus("connected", "已连接到 $url")
                        flushPendingClipboard()
                        refreshDeviceCounts()
                    }
                }
            } catch (e: Exception) {
                Log.e(TAG, "Go 引擎启动失败", e)
                mainHandler.post {
                    engineStarting = false
                    if (!destroyed) {
                        engine = null
                        updateStatus("error", "连接失败: ${e.message}，${ENGINE_RETRY_DELAY_MS / 1000}s 后自动重试")
                        mainHandler.removeCallbacks(engineRetryRunnable)
                        mainHandler.postDelayed(engineRetryRunnable, ENGINE_RETRY_DELAY_MS)
                    }
                }
            }
        }.start()
    }

    // ==================
    // 悬浮窗控制部分
    // ==================
    private fun showInvisibleOverlay() {
        if (overlayLayout != null) {
            return // 已经有了
        }

        overlayLayout = LinearLayout(this).apply {
            alpha = 0f // 完全透明
            val color = ContextCompat.getColor(this@ClipCascadeBackgroundService, android.R.color.transparent)
            setBackgroundColor(color)
            orientation = LinearLayout.VERTICAL
        }
        
        val layoutParams = WindowManager.LayoutParams(
            1, 1, // 越小越好，1x1 像素
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY, // API 26+ Android 8.0 强制
            // 保留剪贴板读取所需焦点，但禁止成为输入法目标，避免键盘弹出/收起。
            WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE or
                WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL or
                WindowManager.LayoutParams.FLAG_ALT_FOCUSABLE_IM,
            PixelFormat.TRANSPARENT
        ).apply {
            alpha = 0f
            softInputMode = WindowManager.LayoutParams.SOFT_INPUT_STATE_UNCHANGED or
                WindowManager.LayoutParams.SOFT_INPUT_ADJUST_NOTHING
        }
        
        windowManager.addView(overlayLayout, layoutParams)
        Log.v(TAG, "添加剪贴板读取窗口（不参与输入法）")
    }

    private fun removeInvisibleOverlay() {
        overlayLayout?.let {
            try {
                windowManager.removeView(it)
                Log.v(TAG, "移除剪贴板读取窗口")
            } catch (e: Exception) {
                Log.w(TAG, "移除悬浮窗出错: ${e.message}")
            }
        }
        overlayLayout = null
    }

    // ==================
    // 前台服务及通知部分
    // ==================
    private fun startForegroundService() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                channelId,
                getString(R.string.notification_channel_name),
                NotificationManager.IMPORTANCE_LOW // 低优先级，避免打扰，不发声
            )
            notificationManager.createNotificationChannel(channel)
        }

        // 点击通知栏打开主 Activity (可选，这里先留空或指向 MainActivity)
        val pendingIntent = PendingIntent.getActivity(
            this, 0, 
            Intent(this, MainActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK
            },
            PendingIntent.FLAG_IMMUTABLE
        )

        val notification = Notification.Builder(this, channelId)
            .setContentTitle(getString(R.string.notification_title))
            .setContentText("服务正在后台时刻准备着...")
            // 前台通知必须有小图标，否则在部分系统上会直接启动失败。
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentIntent(pendingIntent)
            .setOngoing(true) // 划不掉
            .build()
            
        // 适配 Android 14 要求的类型
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                startForeground(notificationId, notification, android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
            } else {
                startForeground(notificationId, notification)
            }
        } catch (e: Exception) {
            Log.e(TAG, "启动前台服务失败，降级不带 Type 启动", e)
            startForeground(notificationId, notification)
        }
    }

    private fun updateNotificationContent(text: String) {
        val notification = Notification.Builder(this, channelId)
            .setContentTitle(getString(R.string.notification_title))
            .setContentText(text)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setOngoing(true)
            .build()
        notificationManager.notify(notificationId, notification)
    }

    private fun refreshDeviceCounts() {
        val currentEngine = engine
        if (currentEngine == null || !currentEngine.isConnected) {
            Log.d(TAG, "跳过在线设备刷新：engine 尚未连接")
            return
        }
        Thread {
            try {
                val raw = currentEngine.deviceCounts()
                val parts = raw.split(",", limit = 2)
                val onlineDevices = parts.getOrNull(0)?.toIntOrNull() ?: 0
                val syncTargets = parts.getOrNull(1)?.toIntOrNull() ?: 0
                Log.d(TAG, "在线设备统计：online=$onlineDevices targets=$syncTargets")
                getSharedPreferences("clipcascade", Context.MODE_PRIVATE).edit()
                    .putInt("OnlineDevices", onlineDevices)
                    .putInt("SyncTargets", syncTargets)
                    .putLong("DeviceCountsUpdatedAt", System.currentTimeMillis())
                    .apply()
                sendBroadcast(Intent(ACTION_STATUS_CHANGED).apply {
                    setPackage(packageName)
                    putExtra(EXTRA_STATUS, "connected")
                    putExtra(EXTRA_DETAIL, "在线设备已刷新")
                    putExtra(EXTRA_ONLINE_DEVICES, onlineDevices)
                    putExtra(EXTRA_SYNC_TARGETS, syncTargets)
                })
            } catch (e: Exception) {
                Log.d(TAG, "刷新在线设备数失败", e)
            }
        }.start()
    }

    private fun updateStatus(status: String, detail: String) {
        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        prefs.edit()
            .putString("ConnectionStatus", status)
            .putString("ConnectionDetail", detail)
            .putLong("ConnectionUpdatedAt", System.currentTimeMillis())
            .apply()

        updateNotificationContent(detail)
        sendBroadcast(Intent(ACTION_STATUS_CHANGED).apply {
            setPackage(packageName)
            putExtra(EXTRA_STATUS, status)
            putExtra(EXTRA_DETAIL, detail)
        })
    }

    private fun notifyHistoryChanged() {
        sendBroadcast(Intent(ACTION_HISTORY_CHANGED).apply {
            setPackage(packageName)
        })
    }

    private fun saveHistoryImage(bytes: ByteArray, direction: String): File? {
        return try {
            val dir = File(cacheDir, "history-images").apply { mkdirs() }
            val extension = AndroidSharePolicy.historyImageExtension(bytes)
            val file = File(dir, "${System.currentTimeMillis()}-$direction-${UUID.randomUUID()}.$extension")
            FileOutputStream(file).use { it.write(bytes) }
            file
        } catch (e: Exception) {
            Log.w(TAG, "保存图片历史失败", e)
            null
        }
    }

    override fun onTaskRemoved(rootIntent: Intent?) {
        super.onTaskRemoved(rootIntent)
        // 用户从最近任务划掉应用：前台服务继续保持；若引擎尚未起来则触发重试
        Log.i(TAG, "用户划掉应用任务，前台服务继续保持运行")
        if (engine == null && !engineStarting) {
            mainHandler.post { startEngineIfNeeded() }
        }
    }

    override fun onDestroy() {
        Log.i(TAG, "🔴 后台服务被销毁")
        destroyed = true
        clipboardReadTask?.let(mainHandler::removeCallbacks)
        clipboardReadTask = null
        clipboardManager.removePrimaryClipChangedListener(onClipChangeListener)
        try {
            val cm = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
            cm.unregisterNetworkCallback(networkCallback)
        } catch (_: Exception) {
        }
        mainHandler.removeCallbacks(engineRetryRunnable)
        releaseSendWakeLock()
        try {
            engine?.stop()
        } catch (_: Exception) {
        }
        engine = null
        incomingPayloadExecutor.shutdownNow()
        deferredUploadExecutor.shutdownNow()
        outgoingClipboardExecutor.shutdownNow()
        removeInvisibleOverlay()
        
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            stopForeground(STOP_FOREGROUND_REMOVE)
        } else {
            @Suppress("DEPRECATION")
            stopForeground(true)
        }
        super.onDestroy()
    }
}
