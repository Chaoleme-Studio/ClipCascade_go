package com.clipcascade.android

object AndroidSharePolicy {
    const val MAX_EAGER_BYTES: Long = 8L * 1024L * 1024L
    const val MAX_AUTO_RELAY_BYTES: Long = 25L * 1024L * 1024L
    const val MAX_RELAY_BYTES: Long = 2L * 1024L * 1024L * 1024L

    fun classify(mimeType: String?, sizeBytes: Long?, hasStream: Boolean = false): ShareDecision {
        val normalizedType = mimeType?.trim().orEmpty()
        if (normalizedType == "text/plain" && !hasStream) {
            return ShareDecision.DirectText
        }
        if (normalizedType.startsWith("image/")) {
            return if (sizeBytes != null && sizeBytes > MAX_AUTO_RELAY_BYTES) {
                ShareDecision.TooLarge
            } else {
                ShareDecision.DirectImage
            }
        }
        if (sizeBytes != null && sizeBytes > MAX_RELAY_BYTES) {
            return ShareDecision.TooLarge
        }
        if (!hasStream) {
            return ShareDecision.UnsupportedFile
        }
        return if (sizeBytes != null && sizeBytes > MAX_AUTO_RELAY_BYTES) {
            ShareDecision.DeferredFile
        } else {
            ShareDecision.DirectFile
        }
    }

    fun historyImageExtension(bytes: ByteArray): String {
        if (bytes.size >= 8 &&
            bytes[0] == 0x89.toByte() &&
            bytes[1] == 0x50.toByte() &&
            bytes[2] == 0x4E.toByte() &&
            bytes[3] == 0x47.toByte()
        ) {
            return "png"
        }
        if (bytes.size >= 3 &&
            bytes[0] == 0xFF.toByte() &&
            bytes[1] == 0xD8.toByte() &&
            bytes[2] == 0xFF.toByte()
        ) {
            return "jpg"
        }
        if (bytes.size >= 12 &&
            bytes[0] == 0x52.toByte() &&
            bytes[1] == 0x49.toByte() &&
            bytes[2] == 0x46.toByte() &&
            bytes[3] == 0x46.toByte() &&
            bytes[8] == 0x57.toByte() &&
            bytes[9] == 0x45.toByte() &&
            bytes[10] == 0x42.toByte() &&
            bytes[11] == 0x50.toByte()
        ) {
            return "webp"
        }
        if (bytes.size >= 6 &&
            bytes[0] == 0x47.toByte() &&
            bytes[1] == 0x49.toByte() &&
            bytes[2] == 0x46.toByte()
        ) {
            return "gif"
        }
        return "img"
    }
}

enum class ShareDecision {
    DirectText,
    DirectImage,
    DirectFile,
    DeferredFile,
    TooLarge,
    UnsupportedFile
}
