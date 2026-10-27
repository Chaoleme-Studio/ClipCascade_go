package com.clipcascade.android

import android.os.Looper
import android.view.accessibility.AccessibilityEvent
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode
import org.robolectric.util.ReflectionHelpers
import java.time.Duration

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@LooperMode(LooperMode.Mode.PAUSED)
class ClipboardAccessibilityTest {
    private val controller = Robolectric.buildService(ClipCascadeAccessibilityService::class.java)
    private lateinit var service: ClipCascadeAccessibilityService
    private val application get() = shadowOf(RuntimeEnvironment.getApplication())

    @Before
    fun setUp() {
        service = controller.create().get()
        idle(1000)
    }

    @After
    fun tearDown() {
        controller.destroy()
    }

    @Test
    fun typingAndMovingTheCursorDoNotRequestClipboardFocus() {
        event(AccessibilityEvent.TYPE_VIEW_TEXT_SELECTION_CHANGED, "正在打字")
        idle(1500)
        assertNull(application.nextStartedService)
    }

    @Test
    fun clickingAnInputOrKeyboardKeyDoesNotRequestClipboardFocus() {
        for (label in listOf("请输入消息", "a", "粘贴", "复制功能怎么用", "copyright")) {
            event(AccessibilityEvent.TYPE_VIEW_CLICKED, label)
            idle(1500)
            assertNull("普通点击不应触发同步: $label", application.nextStartedService)
        }
    }

    @Test
    fun unrelatedNotificationsAndAnnouncementsDoNotRequestClipboardFocus() {
        for (type in listOf(AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED, AccessibilityEvent.TYPE_ANNOUNCEMENT)) {
            event(type, "收到新消息")
            idle(1500)
            assertNull(application.nextStartedService)
        }
    }

    @Test
    fun explicitCopyActionsStillRequestClipboardSync() {
        for (label in listOf("复制", "复制链接", "复制代码", "Copy", "Copy link", "COPY", "剪切", "Cut")) {
            event(AccessibilityEvent.TYPE_VIEW_CLICKED, label)
            idle(1500)
            assertSyncRequested()
        }
    }

    @Test
    fun copyActionCanBeIdentifiedByContentDescription() {
        event(AccessibilityEvent.TYPE_VIEW_CLICKED, description = "复制")
        idle(1500)
        assertSyncRequested()
    }

    @Test
    fun tappingAnEditableFieldContainingTheWordCopyDoesNotRequestFocus() {
        event(AccessibilityEvent.TYPE_VIEW_CLICKED, "复制", className = "android.widget.EditText")
        idle(1500)
        assertNull(application.nextStartedService)
    }

    @Test
    fun copiedNotificationsAndAnnouncementsStillRequestClipboardSync() {
        for (label in listOf("已复制到剪贴板", "已複製到剪貼簿", "Copied to clipboard", "Link copied")) {
            event(AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED, label)
            idle(1500)
            assertSyncRequested()
        }
        event(AccessibilityEvent.TYPE_ANNOUNCEMENT, "Copied")
        idle(1500)
        assertSyncRequested()
    }

    @Test
    fun copyClickAndItsDelayedToastOnlyRequestFocusOnce() {
        event(AccessibilityEvent.TYPE_VIEW_CLICKED, "复制")
        idle(350)
        assertSyncRequested()
        event(AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED, "已复制到剪贴板")
        idle(1500)
        assertNull(application.nextStartedService)
    }

    @Test
    fun aSecondExplicitCopyWithinOneSecondStillRequestsSync() {
        event(AccessibilityEvent.TYPE_VIEW_CLICKED, "Copy")
        idle(350)
        assertSyncRequested()
        event(AccessibilityEvent.TYPE_VIEW_CLICKED, "Copy")
        idle(350)
        assertSyncRequested()
    }

    @Test
    fun eventsFromOurOwnAppDoNotStartAReadLoop() {
        event(AccessibilityEvent.TYPE_VIEW_CLICKED, "复制", sourcePackage = service.packageName)
        idle(1500)
        assertNull(application.nextStartedService)
    }

    @Test
    fun connectingAccessibilityDoesNotGrabFocus() {
        ReflectionHelpers.callInstanceMethod<Any?>(service, "onServiceConnected")
        idle(1500)
        val intent = application.nextStartedService
        assertEquals(ClipCascadeBackgroundService::class.java.name, intent?.component?.className)
        assertNull(intent?.action)
        assertNull(application.nextStartedService)
    }

    @Test
    fun interruptCancelsPendingClipboardRead() {
        event(AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED, "已复制到剪贴板")
        service.onInterrupt()
        idle(1500)
        assertNull(application.nextStartedService)
    }

    private fun event(
        type: Int,
        label: String? = null,
        description: String? = null,
        sourcePackage: String = "com.example.editor",
        className: String = "android.widget.TextView"
    ) {
        val event = AccessibilityEvent.obtain(type).apply {
            packageName = sourcePackage
            this.className = className
            label?.let { text.add(it) }
            contentDescription = description
        }
        try {
            service.onAccessibilityEvent(event)
        } finally {
            event.recycle()
        }
    }

    private fun assertSyncRequested() {
        val intent = application.nextStartedService
        assertEquals(ClipCascadeBackgroundService.ACTION_SYNC_CLIPBOARD_NOW, intent?.action)
        assertEquals(ClipCascadeBackgroundService::class.java.name, intent?.component?.className)
        assertNull(application.nextStartedService)
    }

    private fun idle(millis: Long) {
        shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(millis))
    }
}
