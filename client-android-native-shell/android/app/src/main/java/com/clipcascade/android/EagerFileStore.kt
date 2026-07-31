package com.clipcascade.android

import android.content.Context
import android.util.Base64
import android.util.Base64InputStream
import java.io.ByteArrayInputStream
import java.io.File
import java.io.FileOutputStream
import java.util.UUID

object EagerFileStore {
    private const val DIRECTORY_NAME = "eager-files"

    @Synchronized
    fun saveBase64(context: Context, payload: String, fileName: String): File {
        val directory = directory(context)
        val safeName = safeFileName(fileName).ifBlank { "clipcascade-file" }.take(120)
        val temp = File.createTempFile("incoming-", ".part", directory)
        var written = 0L
        try {
            Base64InputStream(
                ByteArrayInputStream(payload.toByteArray(Charsets.US_ASCII)),
                Base64.DEFAULT
            ).use { input ->
                FileOutputStream(temp).buffered(64 * 1024).use { output ->
                    val buffer = ByteArray(64 * 1024)
                    while (true) {
                        val read = input.read(buffer)
                        if (read <= 0) break
                        written += read.toLong()
                        if (written > AndroidSharePolicy.MAX_EAGER_BYTES) {
                            throw IllegalArgumentException("file_eager 超过 8 MiB 协议上限")
                        }
                        output.write(buffer, 0, read)
                    }
                }
            }
            if (written <= 0L) {
                throw IllegalArgumentException("file_eager 内容为空")
            }
            val target = File(directory, "${System.currentTimeMillis()}-${UUID.randomUUID()}-$safeName")
            if (!temp.renameTo(target)) {
                temp.copyTo(target, overwrite = false)
                temp.delete()
            }
            directory.listFiles()?.filter { it != target }?.forEach { it.delete() }
            return target
        } catch (e: Exception) {
            temp.delete()
            throw e
        }
    }

    @Synchronized
    fun clear(context: Context) {
        directory(context).listFiles()?.forEach { it.delete() }
    }

    private fun directory(context: Context): File {
        return File(context.cacheDir, DIRECTORY_NAME).apply { mkdirs() }
    }
}
