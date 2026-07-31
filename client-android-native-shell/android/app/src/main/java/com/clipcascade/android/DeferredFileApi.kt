package com.clipcascade.android

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.BufferedOutputStream
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.time.Instant
import java.util.UUID

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

data class DeferredFileRegistration(
    val deferredID: String,
    val fileName: String,
    val fileSize: Long,
    val mimeType: String,
    val expiresAtMillis: Long
)

data class RelayUploadResult(
    val relayID: String,
    val fileName: String,
    val fileSize: Long,
    val sha256: String,
    val mimeType: String
)

fun registerDeferredFile(
    serverURL: String,
    cookie: String,
    fileName: String,
    fileSize: Long,
    mimeType: String
): DeferredFileRegistration {
    val body = JSONObject()
        .put("file_name", fileName)
        .put("file_size", fileSize)
        .put("mime_type", mimeType)
        .toString()
    val obj = postDeferredJson(serverURL, cookie, "/api/files/deferred/register", body)
    val deferredID = obj.optString("deferred_id")
    if (deferredID.isBlank()) {
        throw IllegalStateException("register deferred file returned no id")
    }
    return DeferredFileRegistration(
        deferredID = deferredID,
        fileName = obj.optString("file_name").ifBlank { fileName },
        fileSize = obj.optLong("file_size", fileSize),
        mimeType = obj.optString("mime_type").ifBlank { mimeType },
        expiresAtMillis = parseExpiryMillis(obj.optString("expires_at"))
    )
}

fun markDeferredFileUploading(serverURL: String, cookie: String, deferredID: String, uploadedBytes: Long) {
    postDeferredJson(
        serverURL,
        cookie,
        "/api/files/deferred/$deferredID/progress",
        JSONObject().put("uploaded_bytes", uploadedBytes).toString()
    )
}

fun markDeferredFileReady(serverURL: String, cookie: String, deferredID: String, relayID: String) {
    postDeferredJson(
        serverURL,
        cookie,
        "/api/files/deferred/$deferredID/complete",
        JSONObject().put("relay_id", relayID).toString()
    )
}

fun markDeferredFileFailed(serverURL: String, cookie: String, deferredID: String, message: String) {
    postDeferredJson(
        serverURL,
        cookie,
        "/api/files/deferred/$deferredID/fail",
        JSONObject().put("error", message.take(240)).toString()
    )
}

fun uploadRelayFile(
    serverURL: String,
    cookie: String,
    file: File,
    fileName: String,
    mimeType: String,
    onProgress: ((sentBytes: Long, totalBytes: Long) -> Unit)? = null
): RelayUploadResult {
    val fileSize = file.length()
    if (fileSize <= 0L || fileSize > AndroidSharePolicy.MAX_RELAY_BYTES) {
        throw IllegalArgumentException("invalid relay file size: $fileSize")
    }
    val boundary = "ClipCascade-${UUID.randomUUID()}"
    val header = ("--$boundary\r\n" +
        "Content-Disposition: form-data; name=\"file\"; filename=\"${multipartEscape(fileName)}\"\r\n" +
        "Content-Type: $mimeType\r\n\r\n").toByteArray(Charsets.UTF_8)
    val suffix = "\r\n--$boundary--\r\n".toByteArray(Charsets.UTF_8)
    val contentLength = header.size.toLong() + fileSize + suffix.size.toLong()
    val baseURL = normalizeServerUrl(serverURL)
    val conn = (URL("${baseURL.trimEnd('/')}/api/files/upload").openConnection() as HttpURLConnection).apply {
        requestMethod = "POST"
        connectTimeout = 6000
        readTimeout = 60000
        instanceFollowRedirects = false
        doOutput = true
        setRequestProperty("Cookie", cookie)
        setRequestProperty("Content-Type", "multipart/form-data; boundary=$boundary")
        setRequestProperty("X-File-Size", fileSize.toString())
        setFixedLengthStreamingMode(contentLength)
    }
    try {
        BufferedOutputStream(conn.outputStream, 256 * 1024).use { output ->
            output.write(header)
            file.inputStream().buffered(256 * 1024).use { input ->
                val buffer = ByteArray(256 * 1024)
                var sent = 0L
                while (true) {
                    val read = input.read(buffer)
                    if (read <= 0) {
                        break
                    }
                    output.write(buffer, 0, read)
                    sent += read.toLong()
                    onProgress?.invoke(sent, fileSize)
                }
            }
            output.write(suffix)
        }
        ensureAuthorized(conn, "upload")
        if (conn.responseCode != HttpURLConnection.HTTP_OK) {
            val error = conn.errorStream?.bufferedReader()?.use { it.readText() }.orEmpty()
            throw IllegalStateException("upload failed: ${conn.responseCode} $error")
        }
        val obj = JSONObject(conn.inputStream.bufferedReader().use { it.readText() })
        return RelayUploadResult(
            relayID = obj.optString("relay_id"),
            fileName = obj.optString("file_name").ifBlank { fileName },
            fileSize = obj.optLong("file_size", fileSize),
            sha256 = obj.optString("sha256"),
            mimeType = obj.optString("mime_type").ifBlank { mimeType }
        ).also {
            if (it.relayID.isBlank()) {
                throw IllegalStateException("upload returned no relay id")
            }
        }
    } finally {
        conn.disconnect()
    }
}

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
    context: Context
): PublicDownload {
    val baseURL = normalizeServerUrl(serverURL)
    val url = if (item.downloadURL.startsWith("http")) {
        item.downloadURL
    } else {
        "${baseURL.trimEnd('/')}/${item.downloadURL.trimStart('/')}"
    }
    return downloadHttpFile(url, cookie, item.fileName, item.mimeType, context)
}

