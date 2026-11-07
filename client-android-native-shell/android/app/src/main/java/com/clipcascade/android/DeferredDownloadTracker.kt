package com.clipcascade.android

/** In-process transfer state, shared by successive MainActivity instances. */
internal class DeferredDownloadTracker {
    data class Result(val uri: String, val fileName: String, val mimeType: String)
    data class State(
        val bytes: Long = 0L,
        val total: Long = -1L,
        val result: Result? = null,
        val error: String? = null
    ) {
        val finished: Boolean get() = result != null || error != null
        val percent: Int? get() = if (total > 0L) {
            (bytes.toDouble() * 100.0 / total.toDouble()).toInt().coerceIn(0, 100)
        } else null
    }

    private val states = LinkedHashMap<String, State>()
    private val listeners = LinkedHashSet<() -> Unit>()
    private val lastNotifications = HashMap<String, Long>()

    @Synchronized
    fun addListener(listener: () -> Unit) { listeners.add(listener) }

    @Synchronized
    fun removeListener(listener: () -> Unit) { listeners.remove(listener) }

    @Synchronized
    fun snapshot(): Map<String, State> = states.toMap()

    fun start(id: String, total: Long): Boolean {
        val observers: List<() -> Unit>
        synchronized(this) {
            if (states[id]?.finished == false) return false
            states[id] = State(total = total)
            lastNotifications[id] = 0L
            observers = listeners.toList()
        }
        observers.forEach { it() }
        return true
    }

    fun progress(id: String, bytes: Long, total: Long) {
        val observers: List<() -> Unit>
        synchronized(this) {
            val previous = states[id] ?: return
            if (previous.finished) return
            val next = previous.copy(bytes = bytes, total = total)
            states[id] = next
            val now = System.nanoTime()
            val elapsed = now - (lastNotifications[id] ?: 0L)
            if (next.percent == previous.percent || (elapsed < 250_000_000L && next.percent != 100)) return
            lastNotifications[id] = now
            observers = listeners.toList()
        }
        observers.forEach { it() }
    }

    fun finish(id: String, result: Result? = null, error: String? = null) {
        require((result == null) != (error == null))
        val observers: List<() -> Unit>
        synchronized(this) {
            val previous = states[id] ?: return
            states[id] = previous.copy(result = result, error = error)
            lastNotifications.remove(id)
            observers = listeners.toList()
        }
        observers.forEach { it() }
    }

    @Synchronized
    fun consumeFinished(): List<State> {
        val finished = states.filterValues { it.finished }
        finished.keys.forEach { states.remove(it) }
        return finished.values.toList()
    }
}

internal val deferredDownloads = DeferredDownloadTracker()
