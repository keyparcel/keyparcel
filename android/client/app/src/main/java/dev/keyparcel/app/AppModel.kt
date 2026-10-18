package dev.keyparcel.app

import android.app.Application
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import dev.keyparcel.bridge.Bridge
import dev.keyparcel.bridge.Client
import dev.keyparcel.bridge.Delivery
import dev.keyparcel.storage.IdentityStore
import java.io.File
import java.nio.channels.Channels
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.suspendCancellableCoroutine
import org.json.JSONArray
import org.json.JSONObject

data class Device(val key: String, val fingerprint: String, val comment: String, val self: Boolean, val supported: Boolean)
data class InboxItem(val id: String, val from: String, val type: String, val filename: String, val size: Long, val uploaded: String)
data class ViewedText(val item: InboxItem, val content: ParcelContent, val settings: Settings, val identity: String) {
    val id get() = item.id
    val preview get() = content.preview
    fun matches(current: Settings, key: String) = settings == current && identity == key
}
data class FileSaveRequest(val item: InboxItem, val settings: Settings, val identity: String, val view: ViewedText? = null) {
    val host get() = requireNotNull(settings.host)
    val filename get() = if (item.type == "file") item.filename else "${item.id}.txt"
    val parent get() = if (view == null) Page.Inbox else Page.Text
    fun matches(current: Settings, key: String) = settings == current && identity == key
}
data class SavedFile(val request: FileSaveRequest, val name: String) {
    val id get() = request.item.id
}

class AppModel(application: Application) : AndroidViewModel(application) {
    private val store = SettingsStore(application)
    var settings by mutableStateOf(Settings()); private set
    var publicKey by mutableStateOf(""); private set
    var fingerprint by mutableStateOf(""); private set
    var ready by mutableStateOf(false); private set
    var busy by mutableStateOf(false); private set
    var error by mutableStateOf<String?>(null); private set
    var confirmation by mutableStateOf<Confirmation?>(null); private set
    val notice get() = confirmation?.message
    private var confirmationJob: Job? = null
    fun confirm(message: String) {
        confirmationJob?.cancel()
        val value = Confirmation(message, SystemClock.elapsedRealtime() + 3_000)
        confirmation = value
        confirmationJob = viewModelScope.launch {
            delay(3_000)
            if (confirmation === value) confirmation = value.expire(SystemClock.elapsedRealtime())
        }
    }
    fun dismissError() { error = null }
    var connectionState by mutableStateOf("Not connected"); private set
    private fun observeConnection() {
        connectionState = when {
            !foreground -> "Suspended"
            settings.host?.trusted != true -> "Not configured"
            else -> connected?.client?.connectionState() ?: "Not connected"
        }
    }
    var navigation by mutableStateOf(NavigationState()); private set
    val page get() = navigation.page
    var draft by mutableStateOf(SendDraft())
    var devices by mutableStateOf(emptyList<Device>()); private set
    var inbox by mutableStateOf(emptyList<InboxItem>()); private set
    var rejected by mutableStateOf(emptyList<String>()); private set
    var inboxLoaded by mutableStateOf(false); private set
    var devicesLoaded by mutableStateOf(false); private set
    var viewed by mutableStateOf<ViewedText?>(null); private set
    var pendingHost by mutableStateOf<Host?>(null); private set
    var selectedFile by mutableStateOf<SelectedDocument?>(null); private set
    var pendingSave by mutableStateOf<FileSaveRequest?>(null); private set
    var savedFile by mutableStateOf<SavedFile?>(null); private set
    var pendingDelete by mutableStateOf<InboxDeleteRequest?>(null); private set
    var progress by mutableStateOf<String?>(null); private set
    private data class PendingSend(val context: SendContext, val text: String, val file: File?, val filename: String, val report: SendReport)
    private var pendingSend by mutableStateOf<PendingSend?>(null)
    var sendFeedback by mutableStateOf<SendFeedback?>(null); private set
    val sendReport get() = sendFeedback?.report
    private var feedbackJob: Job? = null

