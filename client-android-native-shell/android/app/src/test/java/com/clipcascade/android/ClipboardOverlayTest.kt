package com.clipcascade.android

import android.content.ClipboardManager
import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.os.Looper
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode
import org.robolectric.shadow.api.Shadow
import org.robolectric.shadows.ShadowWindowManagerImpl
import org.robolectric.util.ReflectionHelpers
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@LooperMode(LooperMode.Mode.PAUSED)
class ClipboardOverlayTest {
    private val controller = Robolectric.buildService(ClipCascadeBackgroundService::class.java)
    private lateinit var service: ClipCascadeBackgroundService
    private val addedWindows = mutableListOf<WindowManager.LayoutParams>()
    private val connectedEngine = TestSyncEngine()

    @Before
    fun setUp() {
        service = controller.create().get()
        val realWindowManager = service.getSystemService(WindowManager::class.java)
        // Observe the system boundary while keeping real window add/remove behavior.
        val windowManager = object : WindowManager by realWindowManager {
            override fun addView(view: View, params: ViewGroup.LayoutParams) {
                addedWindows.add(WindowManager.LayoutParams().apply {
                    copyFrom(params as WindowManager.LayoutParams)
                })
                realWindowManager.addView(view, params)
            }
        }
        ReflectionHelpers.setField(service, "windowManager", windowManager)
    }

    @After
    fun tearDown() {
        controller.destroy()
    }

    @Test
    fun receivingRemoteTextWritesClipboardWithoutCreatingAWindow() {
        service.onMessage("remote clipboard text", "text", "")
        shadowOf(Looper.getMainLooper()).idle()

        val clipboard = service.getSystemService(ClipboardManager::class.java)
        assertEquals("remote clipboard text", clipboard.primaryClip?.getItemAt(0)?.text?.toString())
        assertTrue("接收远端文本不需要抢焦点", addedWindows.isEmpty())
    }

    @Test
    fun disconnectedReadsNeverCreateAFocusWindowEvenWithCachedConnectedStatus() {
        service.getSharedPreferences("clipcascade", Context.MODE_PRIVATE).edit()
            .putString("ConnectionStatus", "connected").apply()
        repeat(20) { service.requestClipboardReadForSync() }
        shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMinutes(3))

