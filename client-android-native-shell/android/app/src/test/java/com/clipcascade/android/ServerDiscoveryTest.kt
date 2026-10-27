package com.clipcascade.android

import android.Manifest
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.LinkProperties
import android.net.Network
import android.os.Looper
import android.widget.EditText
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.Implementation
import org.robolectric.annotation.Implements
import org.robolectric.annotation.LooperMode
import org.robolectric.shadow.api.Shadow
import org.robolectric.shadows.ShadowPopupMenu
import org.robolectric.shadows.ShadowNetwork
import org.robolectric.util.ReflectionHelpers
import java.net.InetAddress
import java.time.Duration
import java.util.concurrent.Executor

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], shadows = [RecordingNsdManager::class])
@LooperMode(LooperMode.Mode.PAUSED)
class ServerDiscoveryTest {
    private val controller = Robolectric.buildActivity(MainActivity::class.java)
    private lateinit var activity: MainActivity
    private lateinit var nsd: RecordingNsdManager

    @Before
    fun setUp() {
        shadowOf(RuntimeEnvironment.getApplication()).grantPermissions(
            Manifest.permission.POST_NOTIFICATIONS,
            "com.clipcascade.android.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION"
        )
        controller.create().start()
        activity = controller.get()
        nsd = Shadow.extract(activity.getSystemService(NsdManager::class.java))
        controller.resume().visible()
        idle()
    }

    @After
    fun tearDown() {
        controller.pause().stop().destroy()
    }

    @Test
    fun aFiveSecondRoundActuallyStopsSystemDiscovery() {
        assertEquals(1, nsd.starts.size)
        idle(5000)
        assertEquals(1, nsd.stops.size)
        assertTrue(nsd.active.isEmpty())
    }

    @Test
    fun noResultsStopAfterThreeRoundsWithIncreasingDelays() {
        idle(5000)
        assertEquals(1, nsd.starts.size)
        idle(9999)
        assertEquals(1, nsd.starts.size)
        idle(1)
        assertEquals(2, nsd.starts.size)
        idle(5000)
        idle(29999)
        assertEquals(2, nsd.starts.size)
        idle(1)
        assertEquals(3, nsd.starts.size)
        idle(5000)
        idle(3_600_000)
        assertEquals(3, nsd.starts.size)
        assertEquals(3, nsd.stops.size)
        assertTrue(nsd.active.isEmpty())
    }

    @Test
    fun returningToTheSamePageDoesNotRestartAnExhaustedBatch() {
        idle(60_000)
        val count = nsd.starts.size
        controller.pause().stop().start().resume().visible()
        idle(60_000)
        assertEquals(count, nsd.starts.size)
    }

    @Test
    fun duplicateNetworkCallbacksCannotResetTheRoundBudget() {
        val network = ShadowNetwork.newInstance(123)
        val properties = LinkProperties().apply {
            interfaceName = "wlan0"
        }
        networkChanged(network, properties)
        idle(60_000)
        val count = nsd.starts.size
        repeat(10) { networkChanged(network, properties) }
        idle(60_000)
        assertEquals(count, nsd.starts.size)
        assertTrue(nsd.active.isEmpty())
    }

    @Test
    fun actualNetworkChangeAllowsOneNewBoundedBatch() {
        idle(60_000)
        val count = nsd.starts.size
        networkChanged(ShadowNetwork.newInstance(456), LinkProperties().apply {
            interfaceName = "wlan0"
        })
        idle(60_000)
        assertEquals(count + 3, nsd.starts.size)
        assertTrue(nsd.active.isEmpty())
    }

    @Test
    fun leavingThePageCancelsDelayedRounds() {
        idle(5000)
        controller.pause().stop()
        idle(60_000)
        assertEquals(1, nsd.starts.size)
        assertTrue(nsd.active.isEmpty())
        controller.start().resume().visible()
    }

    @Test
    fun aLateStopAcknowledgmentCannotRestartACanceledBatchAfterReturning() {
        nsd.deferStop = true
        val listener = nsd.starts.single()
        controller.pause().stop().start().resume().visible()
        nsd.completeStop(listener)
        idle(60_000)
        assertEquals(1, nsd.starts.size)
        assertTrue(nsd.active.isEmpty())
    }

    @Test
    fun stopFailureNeverStartsAnotherOverlappingSearch() {
        nsd.failStop = true
        idle(60_000)
        assertEquals(1, nsd.starts.size)
        assertEquals(1, nsd.stops.size)
    }

    @Test
    fun repeatedManualClicksDoNotRestartAnActiveRound() {
        val button = ReflectionHelpers.getField<android.widget.Button>(activity, "discoverButton")
        repeat(10) { button.performClick() }
        idle(5000)
        assertEquals(1, nsd.starts.size)
        assertEquals(1, nsd.stops.size)
    }

    @Test
    fun manualClickAfterExhaustionStartsANewBatch() {
        idle(60_000)
        val count = nsd.starts.size
        ReflectionHelpers.getField<android.widget.Button>(activity, "discoverButton").performClick()
        idle()
        assertEquals(count + 1, nsd.starts.size)
    }