fun downloadRelayFile(
    serverURL: String,
    cookie: String,
    relayID: String,
    fileName: String,
    mimeType: String,
    context: Context
): PublicDownload {
    val baseURL = normalizeServerUrl(serverURL)
    val url = "${baseURL.trimEnd('/')}/api/files/${relayID.trim()}"
    return downloadHttpFile(url, cookie, fileName, mimeType, context)
}

private fun downloadHttpFile(
    url: String,
    cookie: String,
    fileName: String,
    mimeType: String,
    context: Context
): PublicDownload {
    val conn = (URL(url).openConnection() as HttpURLConnection).apply {
        requestMethod = "GET"
        connectTimeout = 6000
        readTimeout = 60000
        setRequestProperty("Cookie", cookie)
    }
    try {
        if (conn.responseCode == HttpURLConnection.HTTP_UNAUTHORIZED || conn.responseCode == HttpURLConnection.HTTP_FORBIDDEN) {
            throw ApiUnauthorizedException("download unauthorized: ${conn.responseCode}")
        }
        if (conn.responseCode != HttpURLConnection.HTTP_OK) {
            throw IllegalStateException("download failed: ${conn.responseCode}")
        }
        val resolvedMimeType = conn.contentType?.substringBefore(';')?.trim().orEmpty().ifBlank {
            mimeType.ifBlank { "application/octet-stream" }
        }
        return PublicDownloadStore.save(context, fileName, resolvedMimeType) { output ->
            conn.inputStream.buffered(256 * 1024).use { input ->
                input.copyTo(output, 256 * 1024)
            }
        }
    } finally {
        conn.disconnect()
    }
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

private fun postDeferredJson(serverURL: String, cookie: String, path: String, body: String): JSONObject {
    val baseURL = normalizeServerUrl(serverURL)
    val payload = body.toByteArray(Charsets.UTF_8)
    val conn = (URL("${baseURL.trimEnd('/')}$path").openConnection() as HttpURLConnection).apply {
        requestMethod = "POST"
        connectTimeout = 6000
        readTimeout = 10000
        instanceFollowRedirects = false
        doOutput = true
        setRequestProperty("Cookie", cookie)
        setRequestProperty("Content-Type", "application/json")
        setFixedLengthStreamingMode(payload.size)
    }
    try {
        conn.outputStream.use { it.write(payload) }
        ensureAuthorized(conn, path)
        if (conn.responseCode != HttpURLConnection.HTTP_OK) {
            val error = conn.errorStream?.bufferedReader()?.use { it.readText() }.orEmpty()
            throw IllegalStateException("deferred request failed: ${conn.responseCode} $error")
        }
        return JSONObject(conn.inputStream.bufferedReader().use { it.readText() })
    } finally {
        conn.disconnect()
    }
}

private fun ensureAuthorized(conn: HttpURLConnection, operation: String) {
    if (conn.responseCode == HttpURLConnection.HTTP_UNAUTHORIZED || conn.responseCode == HttpURLConnection.HTTP_FORBIDDEN) {
        throw ApiUnauthorizedException("$operation unauthorized: ${conn.responseCode}")
    }
}

private fun multipartEscape(value: String): String {
    return value.replace("\\", "\\\\").replace("\"", "\\\"").replace("\r", "_").replace("\n", "_")
}

private fun parseExpiryMillis(value: String): Long {
    return try {
        Instant.parse(value).toEpochMilli()
    } catch (_: Exception) {
        DeferredFileSourceStore.defaultExpiryMillis()
    }
}