    private fun feedback(value: SendFeedback?) {
        feedbackJob?.cancel()
        sendFeedback = value
        if (value != null && value.visible && value.dismissAt != 0L) feedbackJob = viewModelScope.launch {
            delay((value.dismissAt - SystemClock.elapsedRealtime()).coerceAtLeast(0))
            if (sendFeedback === value) sendFeedback = value.expire(SystemClock.elapsedRealtime())
        }
    }

    fun toggleSendFeedback() {
        sendFeedback?.let { feedback(it.expand(!it.expanded, SystemClock.elapsedRealtime())) }
    }
    val sendLocked get() = pendingSend != null
    val canRetrySend get() = pendingSend?.let { it.context.matches(settings, publicKey) && it.report.failed.isNotEmpty() } == true
    private val mainHandler = Handler(Looper.getMainLooper())
    private data class Connected(val host: Host, val client: Client, val epoch: Long)
    @Volatile private var connected: Connected? = null
    @Volatile private var sessionEpoch = 0L
    @Volatile private var foreground = false
    private val sessionLock = Any()
    private var manualRefreshJob: Job? = null
    private var dataRevision = 0L
    private var pollJob: Job? = null
    private var retry = RefreshRetry()
    private var pendingRefresh by mutableStateOf(false)
    val refreshing get() = pendingRefresh
    internal var refreshRevision by mutableStateOf(0L); private set
    internal var requestTimeoutMillis = 120_000L
    internal var documentTimeoutMillis = 120_000L

    init {
        load()
        viewModelScope.launch {
            while (isActive) {
                delay(500)
                observeConnection()
                startRefresh()
            }
        }
    }

    fun setForeground(value: Boolean) {
        foreground = value
        if (!value) {
            pollJob?.cancel()
            manualRefreshJob?.cancel()
            pendingRefresh = false
            retireConnection()
        } else { retry = RefreshRetry(); startRefresh() }
        observeConnection()
    }

    private fun retireConnection() = synchronized(sessionLock) {
        sessionEpoch++
        connected?.client?.close()
        connected = null
    }

    private data class RefreshResult(val items: String?, val discovery: String?, val failure: Exception?)

