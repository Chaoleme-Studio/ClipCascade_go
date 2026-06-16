package com.clipcascade.android

import android.os.Build
import android.content.Context
import android.content.pm.PackageManager
import java.net.URLEncoder
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

fun formatTime(timestamp: Long): String {
    return SimpleDateFormat("HH:mm:ss", Locale.getDefault()).format(Date(timestamp))
}

fun normalizeServerUrl(raw: String): String {
    val trimmed = raw.trim().trimEnd('/')
    if (trimmed.isBlank()) {
        return ""
    }
    return if (trimmed.contains("://")) trimmed else "http://$trimmed"
}

fun safeFileName(name: String): String {
    val cleaned = name.trim()
        .replace("/", "_")
        .replace("\\", "_")
        .replace("\u0000", "")
    return cleaned.ifBlank { "clipcascade-file" }
}

fun urlEncode(value: String): String {
    return URLEncoder.encode(value, "UTF-8")
}

fun appVersionName(context: Context): String {
    return try {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            context.packageManager.getPackageInfo(context.packageName, PackageManager.PackageInfoFlags.of(0)).versionName
        } else {
            @Suppress("DEPRECATION")
            context.packageManager.getPackageInfo(context.packageName, 0).versionName
        } ?: "unknown"
    } catch (_: Exception) {
        "unknown"
    }
}
