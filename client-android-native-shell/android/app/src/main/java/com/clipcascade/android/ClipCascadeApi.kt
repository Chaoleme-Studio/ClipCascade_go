package com.clipcascade.android

import java.net.HttpURLConnection
import java.net.URL

fun loginForApi(serverURL: String, username: String, password: String): String {
    val baseURL = normalizeServerUrl(serverURL)
    val body = "username=${urlEncode(username)}&password=${urlEncode(password)}"
    val conn = (URL("${baseURL.trimEnd('/')}/login").openConnection() as HttpURLConnection).apply {
        requestMethod = "POST"
        instanceFollowRedirects = false
        connectTimeout = 6000
        readTimeout = 6000
        doOutput = true
        setRequestProperty("Content-Type", "application/x-www-form-urlencoded")
    }
    conn.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
    val status = conn.responseCode
    if (status !in 300..399 && status != HttpURLConnection.HTTP_OK) {
        throw IllegalStateException("login failed: $status")
    }
    return conn.headerFields["Set-Cookie"]
        ?.joinToString("; ") { it.substringBefore(";") }
        .orEmpty()
        .also {
            if (it.isBlank()) {
                throw IllegalStateException("login did not return session cookie")
            }
        }
}
