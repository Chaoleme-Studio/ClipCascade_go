package com.clipcascade.android

import org.junit.Assert.assertEquals
import org.junit.Test

class ApiSessionManagerTest {
    @Test
    fun reusesCookieForRepeatedRequestsWithSameAccount() {
        var loginCount = 0
        val manager = ApiSessionManager(
            normalizeServerUrl = { it.trimEnd('/') },
            login = { _, _, _ ->
                loginCount += 1
                "session=$loginCount"
            }
        )

        val first = manager.withSession("http://server/", "admin", "pass") { it }
        val second = manager.withSession("http://server", "admin", "pass") { it }

        assertEquals("session=1", first)
        assertEquals("session=1", second)
        assertEquals(1, loginCount)
    }

    @Test
    fun logsInAgainAfterUnauthorizedResponse() {
        var loginCount = 0
        var requestCount = 0
        val manager = ApiSessionManager(
            normalizeServerUrl = { it },
            login = { _, _, _ ->
                loginCount += 1
                "session=$loginCount"
            }
        )

        val result = manager.withSession("http://server", "admin", "pass") { cookie ->
            requestCount += 1
            if (requestCount == 1) {
                throw ApiUnauthorizedException("expired")
            }
            cookie
        }

        assertEquals("session=2", result)
        assertEquals(2, loginCount)
        assertEquals(2, requestCount)
    }

    @Test
    fun logsInAgainWhenCredentialsChange() {
        var loginCount = 0
        val manager = ApiSessionManager(
            normalizeServerUrl = { it },
            login = { _, username, _ ->
                loginCount += 1
                "session=$username-$loginCount"
            }
        )

        val admin = manager.withSession("http://server", "admin", "pass") { it }
        val other = manager.withSession("http://server", "other", "pass") { it }

        assertEquals("session=admin-1", admin)
        assertEquals("session=other-2", other)
        assertEquals(2, loginCount)
    }
}
