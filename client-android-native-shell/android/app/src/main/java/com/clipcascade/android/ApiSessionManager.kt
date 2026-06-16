package com.clipcascade.android

import android.content.Context

class ApiUnauthorizedException(message: String) : Exception(message)

class ApiSessionManager(
    private val context: Context? = null,
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
            if (!forceLogin) {
                readPersistedCookie(key)?.let { cookie ->
                    cachedSessionKey = key
                    cachedCookie = cookie
                    return cookie
                }
            }
            val cookie = login(serverURL, username, password)
            cachedSessionKey = key
            cachedCookie = cookie
            persistCookie(key, cookie)
            return cookie
        }
    }

    private fun clearCookie(key: String) {
        synchronized(lock) {
            if (cachedSessionKey == key) {
                cachedSessionKey = ""
                cachedCookie = ""
            }
            clearPersistedCookie()
        }
    }

    private fun readPersistedCookie(key: String): String? {
        val prefs = context?.getSharedPreferences("clipcascade", Context.MODE_PRIVATE) ?: return null
        val savedKey = prefs.getString("ApiSessionKey", "") ?: ""
        val savedCookie = prefs.getString("ApiSessionCookie", "") ?: ""
        if (savedKey != key || savedCookie.isBlank()) {
            return null
        }
        return savedCookie
    }

    private fun persistCookie(key: String, cookie: String) {
        context?.getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
            ?.edit()
            ?.putString("ApiSessionKey", key)
            ?.putString("ApiSessionCookie", cookie)
            ?.apply()
    }

    private fun clearPersistedCookie() {
        context?.getSharedPreferences("clipcascade", Context.MODE_PRIVATE)
            ?.edit()
            ?.remove("ApiSessionKey")
            ?.remove("ApiSessionCookie")
            ?.apply()
    }
}
