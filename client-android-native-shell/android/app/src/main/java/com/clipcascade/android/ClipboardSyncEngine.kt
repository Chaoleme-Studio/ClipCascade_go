package com.clipcascade.android

import bridge.bridge.Engine

/** Native engine boundary used by the Android service. */
internal interface ClipboardSyncEngine {
    val isConnected: Boolean
    fun reconnectNow()
    fun sendClipboard(payload: String, payloadType: String)
    fun deviceCounts(): String
    fun stop()
}

internal class GoClipboardSyncEngine(private val delegate: Engine) : ClipboardSyncEngine {
    override val isConnected get() = delegate.isConnected
    override fun reconnectNow() = delegate.reconnectNow()
    override fun sendClipboard(payload: String, payloadType: String) = delegate.sendClipboard(payload, payloadType)
    override fun deviceCounts(): String = delegate.deviceCounts()
    override fun stop() = delegate.stop()
}
