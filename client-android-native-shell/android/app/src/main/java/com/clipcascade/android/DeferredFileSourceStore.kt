package com.clipcascade.android

import android.content.Context
import org.json.JSONObject
import java.io.File

data class DeferredFileSource(
    val deferredID: String,
    val path: String,
    val fileName: String,
    val fileSize: Long,
    val mimeType: String,
    val expiresAtMillis: Long
)

object DeferredFileSourceStore {
    private const val PREFS_NAME = "clipcascade_deferred_sources"
    private const val DEFAULT_RETENTION_MILLIS = 24L * 60L * 60L * 1000L

    @Synchronized
    fun put(context: Context, source: DeferredFileSource) {
        val value = JSONObject()
            .put("deferred_id", source.deferredID)
            .put("path", source.path)
            .put("file_name", source.fileName)
            .put("file_size", source.fileSize)
            .put("mime_type", source.mimeType)
            .put("expires_at_millis", source.expiresAtMillis)
            .toString()
        preferences(context).edit().putString(source.deferredID, value).apply()
    }

    @Synchronized
    fun get(context: Context, deferredID: String): DeferredFileSource? {
        val raw = preferences(context).getString(deferredID, null) ?: return null
        return try {
            val obj = JSONObject(raw)
            DeferredFileSource(
                deferredID = obj.optString("deferred_id").ifBlank { deferredID },
                path = obj.optString("path"),
                fileName = obj.optString("file_name").ifBlank { "clipcascade-file" },
                fileSize = obj.optLong("file_size", 0L),
                mimeType = obj.optString("mime_type").ifBlank { "application/octet-stream" },
                expiresAtMillis = obj.optLong("expires_at_millis", 0L)
            ).takeIf { it.path.isNotBlank() && it.fileSize > 0L }
        } catch (_: Exception) {
            null
        }
    }

    @Synchronized
    fun remove(context: Context, deferredID: String, deleteFile: Boolean = true) {
        if (deleteFile) {
            get(context, deferredID)?.path?.let { File(it).delete() }
        }
        preferences(context).edit().remove(deferredID).apply()
    }

    @Synchronized
    fun cleanupExpired(context: Context, nowMillis: Long = System.currentTimeMillis()) {
        val prefs = preferences(context)
        val editor = prefs.edit()
        for ((key, rawValue) in prefs.all) {
            val raw = rawValue as? String ?: continue
            val source = try {
                val obj = JSONObject(raw)
                DeferredFileSource(
                    deferredID = obj.optString("deferred_id").ifBlank { key },
                    path = obj.optString("path"),
                    fileName = obj.optString("file_name"),
                    fileSize = obj.optLong("file_size", 0L),
                    mimeType = obj.optString("mime_type"),
                    expiresAtMillis = obj.optLong("expires_at_millis", 0L)
                )
            } catch (_: Exception) {
                null
            }
            if (source == null || source.expiresAtMillis <= nowMillis || !File(source.path).exists()) {
                source?.path?.let { File(it).delete() }
                editor.remove(key)
            }
        }
        editor.apply()
        sourceDirectory(context).listFiles()
            ?.filter { it.name.endsWith(".preparing") && nowMillis - it.lastModified() > DEFAULT_RETENTION_MILLIS }
            ?.forEach { it.delete() }
    }

    fun sourceDirectory(context: Context): File {
        return File(context.noBackupFilesDir, "deferred-sources").apply { mkdirs() }
    }

    fun defaultExpiryMillis(nowMillis: Long = System.currentTimeMillis()): Long {
        return nowMillis + DEFAULT_RETENTION_MILLIS
    }

    private fun preferences(context: Context) =
        context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
}
