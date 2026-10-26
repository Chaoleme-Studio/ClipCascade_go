package com.clipcascade.android

import org.junit.Assert.assertEquals
import org.junit.Test

class AndroidSharePolicyTest {
    @Test
    fun matchesDesktopFileThresholds() {
        assertEquals(8L * 1024L * 1024L, AndroidSharePolicy.MAX_EAGER_BYTES)
        assertEquals(25L * 1024L * 1024L, AndroidSharePolicy.MAX_AUTO_RELAY_BYTES)
    }

    @Test
    fun allowsTextAndSmallImagesForDirectClipboardSync() {
        assertEquals(ShareDecision.DirectText, AndroidSharePolicy.classify("text/plain", null))
        assertEquals(ShareDecision.DirectImage, AndroidSharePolicy.classify("image/png", 1024))
        assertEquals(ShareDecision.DirectImage, AndroidSharePolicy.classify("image/jpeg", AndroidSharePolicy.MAX_AUTO_RELAY_BYTES))
    }

    @Test
    fun keepsImagesInlineAndDefersLargeFiles() {
        val oversized = AndroidSharePolicy.MAX_AUTO_RELAY_BYTES + 1

        assertEquals(ShareDecision.TooLarge, AndroidSharePolicy.classify("image/png", oversized))
        assertEquals(ShareDecision.DeferredFile, AndroidSharePolicy.classify("application/pdf", oversized, hasStream = true))
        assertEquals(ShareDecision.DeferredFile, AndroidSharePolicy.classify("video/mp4", DEFAULT_FILE_RELAY_MAX_BYTES, hasStream = true))
    }

    @Test
    fun routesFilesAboveOldLimitToServerPolicyCheck() {
        assertEquals(
            ShareDecision.DeferredFile,
            AndroidSharePolicy.classify("video/mp4", 20L * 1024L * 1024L * 1024L, hasStream = true)
        )
    }

    @Test
    fun doesNotPretendUnsupportedSmallFilesAreSynced() {
        assertEquals(ShareDecision.UnsupportedFile, AndroidSharePolicy.classify(null, null))
    }

    @Test
    fun allowsSmallFileStreamsForRelaySync() {
        assertEquals(ShareDecision.DirectFile, AndroidSharePolicy.classify("application/zip", 1024, hasStream = true))
        assertEquals(ShareDecision.DirectFile, AndroidSharePolicy.classify("application/pdf", 1024, hasStream = true))
        assertEquals(ShareDecision.DirectFile, AndroidSharePolicy.classify("text/plain", 1024, hasStream = true))
    }

    @Test
    fun detectsHistoryImageExtensionsFromMagicBytes() {
        assertEquals("png", AndroidSharePolicy.historyImageExtension(byteArrayOf(0x89.toByte(), 0x50, 0x4E, 0x47, 0, 0, 0, 0)))
        assertEquals("jpg", AndroidSharePolicy.historyImageExtension(byteArrayOf(0xFF.toByte(), 0xD8.toByte(), 0xFF.toByte())))
        assertEquals("webp", AndroidSharePolicy.historyImageExtension(byteArrayOf(0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x45, 0x42, 0x50)))
        assertEquals("gif", AndroidSharePolicy.historyImageExtension(byteArrayOf(0x47, 0x49, 0x46, 0, 0, 0)))
        assertEquals("img", AndroidSharePolicy.historyImageExtension(byteArrayOf(1, 2, 3)))
    }
}
