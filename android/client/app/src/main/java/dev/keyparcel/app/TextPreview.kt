package dev.keyparcel.app

import java.io.File
import java.io.InputStreamReader
import java.nio.charset.CodingErrorAction

data class TextPreview(val text: String, val truncated: Boolean)

// Bound UI layout/memory, while the core verifies and retains the complete transfer.
fun readTextPreview(file: File): TextPreview {
    val limit = 64 * 1024
    val chars = CharArray(limit + 1)
    var count = 0
    val decoder = Charsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
        .onUnmappableCharacter(CodingErrorAction.REPORT)
    InputStreamReader(file.inputStream(), decoder).use { reader ->
        while (count < chars.size) {
            val read = reader.read(chars, count, chars.size - count)
            if (read < 0) break
            count += read
        }
    }
    var end = minOf(count, limit)
    if (count > limit && chars[end - 1].isHighSurrogate()) end--
    return TextPreview(String(chars, 0, end), count > limit)
}
