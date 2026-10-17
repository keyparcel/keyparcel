package dev.keyparcel.app

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.os.Parcel
import android.os.PersistableBundle
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.Clipboard
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource

@Composable
fun CopyButton(label: String, tag: String, enabled: Boolean = true, copy: () -> Unit) {
    IconButton(onClick = copy, enabled = enabled, modifier = Modifier.testTag(tag)) {
        Icon(painterResource(R.drawable.ic_copy), contentDescription = label)
    }
}

fun copyText(context: Context, content: ClipboardText, label: String, sensitive: Boolean, reportError: (String?) -> Unit, copied: () -> Unit = {}) {
    reportError(null)
    val text = content.text
    if (text == null) { reportError(requireNotNull(content.problem)); return }
    val unavailable = "Android cannot copy this much text. Copy a smaller selection; the remote transfer is kept."
    // Binder has a shared 1 MiB buffer; leave room for the request envelope.
    if (text.length >= 1024 * 1024) { reportError(unavailable); return }
    val clip = ClipData.newPlainText(label, text)
    if (sensitive) clip.description.extras = PersistableBundle().apply {
        putBoolean("android.content.extra.IS_SENSITIVE", true)
    }
    val parcel = Parcel.obtain()
    try {
        clip.writeToParcel(parcel, 0)
        if (parcel.dataSize() > 1024 * 1024 - 4096) { reportError(unavailable); return }
    } finally { parcel.recycle() }
    try {
        val clipboard = context.getSystemService(ClipboardManager::class.java)
            ?: error("Clipboard service is unavailable")
        clipboard.setPrimaryClip(clip)
        copied()
    } catch (_: Exception) {
        reportError("Clipboard copying is unavailable." + if (sensitive) " The remote transfer is kept." else "")
    }
}

// Intercept the text box's native selection copying as well as its copy icon.
class ReceivedTextClipboard(private val delegate: Clipboard, private val copy: (ClipboardText) -> Unit) : Clipboard by delegate {
    override suspend fun setClipEntry(clipEntry: ClipEntry?) {
        val text = clipEntry?.clipData?.getItemAt(0)?.text?.toString() ?: return
        copy(clipboardText(text))
    }
}
