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
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.isActive
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

data class Device(val key: String, val fingerprint: String, val comment: String, val self: Boolean, val supported: Boolean)
data class InboxItem(val id: String, val from: String, val type: String, val filename: String, val size: Long, val uploaded: String)
data class ViewedText(val id: String, val preview: TextPreview, val clipboard: ClipboardText, val host: Host)
data class SavedFile(val id: String, val name: String, val host: Host)
data class FileSaveRequest(val item: InboxItem, val host: Host)

class AppModel(application: Application) : AndroidViewModel(application) {
    private val store = SettingsStore(application)
    var settings by mutableStateOf(Settings()); private set
    var publicKey by mutableStateOf(""); private set
    var fingerprint by mutableStateOf(""); private set
    var ready by mutableStateOf(false); private set
    var busy by mutableStateOf(false); private set
    var error by mutableStateOf<String?>(null); private set
    var notice by mutableStateOf<String?>(null); private set
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
    var progress by mutableStateOf<String?>(null); private set
    private val mainHandler = Handler(Looper.getMainLooper())
    private data class Connected(val host: Host, val client: Client, val epoch: Long)
    @Volatile private var connected: Connected? = null
    @Volatile private var sessionEpoch = 0L
    @Volatile private var foreground = false
    private val sessionLock = Mutex()
    private var pollJob: Job? = null
    private var retry = RefreshRetry()
    private var pendingRefresh = false
    internal var refreshRevision by mutableStateOf(0L); private set
    var syncStatus by mutableStateOf("Not connected"); private set
    var syncError by mutableStateOf<String?>(null); private set
    internal var requestTimeoutMillis = 120_000L
    internal var documentTimeoutMillis = 120_000L

    init {
        load()
        viewModelScope.launch {
            while (isActive) {
                delay(500)
                startRefresh()
            }
        }
    }

    fun setForeground(value: Boolean) {
        foreground = value
        if (!value) {
            pollJob?.cancel()
            retireConnection()
            if (settings.host?.trusted == true) syncStatus = "Stale"
        } else { retry = RefreshRetry(); startRefresh() }
    }

    private fun retireConnection() {
        sessionEpoch++
        connected?.client?.close()
        connected = null
    }

    private fun requestRefresh() {
        pendingRefresh = true
        error = null
        retry = retry.copy(nextAttemptMillis = 0)
        startRefresh()
    }

    private fun startRefresh() {
        if (!foreground || !ready || busy || pollJob?.isActive == true || settings.host?.trusted != true ||
            SystemClock.elapsedRealtime() < retry.nextAttemptMillis) return
        val snapshot = settings
        val epoch = sessionEpoch
        if (!inboxLoaded || !devicesLoaded) syncStatus = "Loading"
        pollJob = viewModelScope.launch {
            var items: String? = null
            var discovery: String? = null
            try {
                withContext(Dispatchers.IO) {
                    withClient { items = it.inbox(); discovery = it.discover() }
                }
                if (settings != snapshot || epoch != sessionEpoch || !foreground) return@launch
                publishInbox(requireNotNull(items))
                publishDevices(requireNotNull(discovery))
                retry = RefreshRetry()
                syncStatus = "Current"; syncError = null
                refreshRevision++
                pendingRefresh = false
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) {
                if (settings != snapshot || epoch != sessionEpoch || !foreground) return@launch
                items?.let(::publishInbox)
                syncStatus = "Stale"; syncError = failure.message ?: failure.toString()
                retry = retry.failed(SystemClock.elapsedRealtime())
                if (pendingRefresh) error = syncError
                refreshRevision++
                pendingRefresh = false
            }
        }
    }

