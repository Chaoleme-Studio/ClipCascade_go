package com.clipcascade.android

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.OpenableColumns
import android.util.Log
import android.widget.Toast
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.FileOutputStream
import java.io.OutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.util.UUID

class ShareActivity : Activity() {
    private val tag = "ClipCascade_Share"
    private val apiSessionManager by lazy {
        ApiSessionManager(
            context = applicationContext,
            normalizeServerUrl = { normalizeServerUrl(it) },
            login = { serverURL, username, password -> loginForApi(serverURL, username, password) }
        )
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        handleShareIntent(intent)
    }

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleShareIntent(intent)
    }

    private fun handleShareIntent(intent: Intent?) {
        if (intent == null) {
            finish()
            return
        }
        val sharedUri = if (intent.action == Intent.ACTION_SEND) {
            streamUriFrom(intent)
        } else {
            null
        }
        val sharedSize = sharedUri?.let { queryContentSize(it) }
        val decision = AndroidSharePolicy.classify(intent.type, sharedSize, sharedUri != null)
        val sharedText = when (intent.action) {
            Intent.ACTION_SEND -> {
                if (decision == ShareDecision.DirectText) intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString() else null
            }
            Intent.ACTION_PROCESS_TEXT -> intent.getCharSequenceExtra(Intent.EXTRA_PROCESS_TEXT)?.toString()
            else -> null
        }?.trim()

        if (!sharedText.isNullOrBlank()) {
            startCoreService(sharedText = sharedText)
            Toast.makeText(this, "已接收分享文本，正在同步", Toast.LENGTH_SHORT).show()
            finish()
            return
        }

        if (intent.action != Intent.ACTION_SEND) {
            finish()
            return
        }

        when (decision) {
            ShareDecision.DirectImage -> {
                val imageFile = sharedUri?.let { saveSharedImageForSend(it) }
                if (imageFile != null) {
                    startCoreService(sharedImagePath = imageFile.absolutePath)
                    Toast.makeText(this, "已接收分享图片，正在同步", Toast.LENGTH_SHORT).show()
                }
                finish()
            }
            ShareDecision.DirectFile -> {
                if (sharedUri != null) {
                    uploadSharedFileAndSendOffer(sharedUri, intent.type, sharedSize)
                    Toast.makeText(this, "已接收分享文件，正在上传同步", Toast.LENGTH_SHORT).show()
                } else {
                    finish()
                }
            }
            ShareDecision.TooLarge -> {
                Toast.makeText(this, "文件超过 25 MiB，Android 端不自动发送", Toast.LENGTH_LONG).show()
                finish()
            }
            ShareDecision.UnsupportedFile -> {
                Toast.makeText(this, "不支持此分享内容", Toast.LENGTH_LONG).show()
                finish()
            }
            ShareDecision.DirectText -> finish()
        }
    }

    private fun streamUriFrom(intent: Intent): Uri? {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
        } else {
            @Suppress("DEPRECATION")
            intent.getParcelableExtra(Intent.EXTRA_STREAM)
        }
    }

    private fun queryContentSize(uri: Uri): Long? {
        contentResolver.query(uri, arrayOf(OpenableColumns.SIZE), null, null, null)?.use { cursor ->
            if (cursor.moveToFirst()) {
                val idx = cursor.getColumnIndex(OpenableColumns.SIZE)
                if (idx >= 0 && !cursor.isNull(idx)) {
                    val size = cursor.getLong(idx)
                    if (size >= 0) {
                        return size
                    }
                }
            }
        }
        try {
            contentResolver.openAssetFileDescriptor(uri, "r")?.use { descriptor ->
                if (descriptor.length >= 0) {
                    return descriptor.length
                }
            }
        } catch (e: Exception) {
            Log.d(tag, "无法通过 AssetFileDescriptor 获取分享文件大小", e)
        }
        try {
            contentResolver.openFileDescriptor(uri, "r")?.use { descriptor ->
                if (descriptor.statSize >= 0) {
                    return descriptor.statSize
                }
            }
        } catch (e: Exception) {
            Log.d(tag, "无法通过 ParcelFileDescriptor 获取分享文件大小", e)
        }
        return null
    }

    private fun queryContentName(uri: Uri): String {
        contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cursor ->
            if (cursor.moveToFirst()) {
                val idx = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                if (idx >= 0 && !cursor.isNull(idx)) {
                    val name = safeFileName(cursor.getString(idx))
                    if (name.isNotBlank()) {
                        return name
                    }
                }
            }
        }
        return safeFileName(uri.lastPathSegment ?: "clipcascade-file")
    }

    private fun uploadSharedFileAndSendOffer(uri: Uri, mimeType: String?, sizeHint: Long?) {
        val prefs = getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        val serverURL = normalizeServerUrl(prefs.getString("ServerURL", "") ?: "")
        val username = prefs.getString("Username", "")?.trim().orEmpty()
        val password = prefs.getString("Password", "").orEmpty()
        if (serverURL.isBlank() || username.isBlank() || password.isBlank()) {
            Toast.makeText(this, "请先保存服务器账号", Toast.LENGTH_SHORT).show()
            finish()
            return
        }
        if (sizeHint != null && sizeHint > AndroidSharePolicy.MAX_DIRECT_BYTES) {
            Toast.makeText(this, "文件超过 25 MiB，Android 端不自动发送", Toast.LENGTH_LONG).show()
            finish()
            return
        }
        val fileName = queryContentName(uri)
        val uploadMimeType = mimeType?.takeIf { it.isNotBlank() } ?: "application/octet-stream"
        Thread {
            var stagedFile: File? = null
            try {
                val staged = stageSharedFile(uri, fileName)
                stagedFile = staged.file
                val result = apiSessionManager.withSession(serverURL, username, password) { cookie ->
                    uploadSharedFile(serverURL, cookie, staged.file, fileName, uploadMimeType, staged.size)
                }
                val offer = JSONObject()
                    .put("transfer_id", "android-share-${System.currentTimeMillis()}")
                    .put("relay_id", result.relayID)
                    .put("file_name", result.fileName)
                    .put("file_size", result.fileSize)
                    .put("sha256", result.sha256)
                    .put("mime_type", result.mimeType)
                    .put("transport", "http_relay")
                    .toString()
                startCoreService(sharedFileOffer = offer, showToast = false)
                runOnUiThread {
                    ClipboardHistoryStore.append(this, "文件：${result.fileName}", "sent")
                    notifyHistoryChanged()
                    Toast.makeText(this, "文件已上传并同步：${result.fileName}", Toast.LENGTH_LONG).show()
                    finish()
                }
            } catch (e: ShareFileTooLargeException) {
                Log.i(tag, "分享文件超过 Android 端自动发送上限")
                runOnUiThread {
                    Toast.makeText(this, "文件超过 25 MiB，Android 端不自动发送", Toast.LENGTH_LONG).show()
                    finish()
                }
            } catch (e: Exception) {
                Log.e(tag, "分享文件同步失败", e)
                runOnUiThread {
                    Toast.makeText(this, "文件同步失败：${e.message}", Toast.LENGTH_LONG).show()
                    finish()
                }
            } finally {
                stagedFile?.delete()
            }
        }.start()
    }

    private fun uploadSharedFile(
        serverURL: String,
        cookie: String,
        file: File,
        fileName: String,
        mimeType: String,
        fileSize: Long
    ): SharedFileUploadResult {
        val boundary = "ClipCascade-${UUID.randomUUID()}"
        val baseURL = normalizeServerUrl(serverURL)
        val conn = (URL("${baseURL.trimEnd('/')}/api/files/upload").openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            connectTimeout = 6000
            readTimeout = 60000
            doOutput = true
            setRequestProperty("Cookie", cookie)
            setRequestProperty("Content-Type", "multipart/form-data; boundary=$boundary")
            setRequestProperty("X-File-Size", fileSize.toString())
        }
        try {
            conn.outputStream.use { output ->
                writeMultipartFile(output, boundary, file, fileName, mimeType)
            }
            if (conn.responseCode == HttpURLConnection.HTTP_UNAUTHORIZED || conn.responseCode == HttpURLConnection.HTTP_FORBIDDEN) {
                throw ApiUnauthorizedException("upload unauthorized: ${conn.responseCode}")
            }
            if (conn.responseCode != HttpURLConnection.HTTP_OK) {
                val error = conn.errorStream?.bufferedReader()?.use { it.readText() }.orEmpty()
                throw IllegalStateException("upload failed: ${conn.responseCode} $error")
            }
            val raw = conn.inputStream.bufferedReader().use { it.readText() }
            val obj = JSONObject(raw)
            return SharedFileUploadResult(
                relayID = obj.optString("relay_id"),
                fileName = obj.optString("file_name").ifBlank { fileName },
                fileSize = obj.optLong("file_size", fileSize),
                sha256 = obj.optString("sha256"),
                mimeType = obj.optString("mime_type").ifBlank { mimeType }
            )
        } finally {
            conn.disconnect()
        }
    }

    private fun stageSharedFile(uri: Uri, fileName: String): StagedSharedFile {
        val dir = File(cacheDir, "shared-upload").apply { mkdirs() }
        val temp = File.createTempFile("share-", "-${safeFileName(fileName)}", dir)
        var total = 0L
        try {
            contentResolver.openInputStream(uri)?.use { input ->
                FileOutputStream(temp).use { output ->
                    val buffer = ByteArray(64 * 1024)
                    while (true) {
                        val read = input.read(buffer)
                        if (read <= 0) {
                            break
                        }
                        total += read.toLong()
                        if (total > AndroidSharePolicy.MAX_DIRECT_BYTES) {
                            throw ShareFileTooLargeException()
                        }
                        output.write(buffer, 0, read)
                    }
                }
            } ?: throw IllegalStateException("无法读取分享文件")
            if (total <= 0) {
                throw IllegalStateException("分享文件为空")
            }
            return StagedSharedFile(temp, total)
        } catch (e: Exception) {
            temp.delete()
            throw e
        }
    }

    private fun writeMultipartFile(output: OutputStream, boundary: String, file: File, fileName: String, mimeType: String) {
        val header = "--$boundary\r\n" +
            "Content-Disposition: form-data; name=\"file\"; filename=\"${multipartEscape(fileName)}\"\r\n" +
            "Content-Type: $mimeType\r\n\r\n"
        output.write(header.toByteArray(Charsets.UTF_8))
        file.inputStream().use { input -> input.copyTo(output, 64 * 1024) }
        output.write("\r\n--$boundary--\r\n".toByteArray(Charsets.UTF_8))
    }

    private fun multipartEscape(value: String): String {
        return value.replace("\\", "\\\\").replace("\"", "\\\"").replace("\r", "_").replace("\n", "_")
    }

    private fun saveSharedImageForSend(uri: Uri): File? {
        return try {
            contentResolver.openInputStream(uri)?.use { input ->
                val out = ByteArrayOutputStream()
                val buffer = ByteArray(16 * 1024)
                var total = 0L
                while (true) {
                    val read = input.read(buffer)
                    if (read <= 0) {
                        break
                    }
                    total += read.toLong()
                    if (total > AndroidSharePolicy.MAX_DIRECT_BYTES) {
                        Toast.makeText(this, "图片超过 25 MiB，Android 端不自动发送", Toast.LENGTH_LONG).show()
                        return null
                    }
                    out.write(buffer, 0, read)
                }
                val bytes = out.toByteArray()
                saveHistoryImage(bytes, "sent")
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
            val extension = AndroidSharePolicy.historyImageExtension(bytes)
            val file = File(dir, "${System.currentTimeMillis()}-$direction-${UUID.randomUUID()}.$extension")
            FileOutputStream(file).use { it.write(bytes) }
            file
        } catch (e: Exception) {
            Log.w(tag, "保存图片历史失败", e)
            null
        }
    }

    private fun startCoreService(
        sharedText: String? = null,
        sharedImageBase64: String? = null,
        sharedImagePath: String? = null,
        sharedFileOffer: String? = null,
        showToast: Boolean = true
    ) {
        val intent = Intent(this, ClipCascadeBackgroundService::class.java).apply {
            if (!sharedText.isNullOrBlank()) {
                action = ClipCascadeBackgroundService.ACTION_SEND_SHARED_TEXT
                putExtra(ClipCascadeBackgroundService.EXTRA_SHARED_TEXT, sharedText)
            } else if (!sharedImageBase64.isNullOrBlank()) {
                action = ClipCascadeBackgroundService.ACTION_SEND_SHARED_IMAGE
                putExtra(ClipCascadeBackgroundService.EXTRA_SHARED_IMAGE, sharedImageBase64)
            } else if (!sharedImagePath.isNullOrBlank()) {
                action = ClipCascadeBackgroundService.ACTION_SEND_SHARED_IMAGE
                putExtra(ClipCascadeBackgroundService.EXTRA_SHARED_IMAGE_PATH, sharedImagePath)
            } else if (!sharedFileOffer.isNullOrBlank()) {
                action = ClipCascadeBackgroundService.ACTION_SEND_SHARED_FILE_OFFER
                putExtra(ClipCascadeBackgroundService.EXTRA_SHARED_FILE_OFFER, sharedFileOffer)
            }
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(intent)
        } else {
            startService(intent)
        }
        if (showToast) {
            Toast.makeText(this, "服务已启动，请检查通知栏", Toast.LENGTH_SHORT).show()
        }
    }

    private fun notifyHistoryChanged() {
        sendBroadcast(Intent(ClipCascadeBackgroundService.ACTION_HISTORY_CHANGED).apply {
            setPackage(packageName)
        })
    }

    private data class SharedFileUploadResult(
        val relayID: String,
        val fileName: String,
        val fileSize: Long,
        val sha256: String,
        val mimeType: String
    )

    private data class StagedSharedFile(val file: File, val size: Long)

    private class ShareFileTooLargeException : Exception()
}