    private suspend fun fetchRefresh(snapshot: Settings, background: Boolean): RefreshResult {
        var items: String? = null
        var discovery: String? = null
        var failure: Exception? = null
        try {
            withClient(requireNotNull(snapshot.host), snapshot, background) { items = it.inbox(); discovery = it.discover() }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (caught: Exception) { failure = caught }
        return RefreshResult(items, discovery, failure)
    }

    private fun publishRefresh(result: RefreshResult) {
        result.items?.let(::publishInbox)
        result.discovery?.let(::publishDevices)
        observeConnection()
        refreshRevision++
    }

    private fun requestRefresh() {
        if (busy || manualRefreshJob?.isActive == true) return
        if (settings.host?.trusted != true) { error = "Connect and approve a server in Settings first"; return }
        val snapshot = settings
        val captured = RefreshContext(snapshot, sessionEpoch, ++dataRevision)
        pendingRefresh = true
        error = null
        manualRefreshJob = viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) { fetchRefresh(snapshot, false) }
                if (!captured.matches(settings, sessionEpoch, dataRevision, foreground)) return@launch
                publishRefresh(result)
                retry = if (result.failure == null) RefreshRetry() else retry.failed(SystemClock.elapsedRealtime())
                if (pendingRefresh && error == null) result.failure?.let { error = it.message ?: it.toString() }
            } finally {
                if (captured.matches(settings, sessionEpoch, dataRevision, foreground)) pendingRefresh = false
            }
        }
    }

    fun cancelRefresh() {
        if (!pendingRefresh) return
        pendingRefresh = false
        manualRefreshJob?.cancel()
        dataRevision++
        error = "Refresh cancelled; last successful data retained"
        observeConnection()
    }

    private fun startRefresh() {
        if (!foreground || !ready || busy || manualRefreshJob?.isActive == true || pollJob?.isActive == true || settings.host?.trusted != true ||
            SystemClock.elapsedRealtime() < retry.nextAttemptMillis) return
        val snapshot = settings
        val captured = RefreshContext(snapshot, sessionEpoch, dataRevision)
        pollJob = viewModelScope.launch {
            val result = withContext(Dispatchers.IO) { fetchRefresh(snapshot, true) }
            // Mutations own newer state, even if this poll finishes after Delete.
            if (!captured.matches(settings, sessionEpoch, dataRevision, foreground) || busy) return@launch
            publishRefresh(result)
            retry = if (result.failure == null) RefreshRetry() else retry.failed(SystemClock.elapsedRealtime())
        }
    }

    fun load() = work("Loading…", {
        val context = getApplication<Application>()
        go.Seq.setContext(context)
        Bridge.initialize(context.cacheDir.absolutePath)
        val saved = store.load()
        val key = IdentityStore(context).loadOrCreate()
        try {
            val public = Bridge.publicKey(key)
            Triple(saved, public, Bridge.fingerprint(public))
        } finally { key.fill(0) }
    }) { (saved, public, fp) ->
        if (publicKey.isNotEmpty() && publicKey != public) {
            retireConnection()
            dataRevision++
            invalidateVerifiedResults()
            clearPendingSend()
            draft = draft.copy(recipient = "", additionalRecipients = emptyList())
        }
        applySettings(saved)
        publicKey = public
        fingerprint = fp
        ready = true
        startRefresh()
    }

    private fun applySettings(updated: Settings) {
        draft = draft.reconcile(settings, updated)
        if (pendingSend?.context?.matches(updated, publicKey) == false) {
            clearPendingSend()
            error = "Server or recipient approval changed; choose recipients again. Previous deliveries are not undone."
        }
        if (settings != updated) {
            // Old verification results must never survive a trust-context change.
            pendingDelete = null
            dataRevision++
            pendingRefresh = false
            invalidateVerifiedResults()
            retry = RefreshRetry()
        }
        synchronized(sessionLock) {
            if (settings.host != updated.host) retireConnection()
            settings = updated
        }
        observeConnection()
    }

    private fun invalidateVerifiedResults() {
        pendingDelete = null
        inbox = emptyList(); rejected = emptyList(); devices = emptyList()
        inboxLoaded = false; devicesLoaded = false
        viewed = null; savedFile = null; pendingSave = null
        if (page == Page.Text || page == Page.File) navigation = navigation.open(Page.Inbox)
    }

    private fun setHost(host: Host) {
        applySettings(settings.copy(host = host))
        inbox = emptyList(); rejected = emptyList(); devices = emptyList()
        inboxLoaded = false; devicesLoaded = false; viewed = null; savedFile = null; pendingSave = null
    }

    fun connectHost(draft: Host) {
        val approved = settings.host?.takeIf { it.sameServer(draft) }?.key.orEmpty()
        val host = draft.copy(key = approved)
        pendingHost = null
        if (host.trusted) connectApprovedHost(host)
        else work("Connecting…", {
            host.validateConnection()
            host.copy(key = Bridge.probeServerKey(host.address, host.port.toLong(), minOf(requestTimeoutMillis, 30_000L)))
        }) { pendingHost = it }
    }

    fun cancelHostApproval() { pendingHost = null }

    fun approveHost() {
        val host = pendingHost ?: return
        pendingHost = null
        connectApprovedHost(host)
    }

    private fun connectApprovedHost(host: Host) = work("Connecting…", {
        require(host.trusted) { "Approve the server before connecting" }
        store.save(settings.copy(host = host))
        // Explicit approval is remembered even if this device still needs enrollment.
        withContext(Dispatchers.Main) { setHost(host) }
        withClient(host) { it.checkConnection() }
    }) {
        showPage(Page.Inbox)
    }

    fun forgetHostApproval() = work("Saving…", {
        val host = requireNotNull(settings.host).copy(key = "")
        store.save(settings.copy(host = host))
        host
    }) {
        setHost(it)
        confirm("Server approval removed")
    }

    fun refreshDevices() = requestRefresh()

    private fun publishDevices(json: String) {
        val array = JSONArray(json)
        devices = (0 until array.length()).map {
            val d = array.getJSONObject(it)
            Device(d.getString("public_key"), d.getString("fingerprint"), d.optString("comment"),
                d.getBoolean("self"), d.getBoolean("supported"))
        }
        devicesLoaded = true
    }

    fun suggestedPeerName(device: Device): String {
        if (device.comment in settings.peers) return ""
        return try {
            Bridge.validatePeer(device.comment, device.key)
            device.comment
        } catch (_: Exception) { "" }
    }

    fun approve(device: Device, name: String) = work("Approving…", {
        Bridge.validatePeer(name, device.key)
        require(name !in settings.peers || settings.peers[name] == device.key) {
            "That name belongs to another device; remove its approval before reusing it"
        }
        require(settings.peers.none { (otherName, key) -> key == device.key && otherName != name }) {
            "This device already has an approved name"
        }
        val updated = settings.copy(peers = settings.peers + (name to device.key))
        store.save(updated)
        updated
    }) { applySettings(it); confirm("Device approved as $name") }

    fun forget(name: String) = work("Saving…", {
        val updated = settings.copy(peers = settings.peers - name)
        store.save(updated)
        updated
    }) {
        applySettings(it)
        inbox = emptyList(); rejected = emptyList(); inboxLoaded = false
        confirm("Device unapproved: $name")
    }

    fun refreshInbox() = requestRefresh()

    private fun publishInbox(json: String) {
        val result = JSONObject(json)
        val items = result.getJSONArray("items")
        inbox = (0 until items.length()).map {
            val item = items.getJSONObject(it)
            InboxItem(item.getString("transfer_id"), item.getString("from"), item.getString("type"),
                if (item.isNull("filename")) "" else item.getString("filename"),
                item.getLong("size"), item.getString("uploaded_at"))
        }
        val bad = result.getJSONArray("rejected")
        rejected = (0 until bad.length()).map {
            val item = bad.getJSONObject(it)
            "${item.getString("ID")}: ${item.getString("Reason")}"
        }
        inboxLoaded = true
    }

    fun receive(item: InboxItem) {
        if (busy) return
        val selected = inbox.firstOrNull { it.id == item.id }
            ?: run { error = "Refresh Inbox before viewing this transfer"; return }
        if (selected.size > MAX_VIEW_BYTES) { error = TOO_LARGE_TO_VIEW; return }
        val captured = settings
        val identity = publicKey
        work("Verifying…", {
            var received: ViewedText? = null
            withClient { client ->
                require(settings == captured && publicKey == identity) { "The server or approvals changed; view the parcel again" }
                client.receive(selected.id, Delivery { manifest, path ->
                    val metadata = JSONObject(manifest)
                    val verified = selected.copy(type = metadata.getString("type"),
                        filename = if (metadata.isNull("filename")) "" else metadata.getString("filename"), size = metadata.getLong("size"))
                    received = ViewedText(verified, ParcelContent.readVerified(File(path), verified.size), captured, identity)
                }, true)
            }
            requireNotNull(received)
        }) {
            if (it.matches(settings, publicKey)) { viewed = it; showPage(Page.Text) }
            else error = "The server or approvals changed; view the parcel again"
        }
    }

    fun beginFileSave(item: InboxItem, view: ViewedText? = null) {
        if (busy) return
        if (view != null && (!view.matches(settings, publicKey) || viewed !== view)) {
            error = "The server or approvals changed; view the parcel again"; return
        }
        val selected = view?.item ?: inbox.firstOrNull { it.id == item.id }
            ?: run { error = "Refresh Inbox before saving this transfer"; return }
        pendingSave = FileSaveRequest(selected, settings, publicKey, view)
    }

    fun saveFile(uri: Uri?) {
        val request = pendingSave ?: return
        pendingSave = null
        if (uri == null) return
        work("Saving…", {
            require(request.matches(settings, publicKey)) { "The server or approvals changed; save the parcel again" }
            val operation = currentCoroutineContext()
            val resolver = getApplication<Application>().contentResolver
            var name = request.filename
            fun write(input: java.io.InputStream, size: Long) {
                reportProgress("Saving file…")
                try {
                    runBlocking(operation) {
                        withDocumentAccess(documentTimeoutMillis) { access ->
                            access.open(resolver, uri, "wt").use { destination ->
                                require(destination.declaredLength < 0 || destination.declaredLength >= size) {
                                    "The destination cannot hold the file"
                                }
                                destination.createOutputStream().use { output ->
                                    copyDocumentContent(input, Channels.newOutputStream(access.own(output.channel)), checkActive = access::checkActive)
                                }
                            }
                        }
                    }
                } catch (failure: Exception) {
                    throw java.io.IOException("Save failed; the destination may be incomplete: ${failure.message}", failure)
                }
            }
            if (request.view != null) {
                request.view.content.openStream().use { write(it, request.view.content.size.toLong()) }
            } else {
                reportProgress("Verifying…")
                withClient { client ->
                    require(request.matches(settings, publicKey)) { "The server or approvals changed; save the parcel again" }
                    client.receive(request.item.id, Delivery { manifest, path ->
                        val metadata = JSONObject(manifest)
                        name = if (metadata.getString("type") == "file") metadata.getString("filename") else "${request.item.id}.txt"
                        File(path).inputStream().use { write(it, File(path).length()) }
                    }, true)
                }
            }
            SavedFile(request, name)
        }) {
            if (it.request.matches(settings, publicKey)) { savedFile = it; showPage(Page.File) }
            else error = "The server or approvals changed; return to Inbox"
        }
    }

    fun selectFile(uri: Uri?) {
        if (uri == null) return
        work("Selecting file…", { withDocumentAccess(documentTimeoutMillis) { access ->
            selectedDocument(getApplication<Application>().contentResolver, uri, access)
        } }) { selectedFile = it }
    }

    fun sendFile(recipient: String) {
        if (busy || sendLocked) return
        val document = selectedFile ?: return
        val context = sendContext(recipient)
        feedback(null)
        work("Sending…", {
            reportProgress("Preparing file…")
            val application = getApplication<Application>()
            val staging = File.createTempFile("keyparcel-send-", ".tmp", application.cacheDir)
            var retained = false
            try {
                withDocumentAccess(documentTimeoutMillis) { access ->
                    access.open(application.contentResolver, document.uri, "r").use { source ->
                        source.createInputStream().use { input ->
                            staging.outputStream().use { output ->
                                copyDocumentContent(Channels.newInputStream(access.own(input.channel)), output,
                                    source.declaredLength.takeIf { it >= 0 } ?: Long.MAX_VALUE, access::checkActive)
                                if (source.parcelFileDescriptor.canDetectErrors()) source.parcelFileDescriptor.checkError()
                            }
                        }
                    }
                }
                reportProgress("Uploading…")
                val report = sendBatch(context, context.names, "", staging, document.name)
                retained = withContext(Dispatchers.Main) { finishSend(context, "", staging, document.name, report) }
            } finally { if (!retained) staging.delete() }
        }) {}
    }

    private fun sendContext(recipient: String) = SendContext(settings,
        (listOf(recipient) + draft.additionalRecipients).filter { it.isNotEmpty() }.distinct(), publicKey)

    private suspend fun sendBatch(context: SendContext, names: List<String>, text: String, file: File?, filename: String): SendReport {
        val json = withClient(requireNotNull(context.settings.host) { "Configure a server first" }) { client ->
            require(context.matches(settings, publicKey)) { "Server or recipient approval changed; choose recipients again" }
            val recipients = JSONArray(names).toString()
            if (file == null) client.sendTextTo(recipients, text) else client.sendNamedFileTo(recipients, file.absolutePath, filename)
        }
        val result = JSONObject(json)
        val array = result.optJSONArray("deliveries")
        if (array == null || array.length() == 0) throw java.io.IOException(result.optString("error", "Send did not start"))
        val deliveries = (0 until array.length()).map {
            val d = array.getJSONObject(it)
            SendDelivery(d.getString("recipient"), d.getString("transfer_id"), d.optString("error"), d.optBoolean("unconfirmed"))
        }
        // Never interpret a missing outcome as success or make it retryable.
        return if (deliveries.map { it.recipient } == names) SendReport(deliveries)
            else SendReport(names.map { SendDelivery(it, "", "Incomplete delivery report", true) })
    }

    private fun finishSend(context: SendContext, text: String, file: File?, filename: String, report: SendReport): Boolean {
        if (!context.matches(settings, publicKey)) {
            error = "Send finished for ${context.settings.host?.address}; check those inboxes before sending again"
            return false
        }
        val open = sendFeedback?.expanded == true || !report.complete
        feedback(SendFeedback(report).expand(open, SystemClock.elapsedRealtime()))
        if (!report.complete) {
            pendingSend = PendingSend(context, text, file, filename, report)
            return true
        }
        clearPendingSend()
        if (file == null) draft = draft.copy(text = "") else selectedFile = null
        return false
    }

    fun retrySend() {
        if (busy) return
        val pending = pendingSend ?: return
        val names = pending.report.failed
        if (names.isEmpty()) return
        work("Sending…", {
            require(pending.context.matches(settings, publicKey)) { "Server or recipient approval changed; choose recipients again" }
            val report = sendBatch(pending.context, names, pending.text, pending.file, pending.filename)
            withContext(Dispatchers.Main) {
                if (pendingSend === pending) finishSend(pending.context, pending.text, pending.file, pending.filename, pending.report.merge(report))
            }
        }) {}
    }

    private fun clearPendingSend() { pendingSend?.file?.delete(); pendingSend = null }

    fun editSend() {
        if (busy) return
        clearPendingSend(); feedback(null)
        draft = draft.copy(recipient = "", additionalRecipients = emptyList())
    }

    private fun reportProgress(message: String) { mainHandler.post { if (busy) progress = message } }

    fun beginInboxDelete(item: InboxItem) {
        if (busy || pendingDelete != null) return
        if (settings.host?.trusted != true || inbox.none { it.id == item.id }) {
            error = "The inbox or server changed; select the transfer again"
            return
        }
        pendingDelete = InboxDeleteRequest(item.id, settings)
    }

    fun cancelInboxDelete() { if (!busy) pendingDelete = null }

    fun confirmInboxDelete() {
        if (busy) return
        val request = pendingDelete ?: return
        pendingDelete = null
        if (!request.matches(settings, inbox)) {
            error = "The inbox or server changed; select the transfer again"
            return
        }
        work("Deleting…", {
            withClient(requireNotNull(request.settings.host)) {
                require(request.matches(settings, inbox)) { "The inbox or approvals changed; select the transfer again" }
                it.delete(request.id)
            }
        }) {
            // A completed request must not edit a different server's same-ID item.
            if (request.settings == settings) {
                inbox = inbox.filterNot { it.id == request.id }
                confirm("Parcel deleted")
            }
        }
    }

    fun removeReceived() {
        val id = viewed?.id ?: savedFile?.id ?: return
        val request = savedFile?.request ?: viewed?.let { FileSaveRequest(it.item, it.settings, it.identity, it) } ?: return
        work("Deleting…", {
            require(request.matches(settings, publicKey)) { "The server or approvals changed; return to Inbox" }
            withClient(request.host) {
                require(request.matches(settings, publicKey)) { "The server or approvals changed; return to Inbox" }
                it.delete(id)
            }
        }) {
            if (!request.matches(settings, publicKey)) return@work
            inbox = inbox.filterNot { it.id == id }
            viewed = null; savedFile = null
            showPage(Page.Inbox)
            confirm("Parcel deleted")
        }
    }

    private fun showPage(target: Page) {
        if (page != target) { error = null; pendingRefresh = false }
        pendingHost = null; pendingSave = null; pendingDelete = null
        if (target != Page.Text && !(target == Page.File && savedFile?.request?.view === viewed)) viewed = null
        if (target != Page.File) savedFile = null
        navigation = navigation.open(target)
    }

    fun navigate(target: Page) { if (!busy) showPage(target) }

    val backLabel get() = when (page) {
        Page.Text -> "Back to Inbox"
        Page.File -> if (savedFile?.request?.parent == Page.Text) "Back to parcel" else "Back to Inbox"
        else -> "Back"
    }
    fun back() {
        if (busy) return
        if (page == Page.File && savedFile?.request?.parent == Page.Text && viewed != null) showPage(Page.Text)
        else showPage(navigation.back().page)
    }

    fun discardDraft() { clearPendingSend(); feedback(null); draft = SendDraft(); selectedFile = null; confirm("Draft discarded") }

    fun send(recipient: String, text: String) {
        if (busy || sendLocked) return
        val context = sendContext(recipient)
        feedback(null)
        work("Sending…", {
            val report = sendBatch(context, context.names, text, null, "")
            withContext(Dispatchers.Main) { finishSend(context, text, null, "", report) }
        }) {}
    }

    fun clipboardError(message: String?) { error = message; confirmationJob?.cancel(); confirmation = null }

    private suspend fun <T> withClient(
        host: Host = requireNotNull(settings.host) { "Configure a server in Settings first" },
        snapshot: Settings = settings,
        background: Boolean = false,
        action: (Client) -> T
    ): T {
        require(host.trusted) { "Connect in Settings and approve the server first" }
        currentCoroutineContext().ensureActive()
        val peers = JSONObject(snapshot.peers.toMap()).toString()
        val timeout = requestTimeoutMillis
        val client = synchronized(sessionLock) {
            require(foreground) { "Connection suspended while the app is in the background" }
            // A request captured before a profile change must not resurrect it.
            require(settings == snapshot && settings.host == host) { "The server changed; try the operation again" }
            val epoch = sessionEpoch
            var connection = connected
            if (connection == null || connection.host != host || connection.epoch != epoch) {
                connection?.client?.close()
                val key = IdentityStore(getApplication()).loadOrCreate()
                val owner = try {
                    Bridge.newPersistentClient(key, host.address, host.port.toLong(), host.user, host.key, host.root, host.discovery, timeout)
                } finally { key.fill(0) }
                connection = Connected(host, owner, epoch)
                connected = connection
            }
            connection.client.newRequest(peers, timeout, background)
        }
        try {
            currentCoroutineContext().ensureActive()
            return suspendCancellableCoroutine { continuation ->
                continuation.invokeOnCancellation { client.cancelRequest() }
                try { continuation.resumeWith(Result.success(action(client))) }
                catch (failure: Exception) { continuation.resumeWith(Result.failure(failure)) }
            }
        } finally { client.cancelRequest() }
    }

    private fun <T> work(label: String, task: suspend () -> T, success: (T) -> Unit) {
        if (busy) return
        // Running polls keep their transport; their older snapshots cannot publish.
        pendingRefresh = false
        dataRevision++
        confirmationJob?.cancel(); confirmation = null
        busy = true; error = null; progress = label
        viewModelScope.launch {
            try { success(withContext(Dispatchers.IO) { task() }) }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) { error = failure.message ?: failure.toString() }
            finally { busy = false; progress = null; retry = RefreshRetry(); startRefresh() }
        }
    }

    override fun onCleared() {
        viewed = null; savedFile = null; pendingSave = null
        clearPendingSend()
        foreground = false
        retireConnection()
        super.onCleared()
    }
}
