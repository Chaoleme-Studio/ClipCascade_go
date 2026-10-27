package com.clipcascade.android

import android.content.ClipboardManager
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

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@LooperMode(LooperMode.Mode.PAUSED)
class ClipboardOverlayTest {
    private val controller = Robolectric.buildService(ClipCascadeBackgroundService::class.java)
    private lateinit var service: ClipCascadeBackgroundService
    private val addedWindows = mutableListOf<WindowManager.LayoutParams>()

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
    fun readOverlayCanObtainClipboardFocusWithoutBecomingAnInputMethodTarget() {
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
        service.requestClipboardReadForSync()
        service.requestClipboardReadForSync()
        shadowOf(Looper.getMainLooper()).idle()

        assertEquals(1, addedWindows.size)
    }

    @Test
    @Config(sdk = [28])
    fun olderAndroidReadsWithoutAnOverlay() {
        service.requestClipboardReadForSync()
        shadowOf(Looper.getMainLooper()).idle()

        assertTrue(addedWindows.isEmpty())
    }
}
