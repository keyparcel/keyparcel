package dev.keyparcel.app

import android.database.Cursor
import android.database.MatrixCursor
import android.os.CancellationSignal
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.os.ParcelFileDescriptor
import android.provider.DocumentsContract.Document
import android.provider.DocumentsContract.Root
import android.provider.DocumentsProvider
import android.system.Os
import android.system.OsConstants
import android.system.StructPollfd
import kotlin.concurrent.thread
import java.io.File
import java.io.FileNotFoundException

// Scenario-only provider exercises real pickers with private, non-path documents.
class FixtureDocumentsProvider : DocumentsProvider() {
    private val directory get() = File(requireNotNull(context).filesDir, "document-fixtures").apply { mkdirs() }
    override fun onCreate() = true

    override fun queryRoots(projection: Array<out String>?): Cursor {
        val cursor = MatrixCursor(projection ?: arrayOf(Root.COLUMN_ROOT_ID, Root.COLUMN_DOCUMENT_ID,
            Root.COLUMN_TITLE, Root.COLUMN_FLAGS, Root.COLUMN_MIME_TYPES))
        cursor.newRow().apply {
            add(Root.COLUMN_ROOT_ID, "fixture")
            add(Root.COLUMN_DOCUMENT_ID, "root")
            add(Root.COLUMN_TITLE, "keyparcel test files")
            add(Root.COLUMN_FLAGS, Root.FLAG_SUPPORTS_CREATE or Root.FLAG_LOCAL_ONLY)
            add(Root.COLUMN_MIME_TYPES, "*/*")
        }
        return cursor
    }

    override fun queryDocument(documentId: String, projection: Array<out String>?): Cursor =
        documents(projection).apply { addDocument(this, documentId) }

    override fun queryChildDocuments(parentDocumentId: String, projection: Array<out String>?, sortOrder: String?): Cursor =
        documents(projection).apply {
            check(parentDocumentId == "root")
            directory.listFiles()?.sortedBy { it.name }?.forEach { addDocument(this, it.name) }
        }

    private fun documents(projection: Array<out String>?) = MatrixCursor(projection ?: arrayOf(
        Document.COLUMN_DOCUMENT_ID, Document.COLUMN_DISPLAY_NAME, Document.COLUMN_MIME_TYPE,
        Document.COLUMN_FLAGS, Document.COLUMN_SIZE))

    private fun file(id: String): File {
        require(id.isNotEmpty() && id != "." && id != ".." && id.none { it == '/' || it == '\\' })
        return File(directory, id)
    }

    private fun addDocument(cursor: MatrixCursor, id: String) {
        val root = id == "root"
        val name = when (id) { "source" -> "payload-世界.bin"; "empty" -> "empty.bin"; else -> id }
        cursor.newRow().apply {
            add(Document.COLUMN_DOCUMENT_ID, id)
            add(Document.COLUMN_DISPLAY_NAME, if (root) "keyparcel test files" else name)
            add(Document.COLUMN_MIME_TYPE, when {
                root -> Document.MIME_TYPE_DIR
                id.endsWith(".xml") -> "application/xml"
                else -> "application/octet-stream"
            })
            add(Document.COLUMN_FLAGS, if (root) Document.FLAG_DIR_SUPPORTS_CREATE else Document.FLAG_SUPPORTS_WRITE)
            add(Document.COLUMN_SIZE, if (root || id == "source") null else file(id).length())
        }
    }

    override fun createDocument(parentDocumentId: String, mimeType: String, displayName: String): String {
        check(parentDocumentId == "root")
        var target = file(displayName)
        var suffix = 1
        while (!target.createNewFile()) target = file("${displayName.substringBeforeLast('.', displayName)}(${suffix++}).${displayName.substringAfterLast('.', "bin")}")
        return target.name
    }

    override fun openDocument(documentId: String, mode: String, signal: CancellationSignal?): ParcelFileDescriptor {
        if (documentId.startsWith("blocked-open-")) {
            File(directory, "$documentId-opened").writeText("opened")
            signal?.setOnCancelListener { File(directory, "$documentId-cancelled").writeText("cancelled") }
            val deadline = SystemClock.elapsedRealtime() + 30_000
            // Deliberately ignore cancellation before returning a descriptor.
            while (!File(directory, "blocked-open-release").exists() && SystemClock.elapsedRealtime() < deadline) Thread.sleep(10)
            val descriptor = ParcelFileDescriptor.open(file(documentId), ParcelFileDescriptor.parseMode(mode),
                Handler(Looper.getMainLooper())) {
                File(directory, "$documentId-closed").writeText("closed")
            }
            File(directory, "$documentId-returned").writeText("returned")
            return descriptor
        }
        if (documentId == "fail-read") {
            val pipe = ParcelFileDescriptor.createReliablePipe()
            ParcelFileDescriptor.AutoCloseOutputStream(pipe[1]).use { output ->
                output.write(byteArrayOf(1, 2, 3))
                pipe[1].closeWithError("Fixture source failed")
            }
            return pipe[0]
        }
        if (documentId == "stall-read" || documentId.startsWith("stall-write")) {
            val reading = documentId == "stall-read"
            val marker = if (reading) "stall-read" else "stall-write"
            val pipe = ParcelFileDescriptor.createPipe()
            val held = pipe[if (reading) 1 else 0]
            File(directory, "$marker-opened").writeText("opened")
            // Keep the peer open but idle; observe client closure without unblocking it ourselves.
            thread(name = "fixture-$marker") {
                held.use {
                    val poll = StructPollfd().apply { fd = held.fileDescriptor; events = 0 }
                    Os.poll(arrayOf(poll), 30_000)
                    if (poll.revents.toInt() and (OsConstants.POLLERR or OsConstants.POLLHUP) != 0) {
                        File(directory, "$marker-closed").writeText("closed")
                    }
                }
            }
            return pipe[if (reading) 0 else 1]
        }
        if (documentId.startsWith("fail-output")) throw FileNotFoundException("Fixture destination is unavailable")
        if (documentId.startsWith("fail-write")) {
            val pipe = ParcelFileDescriptor.createPipe()
            pipe[0].close()
            return pipe[1]
        }
        return ParcelFileDescriptor.open(file(documentId), ParcelFileDescriptor.parseMode(mode))
    }

    override fun isChildDocument(parentDocumentId: String, documentId: String) = parentDocumentId == "root" && documentId != "root"
}
