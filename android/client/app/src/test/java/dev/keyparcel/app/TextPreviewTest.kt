package dev.keyparcel.app

import java.nio.file.Files
import org.junit.Assert.*
import org.junit.Test

class TextPreviewTest {
    private fun preview(bytes: ByteArray): TextPreview {
        val file = Files.createTempFile("keyparcel-preview", ".txt").toFile()
        try { file.writeBytes(bytes); return readTextPreview(file) }
        finally { file.delete() }
    }

    @Test fun preservesExactUnicodeAndWhitespaceIncludingEmptyText() {
        for (text in listOf("", "  Keyparcel 世界 🌍\n\n", "a".repeat(64 * 1024))) {
            assertEquals(TextPreview(text, false), preview(text.toByteArray()))
        }
    }

    @Test fun truncationKeepsWholeUnicodeCharacters() {
        val prefix = "a".repeat(64 * 1024 - 1)
        val original = prefix + "🌍 and more"
        assertEquals(TextPreview(prefix, true), preview(original.toByteArray()))
    }

    @Test(expected = java.nio.charset.CharacterCodingException::class)
    fun malformedUtf8IsRejected() { preview(byteArrayOf(0xc3.toByte(), 0x28)) }
}
