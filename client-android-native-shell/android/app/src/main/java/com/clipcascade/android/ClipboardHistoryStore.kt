package com.clipcascade.android

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject

data class ClipboardHistoryItem(
    val text: String,
    val type: String,
    val direction: String,
    val timestamp: Long,
    val imagePath: String? = null,
    val fileName: String? = null,
    val relayID: String? = null,
    val localFilePath: String? = null,
    val fileSize: Long = 0L,
    val mimeType: String? = null
)

object ClipboardHistoryStore {
    private const val PREFS_NAME = "clipcascade"
    private const val KEY_HISTORY = "ClipboardHistoryJson"
    private const val MAX_ITEMS = 50

    fun append(context: Context, text: String, direction: String) {
        val normalized = text.trim().replace("\n", " ")
        if (normalized.isBlank()) {
            return
        }

        val item = JSONObject()
            .put("text", normalized.take(500))
            .put("type", "text")
            .put("direction", direction)
            .put("timestamp", System.currentTimeMillis())

        appendItem(context, item)
    }

    fun appendImage(context: Context, imagePath: String, direction: String) {
        val normalized = imagePath.trim()
        if (normalized.isBlank()) {
            return
        }
        val item = JSONObject()
            .put("text", "图片")
            .put("type", "image")
            .put("image_path", normalized)
            .put("direction", direction)
            .put("timestamp", System.currentTimeMillis())

        appendItem(context, item)
    }

    fun appendFile(
        context: Context,
        relayID: String,
        fileName: String,
        fileSize: Long,
        mimeType: String,
        direction: String
    ) {
        val normalizedID = relayID.trim()
        val normalizedName = safeFileName(fileName)
        if (normalizedID.isBlank() || normalizedName.isBlank()) {
            return
        }
        val item = JSONObject()
            .put("text", normalizedName)
            .put("type", "file")
            .put("relay_id", normalizedID)
            .put("file_name", normalizedName)
            .put("file_size", fileSize.coerceAtLeast(0L))
            .put("mime_type", mimeType.ifBlank { "application/octet-stream" })
            .put("direction", direction)
            .put("timestamp", System.currentTimeMillis())

        appendItem(context, item)
    }

    @Synchronized
    fun replaceEagerFile(
        context: Context,
        localFilePath: String,
        fileName: String,
        fileSize: Long,
        mimeType: String,
        direction: String
    ) {
        val normalizedPath = localFilePath.trim()
        val normalizedName = safeFileName(fileName)
        if (normalizedPath.isBlank() || normalizedName.isBlank()) return

        val item = JSONObject()
            .put("text", normalizedName)
            .put("type", "file")
            .put("local_file_path", normalizedPath)
            .put("file_name", normalizedName)
            .put("file_size", fileSize.coerceAtLeast(0L))
            .put("mime_type", mimeType.ifBlank { "application/octet-stream" })
            .put("direction", direction)
            .put("timestamp", System.currentTimeMillis())

        val prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val current = readJsonArray(prefs.getString(KEY_HISTORY, "[]"))
        val next = JSONArray().put(item)
        for (i in 0 until current.length()) {
            if (next.length() >= MAX_ITEMS) break
            val previous = current.optJSONObject(i) ?: continue
            if (previous.optString("local_file_path").isNotBlank()) continue
            next.put(previous)
        }
        prefs.edit().putString(KEY_HISTORY, next.toString()).apply()
    }

    @Synchronized
    private fun appendItem(context: Context, item: JSONObject) {
        val prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val arr = readJsonArray(prefs.getString(KEY_HISTORY, "[]"))
        val type = item.optString("type", "text")
        val text = item.optString("text")
        val direction = item.optString("direction")

        // Avoid duplicate consecutive records.
        if (arr.length() > 0) {
            val last = arr.optJSONObject(0)
            if (last != null &&
                last.optString("type", "text") == type &&
                last.optString("text") == text &&
                last.optString("image_path") == item.optString("image_path") &&
                last.optString("relay_id") == item.optString("relay_id") &&
                last.optString("direction") == direction
            ) {
                return
            }
        }

        val next = JSONArray().put(item)
        for (i in 0 until arr.length()) {
            if (next.length() >= MAX_ITEMS) {
                break
            }
            next.put(arr.optJSONObject(i))
        }
        prefs.edit().putString(KEY_HISTORY, next.toString()).apply()
    }

    fun list(context: Context, limit: Int = 20): List<ClipboardHistoryItem> {
        val prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val arr = readJsonArray(prefs.getString(KEY_HISTORY, "[]"))
        val out = mutableListOf<ClipboardHistoryItem>()
        for (i in 0 until arr.length()) {
            if (out.size >= limit) {
                break
            }
            val item = arr.optJSONObject(i) ?: continue
            val text = item.optString("text")
            if (text.isBlank()) {
                continue
            }
            out.add(
                ClipboardHistoryItem(
                    text = text,
                    type = item.optString("type", "text"),
                    direction = item.optString("direction", "sent"),
                    timestamp = item.optLong("timestamp", 0L),
                    imagePath = item.optString("image_path").ifBlank { null },
                    fileName = item.optString("file_name").ifBlank { null },
                    relayID = item.optString("relay_id").ifBlank { null },
                    localFilePath = item.optString("local_file_path").ifBlank { null },
                    fileSize = item.optLong("file_size", 0L),
                    mimeType = item.optString("mime_type").ifBlank { null }
                )
            )
        }
        return out
    }

    fun clear(context: Context) {
        val prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        prefs.edit().putString(KEY_HISTORY, "[]").apply()
        EagerFileStore.clear(context)
    }

    private fun readJsonArray(raw: String?): JSONArray {
        return try {
            JSONArray(raw ?: "[]")
        } catch (_: Exception) {
            JSONArray()
        }
    }
}
