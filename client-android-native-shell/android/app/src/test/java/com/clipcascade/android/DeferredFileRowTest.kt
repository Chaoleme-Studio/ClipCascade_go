package com.clipcascade.android

import android.view.View
import android.widget.LinearLayout
import android.widget.ProgressBar
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.util.ReflectionHelpers

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class DeferredFileRowTest {
    private fun item(status: String) = DeferredFileItem(
        deferredID = "row-test",
        fileName = "sample.bin",
        fileSize = 100,
        fileSizeHuman = "100 B",
        mimeType = "application/octet-stream",
        status = status,
        statusDetail = "",
        downloadURL = "/api/files/relay",
        uploadPercent = 42,
        relayID = "relay"
    )

    private fun row(activity: MainActivity, item: DeferredFileItem): LinearLayout =
        ReflectionHelpers.callInstanceMethod(
            activity, "fileHistoryRow", ReflectionHelpers.ClassParameter.from(DeferredFileItem::class.java, item)
        )

    private fun progressBar(row: LinearLayout): ProgressBar? =
        (0 until row.childCount).map { row.getChildAt(it) }.filterIsInstance<ProgressBar>().singleOrNull()

    @Test
    fun showsUploadAndWaitingProgressButNoIdleBar() {
        val controller = Robolectric.buildActivity(MainActivity::class.java).create()
        try {
            val activity = controller.get()
            val upload = progressBar(row(activity, item("uploading")))!!
            assertFalse(upload.isIndeterminate)
            assertEquals(42, upload.progress)
            assertTrue(progressBar(row(activity, item("requested")))!!.isIndeterminate)
            assertEquals(null, progressBar(row(activity, item("ready"))))
        } finally {
            controller.destroy()
        }
    }

    @Test
    fun keepsDownloadProgressWhenRowIsRecreated() {
        val controller = Robolectric.buildActivity(MainActivity::class.java).create()
        try {
            assertTrue(deferredDownloads.start("row-test", 100))
            deferredDownloads.progress("row-test", 63, 100)
            repeat(2) {
                val progress = progressBar(row(controller.get(), item("ready")))!!
                assertFalse(progress.isIndeterminate)
                assertEquals(63, progress.progress)
                assertEquals(View.VISIBLE, progress.visibility)
            }
        } finally {
            deferredDownloads.finish("row-test", error = "test cleanup")
            deferredDownloads.consumeFinished()
            controller.destroy()
        }
    }
}
