package com.clipcascade.android

class ApiUnauthorizedException(message: String) : Exception(message)

class ApiSessionManager(
    private val normalizeServerUrl: (String) -> String,
    private val login: (serverURL: String, username: String, password: String) -> String
) {
    private val lock = Any()

    @Volatile
    private var cachedSessionKey: String = ""

    @Volatile
    private var cachedCookie: String = ""

    fun <T> withSession(
        serverURL: String,
        username: String,
        password: String,
        block: (String) -> T
    ): T {
        val key = sessionKey(serverURL, username, password)
        val cookie = getCookie(serverURL, username, password, key, forceLogin = false)
        return try {
            block(cookie)
        } catch (e: ApiUnauthorizedException) {
            clearCookie(key)
            val freshCookie = getCookie(serverURL, username, password, key, forceLogin = true)
            block(freshCookie)
        }
    }

    private fun sessionKey(serverURL: String, username: String, password: String): String {
        return "${normalizeServerUrl(serverURL)}\n$username\n$password"
    }

    private fun getCookie(
        serverURL: String,
        username: String,
        password: String,
        key: String,
        forceLogin: Boolean
    ): String {
        synchronized(lock) {
            if (!forceLogin && cachedSessionKey == key && cachedCookie.isNotBlank()) {
                return cachedCookie
            }
            val cookie = login(serverURL, username, password)
            cachedSessionKey = key
            cachedCookie = cookie
            return cookie
        }
    }

    private fun clearCookie(key: String) {
        synchronized(lock) {
            if (cachedSessionKey == key) {
                cachedSessionKey = ""
                cachedCookie = ""
            }
        }
    }
}
