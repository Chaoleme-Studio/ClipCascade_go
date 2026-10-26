package com.clipcascade.android

import java.util.Locale

// Used only when talking to servers that predate the file policy API.
const val DEFAULT_FILE_RELAY_MAX_BYTES: Long = 2L * 1024L * 1024L * 1024L

data class FileRelayPolicy(val enabled: Boolean, val maxFileBytes: Long) {
    init {
        require(maxFileBytes > 0L) { "服务器返回了无效的文件大小上限" }
    }

    fun checkSize(size: Long) {
        check(enabled) { "服务器未启用文件中转" }
        require(size > 0L) { "文件为空或大小无效" }
        if (size > maxFileBytes) {
            throw FileRelayTooLargeException(maxFileBytes)
        }
    }
}

class FileRelayTooLargeException(maxBytes: Long) : IllegalArgumentException(
    "文件超过服务器上限（${formatFileRelayLimit(maxBytes)}）"
)

private fun formatFileRelayLimit(bytes: Long): String {
    val gib = 1024L * 1024L * 1024L
    val mib = 1024L * 1024L
    return when {
        bytes >= gib -> String.format(Locale.ROOT, "%.2f GiB", bytes.toDouble() / gib)
        bytes >= mib -> String.format(Locale.ROOT, "%.2f MiB", bytes.toDouble() / mib)
        else -> "$bytes B"
    }
}
