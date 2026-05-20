package com.clipcascade.android

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject

data class ClipboardHistoryItem(
    val text: String,
    val type: String,
    val direction: String,
    val timestamp: Long,
    val imagePath: String? = null
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
                    imagePath = item.optString("image_path").ifBlank { null }
                )
            )
        }
        return out
    }

    fun clear(context: Context) {
        val prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        prefs.edit().putString(KEY_HISTORY, "[]").apply()
    }

    private fun readJsonArray(raw: String?): JSONArray {
        return try {
            JSONArray(raw ?: "[]")
        } catch (_: Exception) {
            JSONArray()
        }
    }
}
