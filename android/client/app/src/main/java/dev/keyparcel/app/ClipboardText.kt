package dev.keyparcel.app

import java.io.File
import java.nio.ByteBuffer
import java.nio.CharBuffer
import java.nio.charset.CharacterCodingException
import java.nio.charset.CodingErrorAction

const val MAX_CLIPBOARD_BYTES = 16 * 1024 * 1024
data class ClipboardText(val text: String? = null, val problem: String? = null)

private const val oversizedClipboard = "Text exceeds the 16 MiB clipboard limit. Copy a smaller selection."
private const val invalidClipboard = "Copying requires valid UTF-8 text without NUL bytes. Copy a valid selection."

fun clipboardText(text: String): ClipboardText {
    if ('\u0000' in text) return ClipboardText(problem = invalidClipboard)
    if (text.length > MAX_CLIPBOARD_BYTES) return ClipboardText(problem = oversizedClipboard)
    return try {
        val bytes = Charsets.UTF_8.newEncoder().onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT).encode(CharBuffer.wrap(text))
        if (bytes.remaining() > MAX_CLIPBOARD_BYTES) ClipboardText(problem = oversizedClipboard)
        else ClipboardText(text = text)
    } catch (_: CharacterCodingException) { ClipboardText(problem = invalidClipboard) }
}

fun readClipboardText(file: File): ClipboardText {
    if (file.length() > MAX_CLIPBOARD_BYTES) return ClipboardText(problem = oversizedClipboard)
    val bytes = file.inputStream().use { input ->
        val output = java.io.ByteArrayOutputStream()
        val buffer = ByteArray(8192)
        while (true) {
            val count = input.read(buffer)
            if (count < 0) break
            if (output.size() + count > MAX_CLIPBOARD_BYTES) return ClipboardText(problem = oversizedClipboard)
            output.write(buffer, 0, count)
        }
        output.toByteArray()
    }
    if (bytes.any { it == 0.toByte() }) return ClipboardText(problem = invalidClipboard)
    return try {
        val text = Charsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(bytes)).toString()
        ClipboardText(text = text)
    } catch (_: CharacterCodingException) { ClipboardText(problem = invalidClipboard) }
}