        assertTrue("实际未连接时禁止创建读取窗口", addedWindows.isEmpty())
    }

    @Test
    fun startingEngineCannotCreateAReadWindow() {
        ReflectionHelpers.setField(service, "engineStarting", true)
        service.requestClipboardReadForSync()
        shadowOf(Looper.getMainLooper()).idle()

        assertTrue(addedWindows.isEmpty())
    }

    @Test
    fun clipboardReadActionDoesNotStartOrRetryTheEngine() {
        val prefs = service.getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
        prefs.edit().putLong("ConnectionUpdatedAt", 1L).apply()
        service.onStartCommand(Intent(service, ClipCascadeBackgroundService::class.java).apply {
            action = ClipCascadeBackgroundService.ACTION_SYNC_CLIPBOARD_NOW
        }, 0, 1)
        shadowOf(Looper.getMainLooper()).idle()

        assertEquals(1L, prefs.getLong("ConnectionUpdatedAt", 0L))
        assertTrue(addedWindows.isEmpty())
    }

    @Test
    fun readOverlayCanObtainClipboardFocusWithoutBecomingAnInputMethodTarget() {
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.requestClipboardReadForSync()
        shadowOf(Looper.getMainLooper()).idle()

        assertEquals(1, addedWindows.size)
        val params = addedWindows.single()
        assertEquals(0, params.flags and WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE)
        assertFalse("读取窗口不能成为输入法目标", WindowManager.LayoutParams.mayUseInputMethod(params.flags))
        assertEquals(
            WindowManager.LayoutParams.SOFT_INPUT_STATE_UNCHANGED,
            params.softInputMode and WindowManager.LayoutParams.SOFT_INPUT_MASK_STATE
        )
    }

    @Test
    fun remoteTextDoesNotRemoveAnOngoingReadWindow() {
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.requestClipboardReadForSync()
        shadowOf(Looper.getMainLooper()).idle()
        val windowManager = service.getSystemService(WindowManager::class.java)
        assertEquals(1, Shadow.extract<ShadowWindowManagerImpl>(windowManager).views.size)

        service.onMessage("text arriving during a read", "text", "")
        shadowOf(Looper.getMainLooper()).idle()

        assertEquals(1, addedWindows.size)
        assertEquals(1, Shadow.extract<ShadowWindowManagerImpl>(windowManager).views.size)
    }

    @Test
    fun repeatedRequestsShareOneReadWindow() {
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.requestClipboardReadForSync()
        service.requestClipboardReadForSync()
        shadowOf(Looper.getMainLooper()).idle()

        assertEquals(1, addedWindows.size)
    }

    @Test
    @Config(sdk = [28])
    fun olderAndroidReadsWithoutAnOverlay() {
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.requestClipboardReadForSync()
        shadowOf(Looper.getMainLooper()).idle()

        assertTrue(addedWindows.isEmpty())
    }

    @Test
    fun connectionLostBeforeQueuedReadRunsCannotCreateAWindow() {
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.requestClipboardReadForSync()
        connectedEngine.isConnected = false
        shadowOf(Looper.getMainLooper()).idle()
        assertTrue(addedWindows.isEmpty())
    }

    @Test
    fun rapidDisconnectAndReconnectCannotReviveAnOldQueuedFocusRequest() {
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.requestClipboardReadForSync()
        connectedEngine.isConnected = false
        service.onStatusChange("reconnecting")
        connectedEngine.isConnected = true
        shadowOf(Looper.getMainLooper()).idle()
        assertTrue(addedWindows.isEmpty())
    }

    @Test
    fun failedAndDisconnectedEngineInstancesNeverRequestFocus() {
        connectedEngine.isConnected = false
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        for (status in listOf("error", "reconnecting", "disconnected")) {
            service.onStatusChange(status)
            service.requestClipboardReadForSync()
            shadowOf(Looper.getMainLooper()).idle()
        }
        assertTrue(addedWindows.isEmpty())
    }

    @Test
    fun disconnectionCancelsAnActiveReadWindowImmediately() {
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.requestClipboardReadForSync()
        shadowOf(Looper.getMainLooper()).idle()
        assertEquals(1, addedWindows.size)

        connectedEngine.isConnected = false
        service.onStatusChange("reconnecting")
        shadowOf(Looper.getMainLooper()).idle()
        val manager = service.getSystemService(WindowManager::class.java)
        assertTrue(Shadow.extract<ShadowWindowManagerImpl>(manager).views.isEmpty())
        assertEquals(null, ReflectionHelpers.getField<Runnable?>(service, "clipboardReadTask"))
    }

    @Test
    fun pollingAlsoRemovesTheWindowIfAStatusCallbackIsDelayed() {
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.requestClipboardReadForSync()
        shadowOf(Looper.getMainLooper()).idle()
        connectedEngine.isConnected = false
        shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(50))
        val manager = service.getSystemService(WindowManager::class.java)
        assertTrue(Shadow.extract<ShadowWindowManagerImpl>(manager).views.isEmpty())
        assertEquals(null, ReflectionHelpers.getField<Runnable?>(service, "clipboardReadTask"))
    }

    @Test
    fun reconnectDoesNotReplayAnOfflineFocusRequest() {
        connectedEngine.isConnected = false
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.requestClipboardReadForSync()
        connectedEngine.isConnected = true
        service.onStatusChange("connected")
        shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(3))
        assertTrue(addedWindows.isEmpty())
    }

    @Test
    fun anActualConnectedClipboardChangeStillSendsItsText() {
        ReflectionHelpers.setField(service, "engine", connectedEngine)
        service.getSystemService(ClipboardManager::class.java)
            .setPrimaryClip(ClipData.newPlainText("test", "connected clipboard"))
        shadowOf(Looper.getMainLooper()).idle()
        assertTrue(connectedEngine.sent.await(2, TimeUnit.SECONDS))
        assertEquals("connected clipboard", connectedEngine.payload)
    }
}

private class TestSyncEngine : ClipboardSyncEngine {
    override var isConnected = true
    val sent = CountDownLatch(1)
    @Volatile var payload: String? = null
    override fun reconnectNow() { isConnected = false }
    override fun sendClipboard(payload: String, payloadType: String) {
        check(payloadType == "text")
        this.payload = payload
        sent.countDown()
    }
    override fun deviceCounts() = "2,1"
    override fun stop() { isConnected = false }
}
