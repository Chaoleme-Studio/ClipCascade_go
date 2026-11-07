package com.clipcascade.android

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.IOException

class DeferredDownloadTest {
    @Test
    fun copiesAndReportsProgressForKnownAndUnknownSizes() {
        val source = ByteArray(600_000) { (it % 251).toByte() }
        for (total in listOf(source.size.toLong(), -1L)) {
            val output = ByteArrayOutputStream()
            val progress = mutableListOf<Pair<Long, Long>>()
            copyDownloadWithProgress(ByteArrayInputStream(source), output, total) { bytes, size ->
                progress.add(bytes to size)
            }
            assertArrayEquals(source, output.toByteArray())
            assertEquals(0L to total, progress.first())
            assertEquals(source.size.toLong() to total, progress.last())
            assertTrue(progress.zipWithNext().all { (a, b) -> b.first > a.first })
        }
    }

    @Test(expected = IOException::class)
    fun rejectsTruncatedDownloads() {
        copyDownloadWithProgress(ByteArrayInputStream(byteArrayOf(1, 2)), ByteArrayOutputStream(), 3L)
    }

    @Test
    fun keepsIndependentDownloadsAndPreventsDuplicatesAcrossObservers() {
        val tracker = DeferredDownloadTracker()
        var changes = 0
        val listener = { changes++ ; Unit }
        tracker.addListener(listener)
        assertTrue(tracker.start("first", 100))
        assertFalse(tracker.start("first", 100))
        assertTrue(tracker.start("second", -1))
        tracker.progress("first", 42, 100)
        assertEquals(42, tracker.snapshot()["first"]?.percent)
        assertNull(tracker.snapshot()["second"]?.percent)
        tracker.removeListener(listener)
        val changesBeforeCompletion = changes
        tracker.finish("first", result = DeferredDownloadTracker.Result("content://file", "file", "text/plain"))
        assertEquals(changesBeforeCompletion, changes)
        assertTrue(tracker.snapshot().getValue("first").finished)
        assertEquals("file", tracker.consumeFinished().single().result?.fileName)
        assertFalse(tracker.snapshot().containsKey("first"))
        assertFalse(tracker.snapshot().getValue("second").finished)
        tracker.finish("second", error = "offline")
        assertEquals("offline", tracker.consumeFinished().single().error)
        assertTrue(tracker.start("second", 100))
    }
}
