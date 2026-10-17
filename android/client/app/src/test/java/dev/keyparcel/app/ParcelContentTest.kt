package dev.keyparcel.app

import java.io.File
import org.junit.Assert.*
import org.junit.Test

class ParcelContentTest {
    private fun read(bytes: ByteArray, size: Long = bytes.size.toLong()): ParcelContent {
        val file = File.createTempFile("parcel", ".bin")
        try { file.writeBytes(bytes); return ParcelContent.readVerified(file,size) }
        finally { file.delete() }
    }
    @Test fun retainsOpaqueBytesAndValidatesBeyondPreview() {
        for (bytes in listOf(byteArrayOf(0,1,2,-1), "hello\u0000world".toByteArray(),
            "x".repeat(65_536).toByteArray()+byteArrayOf(-1), "x".repeat(65_536).toByteArray()+byteArrayOf(0))) {
            val content = read(bytes)
            assertNull(content.preview)
            assertNull(content.clipboard().text)
            assertArrayEquals(bytes,content.openStream().readBytes())
        }
        val text = " 🌍 世界\n".repeat(20000)
        val content = read(text.toByteArray())
        val preview = requireNotNull(content.preview)
        assertTrue(preview.truncated)
        assertTrue(preview.text.length <= 65_536)
        assertEquals(text,content.clipboard().text)
        assertArrayEquals(text.toByteArray(),content.openStream().readBytes())
    }
    @Test fun inclusiveCapChecksActualAndSignedSize() {
        val bytes = ByteArray(MAX_VIEW_BYTES) { 120 }
        assertEquals(MAX_VIEW_BYTES,read(bytes).size)
        assertThrows(IllegalArgumentException::class.java) { read(bytes+byteArrayOf(1)) }
        assertThrows(IllegalArgumentException::class.java) { read(bytes+byteArrayOf(1),1) }
        assertThrows(IllegalArgumentException::class.java) { read(byteArrayOf(1),MAX_VIEW_BYTES+1L) }
        assertThrows(IllegalArgumentException::class.java) { read(byteArrayOf(1),0) }
    }
    @Test fun saveContextRetainsOriginAndRejectsChanges() {
        val item = InboxItem("id","bob","file","binary.bin",3,"")
        val settings = Settings(Host("server",22,"user","host-key"),mapOf("bob" to "peer"))
        val view = ViewedText(item,read(byteArrayOf(0,1,2)),settings,"identity")
        val save = FileSaveRequest(item,settings,"identity",view)
        assertSame(view,save.view)
        assertEquals(Page.Text,save.parent)
        assertEquals(Page.Inbox,save.copy(view=null).parent)
        assertEquals("binary.bin",save.filename)
        assertTrue(save.matches(settings,"identity"))
        assertFalse(save.matches(settings.copy(peers=emptyMap()),"identity"))
        assertFalse(save.matches(settings,"replacement"))
        assertFalse(save.matches(settings.copy(host=settings.host!!.copy(address="other")),"identity"))
        assertEquals("id.txt",FileSaveRequest(item.copy(type="text",filename=""),settings,"identity").filename)
    }
}
