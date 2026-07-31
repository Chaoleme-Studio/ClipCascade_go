package com.clipcascade.android

import android.content.ContentValues
import android.content.Context
import android.media.MediaScannerConnection
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import androidx.annotation.RequiresApi
import androidx.core.content.FileProvider
import java.io.File
import java.io.OutputStream

data class PublicDownload(
    val uri: Uri,
    val fileName: String
)

object PublicDownloadStore {
    fun save(
        context: Context,
        fileName: String,
        mimeType: String,
        writer: (OutputStream) -> Unit
    ): PublicDownload {
        val safeName = safeFileName(fileName).take(180)
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            saveWithMediaStore(context, safeName, mimeType, writer)
        } else {
            saveLegacy(context, safeName, mimeType, writer)
        }
    }

    fun saveFile(context: Context, source: File, fileName: String, mimeType: String): PublicDownload {
        return save(context, fileName, mimeType) { output ->
            source.inputStream().buffered(256 * 1024).use { input ->
                input.copyTo(output, 256 * 1024)
            }
        }
    }

    fun isAvailable(context: Context, uri: Uri): Boolean {
        return try {
            context.contentResolver.openAssetFileDescriptor(uri, "r")?.use { true } ?: false
        } catch (_: Exception) {
            false
        }
    }

    @RequiresApi(Build.VERSION_CODES.Q)
    private fun saveWithMediaStore(
        context: Context,
        fileName: String,
        mimeType: String,
        writer: (OutputStream) -> Unit
    ): PublicDownload {
        val resolver = context.contentResolver
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, fileName)
            put(MediaStore.Downloads.MIME_TYPE, mimeType.ifBlank { "application/octet-stream" })
            put(MediaStore.Downloads.RELATIVE_PATH, "${Environment.DIRECTORY_DOWNLOADS}/")
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
            ?: throw IllegalStateException("无法创建 Download 文件")
        try {
            resolver.openOutputStream(uri, "w")?.use { stream ->
                stream.buffered(256 * 1024).use { output -> writer(output) }
            } ?: throw IllegalStateException("无法写入 Download 文件")
            resolver.update(
                uri,
                ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) },
                null,
                null
            )
            return PublicDownload(uri, queryDisplayName(context, uri).ifBlank { fileName })
        } catch (e: Exception) {
            resolver.delete(uri, null, null)
            throw e
        }
    }

    @Suppress("DEPRECATION")
    private fun saveLegacy(
        context: Context,
        fileName: String,
        mimeType: String,
        writer: (OutputStream) -> Unit
    ): PublicDownload {
        val directory = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
        if (!directory.exists() && !directory.mkdirs()) {
            throw IllegalStateException("无法创建 Download 目录")
        }
        val file = uniqueFile(directory, fileName)
        try {
            file.outputStream().buffered(256 * 1024).use(writer)
        } catch (e: Exception) {
            file.delete()
            throw e
        }
        MediaScannerConnection.scanFile(context, arrayOf(file.absolutePath), arrayOf(mimeType), null)
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", file)
        return PublicDownload(uri, file.name)
    }

    private fun uniqueFile(directory: File, fileName: String): File {
        val first = File(directory, fileName)
        if (!first.exists()) {
            return first
        }
        val extensionIndex = fileName.lastIndexOf('.').takeIf { it > 0 } ?: fileName.length
        val base = fileName.substring(0, extensionIndex)
        val extension = fileName.substring(extensionIndex)
        var number = 1
        while (true) {
            val candidate = File(directory, "$base ($number)$extension")
            if (!candidate.exists()) {
                return candidate
            }
            number++
        }
    }

    private fun queryDisplayName(context: Context, uri: Uri): String {
        return try {
            context.contentResolver.query(
                uri,
                arrayOf(MediaStore.MediaColumns.DISPLAY_NAME),
                null,
                null,
                null
            )?.use { cursor ->
                if (cursor.moveToFirst()) cursor.getString(0).orEmpty() else ""
            }.orEmpty()
        } catch (_: Exception) {
            ""
        }
    }
}