    @Test
    fun asynchronousResultsNeverFillTheInputOrOpenAMenu() {
        val input = ReflectionHelpers.getField<EditText>(activity, "serverAddressInput")
        input.setText("")
        input.requestFocus()
        ReflectionHelpers.getField<android.widget.Button>(activity, "discoverButton").performLongClick()
        idle(500)
        val service = serviceInfo()
        nsd.starts.last().onServiceFound(service)
        idle()
        nsd.updates.last().onServiceUpdated(service)
        idle()

        assertEquals("", input.text.toString())
        assertTrue(input.hasFocus())
        assertNull(ShadowPopupMenu.getLatestPopupMenu())
        idle(60_000)
        assertTrue(nsd.active.isEmpty())
        assertEquals(1, nsd.starts.size)
    }

    @Test
    fun timeoutUnregistersServiceCallbacksAndIgnoresLateUpdates() {
        nsd.starts.last().onServiceFound(serviceInfo())
        idle()
        val callback = nsd.updates.single()
        idle(5000)
        assertEquals(1, nsd.unregistered.size)
        callback.onServiceUpdated(serviceInfo())
        idle()
        val results = ReflectionHelpers.getField<Set<String>>(activity, "discoveredServers")
        assertTrue(results.isEmpty())
    }

    @Test
    @Config(sdk = [28])
    fun oldAndroidIgnoresResolutionResultsAfterTheRoundHasStopped() {
        nsd.starts.last().onServiceFound(NsdServiceInfo().apply {
            serviceName = "legacy-server"
            serviceType = "_clipcascade._tcp."
        })
        idle()
        val resolve = nsd.resolutions.single()
        idle(5000)
        resolve.onServiceResolved(NsdServiceInfo().apply {
            serviceName = "legacy-server"
            host = InetAddress.getByName("192.168.1.20")
            port = 8080
        })
        idle()
        assertTrue(ReflectionHelpers.getField<Set<String>>(activity, "discoveredServers").isEmpty())
        assertTrue(nsd.active.isEmpty())
    }

    @Test
    fun systemDiscoveryStartFailuresStillHaveAFiniteRetryBudget() {
        idle(60_000)
        val count = nsd.starts.size
        nsd.failStart = true
        ReflectionHelpers.getField<android.widget.Button>(activity, "discoverButton").performClick()
        idle(120_000)
        assertEquals(count + 3, nsd.starts.size)
        assertTrue(nsd.active.isEmpty())
    }

    private fun networkChanged(network: Network, properties: LinkProperties) {
        ReflectionHelpers.callInstanceMethod<Any?>(activity, "handleNetworkChanged",
            ReflectionHelpers.ClassParameter.from(Network::class.java, network),
            ReflectionHelpers.ClassParameter.from(LinkProperties::class.java, properties))
    }

    private fun serviceInfo() = NsdServiceInfo().apply {
        serviceName = "test-clipcascade"
        serviceType = "_clipcascade._tcp."
        port = 8080
        hostAddresses = listOf(InetAddress.getByName("192.168.1.20"))
    }

    private fun idle(millis: Long = 0) {
        shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(millis))
    }
}

// Only replace the Android NSD boundary; execute the real Activity and scheduled callbacks.
@Implements(NsdManager::class)
class RecordingNsdManager {
    val starts = mutableListOf<NsdManager.DiscoveryListener>()
    val stops = mutableListOf<NsdManager.DiscoveryListener>()
    val active = mutableSetOf<NsdManager.DiscoveryListener>()
    val updates = mutableListOf<NsdManager.ServiceInfoCallback>()
    val unregistered = mutableListOf<NsdManager.ServiceInfoCallback>()
    val resolutions = mutableListOf<NsdManager.ResolveListener>()
    var failStop = false
    var failStart = false
    var deferStop = false

    @Implementation(maxSdk = 32)
    fun init() {}

    @Implementation
    fun discoverServices(type: String, protocol: Int, listener: NsdManager.DiscoveryListener) {
        check(type == "_clipcascade._tcp." && protocol == NsdManager.PROTOCOL_DNS_SD)
        starts.add(listener)
        if (failStart) {
            listener.onStartDiscoveryFailed(type, NsdManager.FAILURE_INTERNAL_ERROR)
            return
        }
        active.add(listener)
        listener.onDiscoveryStarted(type)
    }

    @Implementation
    fun stopServiceDiscovery(listener: NsdManager.DiscoveryListener) {
        stops.add(listener)
        if (failStop) {
            listener.onStopDiscoveryFailed("_clipcascade._tcp.", NsdManager.FAILURE_INTERNAL_ERROR)
        } else if (!deferStop) {
            completeStop(listener)
        }
    }

    fun completeStop(listener: NsdManager.DiscoveryListener) {
        active.remove(listener)
        listener.onDiscoveryStopped("_clipcascade._tcp.")
    }

    @Implementation(minSdk = 34)
    @Suppress("UNUSED_PARAMETER")
    fun registerServiceInfoCallback(service: NsdServiceInfo, executor: Executor, callback: NsdManager.ServiceInfoCallback) {
        check(service.serviceType == "_clipcascade._tcp.")
        updates.add(callback)
    }

    @Implementation
    fun resolveService(service: NsdServiceInfo, listener: NsdManager.ResolveListener) {
        check(service.serviceType == "_clipcascade._tcp.")
        resolutions.add(listener)
    }

    @Implementation(minSdk = 34)
    fun unregisterServiceInfoCallback(callback: NsdManager.ServiceInfoCallback) {
        unregistered.add(callback)
    }
}
