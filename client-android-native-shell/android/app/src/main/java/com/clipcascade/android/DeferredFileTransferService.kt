package com.clipcascade.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.os.IBinder
import android.os.Looper
import android.os.Handler
import android.os.StatFs
import android.util.Log
import android.widget.Toast
import java.io.File
import java.io.FileOutputStream
import java.util.concurrent.Executors

class DeferredFileTransferService : Service() {
    companion object {
        private const val ACTION_PREPARE = "com.clipcascade.android.action.PREPARE_DEFERRED_FILE"
        private const val EXTRA_FILE_NAME = "com.clipcascade.android.extra.DEFERRED_FILE_NAME"
        private const val EXTRA_FILE_SIZE = "com.clipcascade.android.extra.DEFERRED_FILE_SIZE"
        private const val EXTRA_MIME_TYPE = "com.clipcascade.android.extra.DEFERRED_MIME_TYPE"
        private const val NOTIFICATION_ID = 1002
        private const val CHANNEL_ID = "clipcascade_file_transfer_channel"

        fun startPreparing(
            context: Context,
            uri: Uri,
            fileName: String,
            fileSize: Long,
            mimeType: String
        ) {
            val intent = Intent(context, DeferredFileTransferService::class.java).apply {
                action = ACTION_PREPARE
                data = uri
                clipData = ClipData.newRawUri("ClipCascade deferred file", uri)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                putExtra(EXTRA_FILE_NAME, fileName)
                putExtra(EXTRA_FILE_SIZE, fileSize)
                putExtra(EXTRA_MIME_TYPE, mimeType)
            }
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(intent)
            } else {
                context.startService(intent)
            }
        }
    }

    private val tag = "ClipCascade_FileSvc"
    private val executor = Executors.newSingleThreadExecutor()
    private val mainHandler = Handler(Looper.getMainLooper())
    private lateinit var notificationManager: NotificationManager
    private val apiSessionManager by lazy {
        ApiSessionManager(
            context = applicationContext,
            normalizeServerUrl = { normalizeServerUrl(it) },
            login = { serverURL, username, password -> loginForApi(serverURL, username, password) }
        )
    }

    override fun onCreate() {
        super.onCreate()
        notificationManager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        createNotificationChannel()
        startAsForeground(buildNotification("正在准备大文件", 0, true))
        DeferredFileSourceStore.cleanupExpired(this)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action != ACTION_PREPARE || intent.data == null) {
            stopSelfResult(startId)
            return START_NOT_STICKY
        }
        val uri = intent.data ?: return START_NOT_STICKY
        val fileName = safeFileName(intent.getStringExtra(EXTRA_FILE_NAME).orEmpty()).take(180)
        val sizeHint = intent.getLongExtra(EXTRA_FILE_SIZE, -1L)
        val mimeType = intent.getStringExtra(EXTRA_MIME_TYPE)?.takeIf { it.isNotBlank() }
            ?: "application/octet-stream"
        executor.execute {
            prepareDeferredSource(uri, fileName, sizeHint, mimeType)
            stopSelfResult(startId)
        }
        return START_NOT_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun prepareDeferredSource(uri: Uri, fileName: String, sizeHint: Long, mimeType: String) {
        var stagedFile: File? = null
        try {
            if (sizeHint > AndroidSharePolicy.MAX_RELAY_BYTES) {
                throw DeferredFileTooLargeException()
            }
            val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
            val serverURL = normalizeServerUrl(prefs.getString("ServerURL", "") ?: "")
            val username = prefs.getString("Username", "")?.trim().orEmpty()
            val password = prefs.getString("Password", "").orEmpty()
            if (serverURL.isBlank() || username.isBlank() || password.isBlank()) {
                throw IllegalStateException("请先保存服务器账号")
            }

            val staged = copyToPrivateSource(uri, sizeHint) { copied, total ->
                updateProgressNotification("正在保存 $fileName", copied, total)
            }
            stagedFile = staged
            if (staged.length() <= AndroidSharePolicy.MAX_AUTO_RELAY_BYTES) {
                throw IllegalStateException("文件未超过 25 MiB，请重新分享")
            }
            updateNotification("正在登记 $fileName")
            val registration = apiSessionManager.withSession(serverURL, username, password) { cookie ->
                registerDeferredFile(serverURL, cookie, fileName, staged.length(), mimeType)
            }
            val finalFile = File(DeferredFileSourceStore.sourceDirectory(this), "${registration.deferredID}.source")
            if (!staged.renameTo(finalFile)) {
                throw IllegalStateException("无法保存延迟文件源")
            }
            stagedFile = null
            DeferredFileSourceStore.put(
                this,
                DeferredFileSource(
                    deferredID = registration.deferredID,
                    path = finalFile.absolutePath,
                    fileName = registration.fileName,
                    fileSize = registration.fileSize,
                    mimeType = registration.mimeType,
                    expiresAtMillis = registration.expiresAtMillis
                )
            )
            ClipboardHistoryStore.append(this, "文件：${registration.fileName}", "sent")
            sendBroadcast(Intent(ClipCascadeBackgroundService.ACTION_HISTORY_CHANGED).apply {
                setPackage(packageName)
            })
            updateNotification("已登记：${registration.fileName}")
            showToast("大文件已登记，其他设备请求下载时再上传")
            Log.i(tag, "延迟文件已登记: id=${registration.deferredID} name=${registration.fileName} size=${registration.fileSize}")
        } catch (e: DeferredFileTooLargeException) {
            Log.i(tag, "分享文件超过 2 GiB")
            updateNotification("文件超过 2 GiB，无法登记")
            showToast("文件超过 2 GiB，暂不支持")
        } catch (e: Exception) {
            Log.e(tag, "延迟文件准备失败", e)
            updateNotification("大文件登记失败")
            showToast("大文件登记失败：${e.message}")
        } finally {
            stagedFile?.delete()
        }
    }

    private fun copyToPrivateSource(
        uri: Uri,
        sizeHint: Long,
        onProgress: (copiedBytes: Long, totalBytes: Long) -> Unit
    ): File {
        val dir = DeferredFileSourceStore.sourceDirectory(this)
        if (sizeHint > 0L && StatFs(dir.absolutePath).availableBytes < sizeHint) {
            throw IllegalStateException("存储空间不足，无法保存大文件源")
        }
        val temp = File.createTempFile("source-", ".preparing", dir)
        var copied = 0L
        var lastNotificationAt = 0L
        try {
            contentResolver.openInputStream(uri)?.use { input ->
                FileOutputStream(temp).buffered(256 * 1024).use { output ->
                    val buffer = ByteArray(256 * 1024)
                    while (true) {
                        val read = input.read(buffer)
                        if (read <= 0) {
                            break
                        }
                        copied += read.toLong()
                        if (copied > AndroidSharePolicy.MAX_RELAY_BYTES) {
                            throw DeferredFileTooLargeException()
                        }
                        output.write(buffer, 0, read)
                        val now = System.currentTimeMillis()
                        if (now - lastNotificationAt >= 500L) {
                            onProgress(copied, sizeHint)
                            lastNotificationAt = now
                        }
                    }
                }
            } ?: throw IllegalStateException("无法读取分享文件")
            if (copied <= 0L) {
                throw IllegalStateException("分享文件为空")
            }
            onProgress(copied, copied)
            return temp
        } catch (e: Exception) {
            temp.delete()
            throw e
        }
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            notificationManager.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "大文件传输", NotificationManager.IMPORTANCE_LOW)
            )
        }
    }

    private fun startAsForeground(notification: Notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
    }

    private fun updateProgressNotification(text: String, done: Long, total: Long) {
        val hasTotal = total > 0L
        val progress = if (hasTotal) ((done.coerceAtMost(total) * 100L) / total).toInt() else 0
        notificationManager.notify(NOTIFICATION_ID, buildNotification(text, progress, !hasTotal))
    }

    private fun updateNotification(text: String) {
        notificationManager.notify(NOTIFICATION_ID, buildNotification(text, 0, false))
    }

    private fun buildNotification(text: String, progress: Int, indeterminate: Boolean): Notification {
        val pendingIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("ClipCascade 大文件")
            .setContentText(text)
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentIntent(pendingIntent)
            .setOngoing(true)
            .setProgress(100, progress, indeterminate)
            .build()
    }

    private fun showToast(message: String) {
        mainHandler.post { Toast.makeText(this, message, Toast.LENGTH_LONG).show() }
    }

    override fun onDestroy() {
        executor.shutdownNow()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            stopForeground(STOP_FOREGROUND_REMOVE)
        } else {
            @Suppress("DEPRECATION")
            stopForeground(true)
        }
        super.onDestroy()
    }

    private class DeferredFileTooLargeException : Exception()
}
