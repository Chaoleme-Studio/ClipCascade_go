package com.clipcascade.android

import org.junit.Assert.assertTrue
import org.junit.Test

class FileRelayPolicyTest {
    @Test
    fun acceptsConfiguredLimitAboveTwoGiB() {
        val max = 20L * 1024L * 1024L * 1024L
        FileRelayPolicy(true, max).checkSize(max)
    }

    @Test(expected = FileRelayTooLargeException::class)
    fun defaultRejectsFilesAboveTwoGiB() {
        FileRelayPolicy(true, DEFAULT_FILE_RELAY_MAX_BYTES).checkSize(DEFAULT_FILE_RELAY_MAX_BYTES + 1)
    }

    @Test
    fun rejectionReportsActualServerLimit() {
        try {
            FileRelayPolicy(true, 10L * 1024L * 1024L).checkSize(11L * 1024L * 1024L)
            throw AssertionError("expected rejection")
        } catch (e: FileRelayTooLargeException) {
            assertTrue(e.message.orEmpty().contains("10.00 MiB"))
        }
    }

    @Test(expected = IllegalStateException::class)
    fun rejectsDisabledRelay() {
        FileRelayPolicy(false, DEFAULT_FILE_RELAY_MAX_BYTES).checkSize(1)
    }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsInvalidServerLimit() {
        FileRelayPolicy(true, 0)
    }
}
