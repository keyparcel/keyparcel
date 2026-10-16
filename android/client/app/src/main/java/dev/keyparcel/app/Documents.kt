package dev.keyparcel.app

import android.content.ContentResolver
import android.content.Context
import android.content.Intent
import android.content.res.AssetFileDescriptor
import android.net.Uri
import android.os.CancellationSignal
import android.provider.OpenableColumns
import androidx.activity.result.contract.ActivityResultContracts
import java.io.Closeable
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeout
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.SynchronousQueue
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

data class SelectedDocument(val uri: Uri, val name: String, val size: Long?)

class SaveDocument : ActivityResultContracts.CreateDocument("application/octet-stream") {
    // Generic output bytes must not disable existing XML, image, or other typed files.
    override fun createIntent(context: Context, input: String): Intent =
        super.createIntent(context, input).putExtra(Intent.EXTRA_MIME_TYPES, arrayOf("*/*"))
}

fun selectedDocument(resolver: ContentResolver, uri: Uri, access: DocumentAccess): SelectedDocument {
    var name = "file"
    var size: Long? = null
    resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null, access.signal)?.let(access::own)?.use { cursor ->
        if (cursor.moveToFirst()) {
            val nameColumn = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
            val sizeColumn = cursor.getColumnIndex(OpenableColumns.SIZE)
            if (nameColumn >= 0 && !cursor.isNull(nameColumn)) name = cursor.getString(nameColumn)
            if (sizeColumn >= 0 && !cursor.isNull(sizeColumn)) size = cursor.getLong(sizeColumn).takeIf { it >= 0 }
        }
    }
    require(name.isNotEmpty() && name != "." && name != ".." && name.none { it == '/' || it == '\\' || it == '\u0000' }) {
        "The selected document has an invalid filename"
    }
    return SelectedDocument(uri, name, size)
}

// Closing an interruptible channel wakes provider reads/writes on supported Android versions.
class DocumentAccess {
    val signal = CancellationSignal()
    private var resource: Closeable? = null
    @Volatile private var cancelled = false

    fun checkActive() {
        if (cancelled) throw CancellationException("Document operation cancelled")
    }

    fun <T : Closeable> own(value: T): T {
        synchronized(this) {
            if (!cancelled) { resource = value; return value }
        }
        value.close()
        throw CancellationException("Document operation cancelled")
    }

    fun open(resolver: ContentResolver, uri: Uri, mode: String): AssetFileDescriptor =
        own(requireNotNull(resolver.openAssetFileDescriptor(uri, mode, signal)) { "Cannot open the document" })

    fun cancel() {
        val opened = synchronized(this) { cancelled = true; resource.also { resource = null } }
        // Provider cancellation and descriptor cleanup must not block the UI thread.
        CoroutineScope(Dispatchers.IO).launch {
            runCatching { opened?.close() }
            runCatching { signal.cancel() }
        }
    }
}

// One abandoned provider call leaves room for recovery; reject overload without queuing.
private val documentWorkers = ThreadPoolExecutor(0, 2, 30, TimeUnit.SECONDS, SynchronousQueue(), { task ->
    Thread(task, "keyparcel-documents").apply { isDaemon = true }
})

suspend fun <T> withDocumentAccess(timeoutMillis: Long, action: (DocumentAccess) -> T): T = try {
    withTimeout(timeoutMillis) {
        val access = DocumentAccess()
        suspendCancellableCoroutine { continuation ->
            continuation.invokeOnCancellation { access.cancel() }
            try {
                documentWorkers.execute {
                    try {
                        access.checkActive()
                        val result = action(access)
                        access.checkActive()
                        continuation.resume(result)
                    } catch (failure: Exception) {
                        continuation.resumeWithException(failure)
                    }
                }
            } catch (busy: RejectedExecutionException) {
                continuation.resumeWithException(IOException("Document providers are busy; retry after a stalled provider responds", busy))
            }
        }
    }
} catch (timeout: TimeoutCancellationException) {
    throw IOException("Document operation timed out", timeout)
}

fun copyDocumentContent(input: InputStream, output: OutputStream, byteLimit: Long = Long.MAX_VALUE, checkActive: () -> Unit): Long {
    val buffer = ByteArray(64 * 1024)
    var copied = 0L
    while (copied < byteLimit) {
        checkActive()
        val count = input.read(buffer, 0, minOf(buffer.size.toLong(), byteLimit - copied).toInt())
        if (count < 0) return copied
        output.write(buffer, 0, count)
        copied += count
    }
    return copied
}
