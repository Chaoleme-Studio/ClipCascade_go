package com.clipcascade.android

import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL

data class DeferredFileItem(
    val deferredID: String,
    val fileName: String,
    val fileSizeHuman: String,
    val mimeType: String,
    val status: String,
    val statusDetail: String,
    val downloadURL: String,
    val uploadPercent: Int,
    val relayID: String
)

fun fetchDeferredFiles(serverURL: String, cookie: String): List<DeferredFileItem> {
    val baseURL = normalizeServerUrl(serverURL)
    val conn = (URL("${baseURL.trimEnd('/')}/api/files/deferred").openConnection() as HttpURLConnection).apply {
        requestMethod = "GET"
        connectTimeout = 6000
        readTimeout = 6000
        setRequestProperty("Cookie", cookie)
    }
    if (conn.responseCode == HttpURLConnection.HTTP_UNAUTHORIZED || conn.responseCode == HttpURLConnection.HTTP_FORBIDDEN) {
        throw ApiUnauthorizedException("list deferred unauthorized: ${conn.responseCode}")
    }
    if (conn.responseCode != HttpURLConnection.HTTP_OK) {
        throw IllegalStateException("list deferred failed: ${conn.responseCode}")
    }
    val raw = conn.inputStream.bufferedReader().use { it.readText() }
    val arr = JSONArray(raw)
    val out = mutableListOf<DeferredFileItem>()
    for (i in 0 until arr.length()) {
        val obj = arr.optJSONObject(i) ?: continue
        out.add(parseDeferredFile(obj))
    }
    return out
}

fun requestDeferredFile(serverURL: String, cookie: String, deferredID: String) {
    val baseURL = normalizeServerUrl(serverURL)
    val conn = (URL("${baseURL.trimEnd('/')}/api/files/deferred/$deferredID/request").openConnection() as HttpURLConnection).apply {
        requestMethod = "POST"
        connectTimeout = 6000
        readTimeout = 6000
        doOutput = true
        setRequestProperty("Cookie", cookie)
        setRequestProperty("Content-Length", "0")
    }
    conn.outputStream.use { }
    if (conn.responseCode == HttpURLConnection.HTTP_UNAUTHORIZED || conn.responseCode == HttpURLConnection.HTTP_FORBIDDEN) {
        throw ApiUnauthorizedException("request unauthorized: ${conn.responseCode}")
    }
    if (conn.responseCode != HttpURLConnection.HTTP_OK) {
        throw IllegalStateException("request failed: ${conn.responseCode}")
    }
}

fun downloadDeferredFile(
    serverURL: String,
    cookie: String,
    item: DeferredFileItem,
    destinationDir: File
): File {
    val baseURL = normalizeServerUrl(serverURL)
    val url = if (item.downloadURL.startsWith("http")) {
        item.downloadURL
    } else {
        "${baseURL.trimEnd('/')}/${item.downloadURL.trimStart('/')}"
    }
    val conn = (URL(url).openConnection() as HttpURLConnection).apply {
        requestMethod = "GET"
        connectTimeout = 6000
        readTimeout = 60000
        setRequestProperty("Cookie", cookie)
    }
    if (conn.responseCode == HttpURLConnection.HTTP_UNAUTHORIZED || conn.responseCode == HttpURLConnection.HTTP_FORBIDDEN) {
        throw ApiUnauthorizedException("download unauthorized: ${conn.responseCode}")
    }
    if (conn.responseCode != HttpURLConnection.HTTP_OK) {
        throw IllegalStateException("download failed: ${conn.responseCode}")
    }
    destinationDir.mkdirs()
    val file = File(destinationDir, safeFileName(item.fileName))
    conn.inputStream.use { input ->
        file.outputStream().use { output ->
            input.copyTo(output)
        }
    }
    return file
}

private fun parseDeferredFile(obj: JSONObject): DeferredFileItem {
    return DeferredFileItem(
        deferredID = obj.optString("deferred_id"),
        fileName = obj.optString("file_name"),
        fileSizeHuman = obj.optString("file_size_human"),
        mimeType = obj.optString("mime_type", "application/octet-stream"),
        status = obj.optString("status"),
        statusDetail = obj.optString("status_detail"),
        downloadURL = obj.optString("download_url"),
        uploadPercent = obj.optInt("upload_percent", 0),
        relayID = obj.optString("relay_id")
    )
}
