package dev.keyparcel.app

import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp

internal fun insertClipboardText(value: TextFieldValue, text: String): TextFieldValue {
    if (text.isEmpty()) return value
    val start = value.selection.min
    val end = value.selection.max
    return TextFieldValue(value.text.replaceRange(start, end, text), TextRange(start + text.length))
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun MessageEditor(text: String, enabled: Boolean, change: (String) -> Unit, reportError: (String?) -> Unit) {
    var editing by rememberSaveable(stateSaver = TextFieldValue.Saver) { mutableStateOf(TextFieldValue(text)) }
    // External draft resets must not retain a selection from the previous text.
    val value = if (editing.text == text) editing else TextFieldValue(text)
    val focus = remember { FocusRequester() }
    val context = LocalContext.current
    Box {
        OutlinedTextField(value = value, onValueChange = { editing = it; change(it.text) }, enabled = enabled,
            label = { Text("Message") }, minLines = 4,
            trailingIcon = { Spacer(Modifier.width(48.dp)) },
            modifier = Modifier.fillMaxWidth().testTag("message").focusRequester(focus))
        Box(Modifier.align(Alignment.TopEnd).padding(top = 8.dp)) {
            TooltipBox(positionProvider = TooltipDefaults.rememberPlainTooltipPositionProvider(),
                tooltip = { PlainTooltip { Text("Paste from clipboard") } }, state = rememberTooltipState()) {
                IconButton(enabled = enabled, modifier = Modifier.testTag("paste-text"), onClick = {
                    try {
                        val clipboard = context.getSystemService(ClipboardManager::class.java)
                            ?: error("Clipboard service is unavailable")
                        // Read only on this explicit action; never coerce a URI into content.
                        val clip = clipboard.primaryClip
                        val pasted = if (clip != null && clip.itemCount > 0) clip.getItemAt(0).text?.toString() else null
                        if (!pasted.isNullOrEmpty()) {
                            editing = insertClipboardText(value, pasted)
                            change(editing.text)
                            focus.requestFocus()
                        }
                    } catch (_: Exception) { reportError("Clipboard text is unavailable") }
                }) {
                    Icon(painterResource(R.drawable.ic_paste), contentDescription = "Paste from clipboard")
                }
            }
        }
    }
}
