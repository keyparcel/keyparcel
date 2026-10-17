package dev.keyparcel.app

import java.io.ByteArrayInputStream
import java.io.File
import java.io.InputStreamReader
import java.nio.charset.CharacterCodingException
import java.nio.charset.CodingErrorAction

const val MAX_VIEW_BYTES = 16 * 1024 * 1024
const val TOO_LARGE_TO_VIEW = "Too large to preview—save the file instead"
const val UNSUPPORTED_PREVIEW = "Can’t preview this parcel as text. Save it to open with another application."

// Created only in the delivery callback, after complete core verification.
class ParcelContent private constructor(private val bytes: ByteArray, val preview: TextPreview?) {
    val size get() = bytes.size
    fun openStream() = ByteArrayInputStream(bytes)
    fun clipboard(): ClipboardText = if (preview == null) ClipboardText(problem = UNSUPPORTED_PREVIEW)
        else ClipboardText(text = String(bytes, Charsets.UTF_8))

    companion object {
        fun readVerified(file: File, signedSize: Long): ParcelContent {
            val size = file.length()
            require(size <= MAX_VIEW_BYTES && signedSize <= MAX_VIEW_BYTES) { TOO_LARGE_TO_VIEW }
            require(size == signedSize && size >= 0) { "Verified parcel size changed" }
            val bytes = ByteArray(size.toInt())
            file.inputStream().use { input ->
                var offset = 0
                while (offset < bytes.size) {
                    val n = input.read(bytes, offset, bytes.size - offset)
                    check(n > 0) { "Verified parcel size changed" }
                    offset += n
                }
                check(input.read() == -1) { "Verified parcel size changed" }
            }
            var text = bytes.none { it == 0.toByte() }
            if (text) try {
                val decoder = Charsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT)
                InputStreamReader(ByteArrayInputStream(bytes), decoder).use { reader ->
                    val buffer = CharArray(8192)
                    while (reader.read(buffer) != -1) { /* Validate beyond the displayed prefix. */ }
                }
            } catch (_: CharacterCodingException) { text = false }
            return ParcelContent(bytes, if (text) readTextPreview(ByteArrayInputStream(bytes)) else null)
        }
    }
}