    fun load() = work({
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
        applySettings(saved)
        publicKey = public
        fingerprint = fp
        ready = true
        startRefresh()
    }

    private fun applySettings(updated: Settings) {
        draft = draft.reconcile(settings, updated)
        if (settings != updated) {
            // Old verification results must never survive a trust-context change.
            pollJob?.cancel()
            retireConnection()
            inbox = emptyList(); rejected = emptyList(); devices = emptyList()
            inboxLoaded = false; devicesLoaded = false
            syncStatus = if (updated.host?.trusted == true) "Loading" else "Not connected"
            syncError = null; retry = RefreshRetry()
        }
        settings = updated
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
        else work({
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

    private fun connectApprovedHost(host: Host) = work({
        require(host.trusted) { "Approve the server before connecting" }
        store.save(settings.copy(host = host))
        // Explicit approval is remembered even if this device still needs enrollment.
        withContext(Dispatchers.Main) { setHost(host) }
        withClient(host) { it.checkConnection() }
    }) {
        showPage(Page.Inbox)
        notice = "Server connected. Inbox and Devices update automatically."
    }

    fun forgetHostApproval() = work({
        val host = requireNotNull(settings.host).copy(key = "")
        store.save(settings.copy(host = host))
        host
    }) {
        setHost(it)
        notice = "Server approval removed. Connect again and verify its fingerprint."
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

    fun approve(device: Device, name: String) = work({
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
    }) { applySettings(it); notice = "Device approved as $name" }

    fun forget(name: String) = work({
        val updated = settings.copy(peers = settings.peers - name)
        store.save(updated)
        updated
    }) {
        applySettings(it)
        inbox = emptyList(); rejected = emptyList(); inboxLoaded = false
        notice = "Approval removed for $name"
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

    fun receive(item: InboxItem) = work({
        val host = requireNotNull(settings.host) { "Configure a server first" }
        var received: ViewedText? = null
        withClient { client ->
            client.receive(item.id, Delivery { manifest, path ->
                require(JSONObject(manifest).getString("type") == "text") { "Expected a text transfer" }
                val file = File(path)
                received = ViewedText(item.id, readTextPreview(file), readClipboardText(file), host)
            }, true)
        }
        requireNotNull(received)
    }) { viewed = it; showPage(Page.Text) }

    fun beginFileSave(item: InboxItem) {
        if (busy) return
        pendingSave = FileSaveRequest(item, requireNotNull(settings.host))
    }

    fun saveFile(uri: Uri?) {
        val request = pendingSave ?: return
        pendingSave = null
        if (uri == null) return
        work({
            require(settings.host == request.host) { "The server changed; receive the file again" }
            val operation = currentCoroutineContext()
            val resolver = getApplication<Application>().contentResolver
            var name = request.item.filename
            reportProgress("Downloading and verifying $name…")
            withClient { client ->
                client.receive(request.item.id, Delivery { manifest, path ->
                    val metadata = JSONObject(manifest)
                    require(metadata.getString("type") == "file") { "Expected a file transfer" }
                    name = metadata.getString("filename")
                    reportProgress("Saving $name…")
                    try {
                        runBlocking(operation) {
                            withDocumentAccess(documentTimeoutMillis) { access ->
                                access.open(resolver, uri, "wt").use { destination ->
                                    require(destination.declaredLength < 0 || destination.declaredLength >= File(path).length()) {
                                        "The destination cannot hold the file"
                                    }
                                    File(path).inputStream().use { input ->
                                        destination.createOutputStream().use { output ->
                                            copyDocumentContent(input, Channels.newOutputStream(access.own(output.channel)), checkActive = access::checkActive)
                                        }
                                    }
                                }
                            }
                        }
                    } catch (failure: Exception) {
                        throw java.io.IOException("Save failed; the destination may be incomplete: ${failure.message}", failure)
                    }
                }, true)
            }
            SavedFile(request.item.id, name, request.host)
        }) { savedFile = it; showPage(Page.File) }
    }

    fun selectFile(uri: Uri?) {
        if (uri == null) return
        work({ withDocumentAccess(documentTimeoutMillis) { access ->
            selectedDocument(getApplication<Application>().contentResolver, uri, access)
        } }) { selectedFile = it }
    }

    fun sendFile(recipient: String) {
        val document = selectedFile ?: return
        work({
            reportProgress("Preparing ${document.name}…")
            val context = getApplication<Application>()
            val staging = File.createTempFile("keyparcel-send-", ".tmp", context.cacheDir)
            try {
                withDocumentAccess(documentTimeoutMillis) { access ->
                    access.open(context.contentResolver, document.uri, "r").use { source ->
                        source.createInputStream().use { input ->
                            staging.outputStream().use { output ->
                                copyDocumentContent(Channels.newInputStream(access.own(input.channel)), output,
                                    source.declaredLength.takeIf { it >= 0 } ?: Long.MAX_VALUE, access::checkActive)
                                if (source.parcelFileDescriptor.canDetectErrors()) source.parcelFileDescriptor.checkError()
                            }
                        }
                    }
                }
                reportProgress("Uploading ${document.name}…")
                withClient { it.sendNamedFile(recipient, staging.absolutePath, document.name) }
            } finally { staging.delete() }
        }) {
            discardDraft(); showPage(Page.Inbox)
            notice = "Sent to ${if (recipient == "self") "this device" else recipient} · $it"
        }
    }

    private fun reportProgress(message: String) { mainHandler.post { if (busy) progress = message } }

    fun removeReceived() {
        val id = viewed?.id ?: savedFile?.id ?: return
        val host = viewed?.host ?: savedFile?.host ?: return
        work({
            require(settings.host == host) { "The server changed; return to Inbox and receive again" }
            withClient { it.delete(id) }
        }) {
            inbox = inbox.filterNot { it.id == id }
            viewed = null; savedFile = null
            showPage(Page.Inbox)
            notice = "Transfer removed"
        }
    }

    private fun showPage(target: Page) {
        pendingHost = null; pendingSave = null
        if (target != Page.Text) viewed = null
        if (target != Page.File) savedFile = null
        navigation = navigation.open(target)
    }

    fun navigate(target: Page) { if (!busy) showPage(target) }

    fun back() { if (!busy) showPage(navigation.back().page) }

    fun discardDraft() { draft = SendDraft(); selectedFile = null }

    fun send(recipient: String, text: String) = work({ withClient { it.sendText(recipient, text) } }) {
        discardDraft(); showPage(Page.Inbox)
        notice = "Sent to ${if (recipient == "self") "this device" else recipient} · $it"
    }

    fun clipboardError(message: String?) { error = message; notice = null }

    private suspend fun <T> withClient(host: Host = requireNotNull(settings.host) { "Configure a server in Settings first" }, action: (Client) -> T): T = sessionLock.withLock {
        require(host.trusted) { "Connect in Settings and approve the server first" }
        currentCoroutineContext().ensureActive()
        val epoch = sessionEpoch
        var connection = connected
        if (connection == null || connection.host != host || connection.epoch != epoch) {
            connection?.client?.close()
            val key = IdentityStore(getApplication()).loadOrCreate()
            val client = try {
                Bridge.newPersistentClient(key, host.address, host.port.toLong(), host.user, host.key, host.root, host.discovery,
                    requestTimeoutMillis)
            } finally { key.fill(0) }
            connection = Connected(host, client, epoch)
            connected = connection
        }
        val client = connection.client
        if (epoch != sessionEpoch || !foreground) {
            client.close()
            if (connected === connection) connected = null
            throw java.io.IOException("Connection suspended while the app is in the background")
        }
        client.setRequestTimeout(requestTimeoutMillis)
        client.setPeers(JSONObject(settings.peers).toString())
        action(client)
    }

    private fun <T> work(task: suspend () -> T, success: (T) -> Unit) {
        if (busy) return
        // User operations preempt a slow poll and never wait behind a backlog.
        if (pollJob?.isActive == true) { pollJob?.cancel(); retireConnection() }
        busy = true; error = null; notice = null; progress = null
        viewModelScope.launch {
            try { success(withContext(Dispatchers.IO) { task() }) }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) { error = failure.message ?: failure.toString() }
            finally { busy = false; progress = null; retry = RefreshRetry(); startRefresh() }
        }
    }

    override fun onCleared() {
        foreground = false
        retireConnection()
        super.onCleared()
    }
}
