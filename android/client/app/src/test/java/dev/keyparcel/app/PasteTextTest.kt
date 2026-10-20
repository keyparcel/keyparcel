package dev.keyparcel.app

import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import org.junit.Assert.assertEquals
import org.junit.Test

class PasteTextTest {
    @Test fun insertsAtCursorAndReplacesSelection() {
        for (selection in listOf(TextRange(1, 3), TextRange(3, 1))) {
            val result = insertClipboardText(TextFieldValue("abcd", selection), "世界\n")
            assertEquals("a世界\nd", result.text)
            assertEquals(TextRange(4), result.selection)
        }
        assertEquals("abXcd", insertClipboardText(TextFieldValue("abcd", TextRange(2)), "X").text)
        assertEquals("fresh", insertClipboardText(TextFieldValue(""), "fresh").text)
        val unchanged = TextFieldValue("draft", TextRange(1, 3))
        assertEquals(unchanged, insertClipboardText(unchanged, ""))
    }
}
