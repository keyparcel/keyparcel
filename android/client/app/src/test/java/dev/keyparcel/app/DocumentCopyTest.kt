package dev.keyparcel.app

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.security.MessageDigest
import org.junit.Assert.*
import org.junit.Test

class DocumentCopyTest {
    @Test fun emptyAndLargeBinaryCopiesUseBoundedReads() {
        for (size in listOf(0, 17 * 1024 * 1024 + 31)) {
            var position = 0
            val expected = MessageDigest.getInstance("SHA-256")
            val actual = MessageDigest.getInstance("SHA-256")
            val input = object : InputStream() {
                override fun read(): Int = error("Use buffered reads")
                override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
                    assertTrue("Unbounded read buffer", length <= 64 * 1024)
                    if (position == size) return -1
                    val count = minOf(length, size - position)
                    for (i in 0 until count) buffer[offset + i] = ((position + i) % 251).toByte()
                    expected.update(buffer, offset, count)
                    position += count
                    return count
                }
            }
            val output = object : OutputStream() {
                override fun write(value: Int) = error("Use buffered writes")
                override fun write(buffer: ByteArray, offset: Int, length: Int) { actual.update(buffer, offset, length) }
            }
            assertEquals(size.toLong(), copyDocumentContent(input, output) {})
            assertArrayEquals(expected.digest(), actual.digest())
        }
    }

    @Test fun declaredDocumentLengthDoesNotCopyTrailingBytes() {
        for (length in listOf(0L, 3L)) {
            val output = ByteArrayOutputStream()
            assertEquals(length, copyDocumentContent(ByteArrayInputStream(byteArrayOf(1, 2, 3, 4)), output, length) {})
            assertArrayEquals(byteArrayOf(1, 2, 3).take(length.toInt()).toByteArray(), output.toByteArray())
        }
    }

    @Test fun failuresAndCancellationDoNotReportSuccess() {
        val badSource = object : InputStream() { override fun read(): Int = throw IOException("read failed") }
        val badDestination = object : OutputStream() { override fun write(value: Int) = throw IOException("write failed") }
        assertThrows(IOException::class.java) { copyDocumentContent(badSource, ByteArrayOutputStream()) {} }
        assertThrows(IOException::class.java) { copyDocumentContent(ByteArrayInputStream(byteArrayOf(0, -1)), badDestination) {} }
        val output = ByteArrayOutputStream()
        assertThrows(IllegalStateException::class.java) {
            copyDocumentContent(ByteArrayInputStream(byteArrayOf(1)), output) { error("canceled") }
        }
        assertEquals(0, output.size())
    }
}
