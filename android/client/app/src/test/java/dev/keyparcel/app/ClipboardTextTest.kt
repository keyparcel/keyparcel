package dev.keyparcel.app

import java.io.File
import java.io.RandomAccessFile
import java.nio.file.Files
import org.junit.Assert.*
import org.junit.Test

class ClipboardTextTest {
    private fun withFile(bytes: ByteArray, check: (File) -> Unit) {
        val file = Files.createTempFile("keyparcel-clipboard", ".txt").toFile()
        try { file.writeBytes(bytes); check(file) }
        finally { file.delete() }
    }

    @Test fun completeTextPreservesUnicodeWhitespaceAndThePreviewTail() {
        for (text in listOf("", "  世界 🌍\n\n", "prefix\n" + "α".repeat(70_000) + "\ncomplete-tail")) {
            withFile(text.toByteArray()) { file ->
                assertEquals(ClipboardText(text = text), readClipboardText(file))
                assertEquals(ClipboardText(text = text), clipboardText(text))
                if (text.length > 65_536) {
                    assertTrue(readTextPreview(file).truncated)
                    assertFalse(readTextPreview(file).text.contains("complete-tail"))
                }
            }
        }
    }

    @Test fun clipboardCapCountsUtf8BytesAndIncludesTheBoundary() {
        val boundary = "🌍".repeat(MAX_CLIPBOARD_BYTES / 4)
        assertEquals(boundary, clipboardText(boundary).text)
        assertTrue(clipboardText(boundary + "a").problem!!.contains("16 MiB"))
        withFile(boundary.toByteArray()) { assertEquals(boundary, readClipboardText(it).text) }
        val file = Files.createTempFile("keyparcel-oversized-clipboard", ".txt").toFile()
        try {
            RandomAccessFile(file, "rw").use { it.setLength(MAX_CLIPBOARD_BYTES.toLong() + 1) }
            assertTrue(readClipboardText(file).problem!!.contains("16 MiB"))
        } finally { file.delete() }
    }

    @Test fun nulMalformedUtf8AndSplitSurrogatesCannotBeCopied() {
        for (text in listOf("a\u0000b", "\uD800", "\uDC00")) {
            assertNull(clipboardText(text).text)
            assertTrue(clipboardText(text).problem!!.contains("UTF-8"))
        }
        for (bytes in listOf(byteArrayOf(0xC0.toByte()), "a\u0000b".toByteArray(),
            "a".repeat(200_000).toByteArray() + byteArrayOf(0x80.toByte()))) {
            withFile(bytes) { assertNull(readClipboardText(it).text) }
        }
        assertNotNull(clipboardText("\uFFFD").text)
    }
}
