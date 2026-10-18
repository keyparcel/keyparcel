package dev.keyparcel.app

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.background
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.material3.pulltorefresh.PullToRefreshDefaults
import androidx.compose.material3.pulltorefresh.rememberPullToRefreshState
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import dev.keyparcel.bridge.Bridge
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter

class MainActivity : ComponentActivity() {
    private val model by viewModels<AppModel>()
    override fun onStart() { super.onStart(); model.setForeground(true) }
    override fun onStop() { model.setForeground(false); super.onStop() }
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent { KeyparcelTheme { ClientScreen(model) } }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ClientScreen(model: AppModel) {
    var connectionDetails by remember { mutableStateOf(false) }
    val pickFile = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument(), model::selectFile)
    val saveFile = rememberLauncherForActivityResult(SaveDocument(), model::saveFile)
    BackHandler(enabled = model.page != Page.Inbox) {
        model.back()
    }
    Scaffold(bottomBar = {
        if (model.ready && model.page != Page.Text && model.page != Page.File) {
            NavigationBar(modifier = Modifier.imePadding()) {
                for ((page, icon, tag) in listOf(
                    Triple(Page.Inbox, R.drawable.ic_inbox, "inbox-screen"),
                    Triple(Page.Send, R.drawable.ic_send, "send-screen"),
                    Triple(Page.Devices, R.drawable.ic_devices, "devices-screen"))) {
                    NavigationBarItem(selected = model.page == page,
                        onClick = { model.navigate(page) }, enabled = !model.busy,
                        icon = { Icon(painterResource(icon), contentDescription = null) },
                        label = { Text(page.name) }, modifier = Modifier.testTag(tag))
                }
            }
        }
    }) { insets ->
        Box(Modifier.fillMaxSize().padding(insets).consumeWindowInsets(insets).imePadding()) {
            Column(Modifier.fillMaxSize().padding(horizontal = 20.dp, vertical = 12.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Row(Modifier.fillMaxWidth().testTag("app-header"), horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically) {
                    Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Icon(painterResource(R.drawable.ic_keyparcel), contentDescription = null,
                            tint = Color.Unspecified, modifier = Modifier.size(32.dp))
                        Text("keyparcel", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                        val state = model.connectionState
                        val color = when(state) {
                            "Connected" -> Color(0xff287d38)
                            "Connecting" -> Color(0xff947000)
                            "Disconnected" -> MaterialTheme.colorScheme.error
                            else -> MaterialTheme.colorScheme.outline
                        }
                        TextButton(onClick = { connectionDetails = true }, contentPadding = PaddingValues(0.dp),
                            modifier = Modifier.size(48.dp).testTag("connection-status").semantics {
                                contentDescription = "Connection: $state"; stateDescription = state
                            }) {
                            Text(when(state) { "Connected" -> "●"; "Connecting" -> "↻"; "Disconnected" -> "⊗"; else -> "○" },color = color)
                        }
                    }
                    if (model.page != Page.Inbox) {
                        val parent = model.navigation.back().page.name
                        TextButton(onClick = model::back, enabled = !model.busy,
                            modifier = Modifier.testTag("back").semantics { contentDescription = if (model.page == Page.Text || model.page == Page.File) model.backLabel else "Back to $parent" }) { Text(model.backLabel) }
                    }
                    if (model.page != Page.Settings && model.page != Page.Text && model.page != Page.File) {
                        TextButton(onClick = { model.navigate(Page.Settings) }, enabled = !model.busy && model.ready,
                            modifier = Modifier.testTag("settings")) { Text("Settings") }
                    }
                }
                Column(Modifier.weight(1f).fillMaxWidth(),verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (model.busy) Row(Modifier.testTag("content-progress"), verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        CircularProgressIndicator(Modifier.size(16.dp).testTag("working"), strokeWidth = 2.dp)
                        Text(model.progress ?: "Working…", style = MaterialTheme.typography.labelSmall,
                            modifier = Modifier.testTag("operation-progress"))
                    } else if (model.refreshing && model.page != Page.Inbox && model.page != Page.Devices) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            PullToRefreshDefaults.Indicator(state = rememberPullToRefreshState(), isRefreshing = true)
                            RefreshCancel(model)
                        }
                    }
                    model.error?.let {
                        Surface(color = MaterialTheme.colorScheme.errorContainer) {
                            Column(Modifier.padding(12.dp)) {
                                Text(it, color = MaterialTheme.colorScheme.onErrorContainer, modifier = Modifier.testTag("error"))
                                TextButton(onClick = model::dismissError) { Text("Dismiss error") }
                            }
                        }
                    }
                    if (!model.ready) {
                        if (!model.busy) Button(onClick = { model.load() }) { Text("Retry initialization") }
                    } else when (model.page) {
                        Page.Inbox -> InboxScreen(model) { item ->
                            model.beginFileSave(item)
                            model.pendingSave?.let { saveFile.launch(it.filename) }
                        }
                        Page.Settings -> SettingsScreen(model)
                        Page.Devices -> DevicesScreen(model)
                        Page.Send -> SendScreen(model) { pickFile.launch(arrayOf("*/*")) }
                        Page.Text -> TextScreen(model) {
                            model.viewed?.let { view ->
                                model.beginFileSave(view.item,view)
                                model.pendingSave?.let { saveFile.launch(it.filename) }
                            }
                        }
                        Page.File -> FileScreen(model)
                    }
                }
            }
            model.notice?.let { message ->
                ConfirmationNotice(message, Modifier.align(Alignment.BottomCenter))
            }
        }
    }
    if (connectionDetails) {
        AlertDialog(onDismissRequest = { connectionDetails = false }, title = { Text("Connection") },
            text = { Text(when(model.connectionState) {
                "Connected" -> "Connected and authenticated. No connection loss has been observed."
                "Connecting" -> "Connecting and authenticating to the SSH server."
                "Disconnected" -> "Connection lost or failed. Reconnection will be attempted automatically."
                "Suspended" -> "Connection suspended while the app is in the background."
                else -> "Connect and approve a server in Settings."
            }) },
            confirmButton = { TextButton(onClick = { connectionDetails = false }) { Text("Close") } },
            dismissButton = { TextButton(onClick = { connectionDetails = false; model.refreshInbox() }) { Text("Refresh now") } })
    }
}

@Composable
internal fun ConfirmationNotice(message: String, modifier: Modifier = Modifier) {
    Box(modifier = modifier.padding(16.dp)
        .shadow(4.dp, MaterialTheme.shapes.small)
        .background(MaterialTheme.colorScheme.inverseSurface, MaterialTheme.shapes.small)
        .semantics { liveRegion = LiveRegionMode.Polite }) {
        Text(message, color = MaterialTheme.colorScheme.inverseOnSurface,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp).testTag("notice"))
    }
}

@Composable
private fun RefreshCancel(model: AppModel) {
    if (model.refreshing && !model.busy) TextButton(onClick = model::cancelRefresh,
        modifier = Modifier.testTag("cancel-refresh").semantics { contentDescription = "Cancel refresh" }) {
        Text("Cancel")
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun InboxScreen(model: AppModel, save: (InboxItem) -> Unit) {
    model.pendingDelete?.let {
        AlertDialog(onDismissRequest = model::cancelInboxDelete,
            title = { Text("Delete inbox copy?") },
            text = { Text("Delete this device's remote inbox copy without downloading it? Other recipients' copies and saved local files remain untouched.") },
            confirmButton = { TextButton(onClick = model::confirmInboxDelete, enabled = !model.busy,
                modifier = Modifier.testTag("confirm-inbox-delete")) { Text("Delete") } },
            dismissButton = { TextButton(onClick = model::cancelInboxDelete, enabled = !model.busy,
                modifier = Modifier.testTag("cancel-inbox-delete")) { Text("Cancel") } })
    }
    val context = LocalContext.current
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("Inbox", style = MaterialTheme.typography.titleLarge)
        IconButton(onClick = model::refreshInbox, enabled = !model.busy && model.settings.host?.trusted == true,
            modifier = Modifier.testTag("refresh-inbox")) {
            Icon(painterResource(R.drawable.ic_refresh), contentDescription = "Refresh inbox")
        }
        RefreshCancel(model)
    }
    PullToRefreshBox(isRefreshing = model.refreshing && !model.busy,
        onRefresh = { if (!model.busy && model.settings.host?.trusted == true) model.refreshInbox() },
        modifier = Modifier.fillMaxSize().testTag("inbox-pull-refresh")) {
        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            if (model.settings.host?.trusted != true) {
                Text("Set up your server and enroll this device through Settings to get started.")
            } else if (!model.inboxLoaded) {
                Text("Press Refresh to check for incoming transfers.")
            } else if (model.inbox.isEmpty() && model.rejected.isEmpty()) {
                Text("Inbox is empty", modifier = Modifier.testTag("empty-inbox"))
            }
            LazyColumn(Modifier.fillMaxSize().testTag("inbox-list"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                items(model.inbox, key = { it.id }) { item ->
                    var details by rememberSaveable(item.id) { mutableStateOf(false) }
                    Card(Modifier.fillMaxWidth().testTag("item-${item.id}")) {
                        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            Text(if (item.type == "text") "Text" else item.filename, style = MaterialTheme.typography.titleMedium)
                            Text("From ${item.from}", style = MaterialTheme.typography.titleSmall)
                            val size = Bridge.formatSize(item.size)
                            Text("${if (item.type == "text") "Text" else "File"} · $size", style = MaterialTheme.typography.bodySmall)
                            val time = OffsetDateTime.parse(item.uploaded).atZoneSameInstant(ZoneId.systemDefault())
                            Text("Uploaded ${time.format(DateTimeFormatter.ofPattern("uuuu-MM-dd HH:mm:ss xxx"))}",
                                style = MaterialTheme.typography.bodySmall)
                            if (item.size > MAX_VIEW_BYTES) Text(TOO_LARGE_TO_VIEW,
                                style = MaterialTheme.typography.bodySmall, modifier = Modifier.testTag("view-limit-${item.id}"))
                            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                if (item.size <= MAX_VIEW_BYTES) Button(onClick = { model.receive(item) }, enabled = !model.busy,
                                    modifier = Modifier.testTag("receive-${item.id}")) { Text("View") }
                                else Button(onClick = {}, enabled = false, modifier = Modifier.testTag("receive-${item.id}")
                                    .semantics { contentDescription = "View. $TOO_LARGE_TO_VIEW" }) { Text("View") }
                                Button(onClick = { save(item) }, enabled = !model.busy,
                                    modifier = Modifier.testTag("save-${item.id}")) { Text("Save file") }
                                TextButton(onClick = { details = !details }, modifier = Modifier.testTag("details-${item.id}")) {
                                    Text(if (details) "Hide details" else "Details")
                                }
                                TextButton(onClick = { model.beginInboxDelete(item) }, enabled = !model.busy,
                                    modifier = Modifier.testTag("delete-${item.id}").semantics {
                                        contentDescription = "Delete ${if (item.type == "text") "text" else item.filename} from ${item.from}"
                                    }) { Text("Delete") }
                            }
                            if (details) {
                                Text("Transfer ID", style = MaterialTheme.typography.labelMedium)
                                Row {
                                    Text(item.id, style = MaterialTheme.typography.bodySmall,
                                        modifier = Modifier.weight(1f).testTag("transfer-id-${item.id}"))
                                    CopyButton("Copy transfer ID", "copy-transfer-${item.id}") {
                                        copyText(context, clipboardText(item.id), "Transfer ID", false, model::clipboardError, { model.confirm("Copied") })
                                    }
                                }
                            }
                        }
                    }
                }
                items(model.rejected) { Text("Not verified: $it", color = MaterialTheme.colorScheme.error) }
            }
        }
    }
}

@Composable
private fun SettingsScreen(model: AppModel) {
    val context = LocalContext.current
    val host = model.settings.host
    var address by remember(host) { mutableStateOf(host?.address ?: "") }
    var port by remember(host) { mutableStateOf(host?.port?.toString() ?: "22") }
    var user by remember(host) { mutableStateOf(host?.user ?: "") }
    var root by remember(host) { mutableStateOf(host?.root ?: ".local/share/keyparcel") }
    var discovery by remember(host) { mutableStateOf(host?.discovery ?: ".ssh/authorized_keys") }
    var inboxMode by remember(host) { mutableStateOf(host?.inboxMode ?: "auto") }
    var forgetApproval by remember { mutableStateOf(false) }
    var advanced by rememberSaveable { mutableStateOf(false) }
    var formError by remember { mutableStateOf<String?>(null) }
    Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Settings", style = MaterialTheme.typography.titleLarge)
        Text("This device", style = MaterialTheme.typography.titleMedium)
        Row {
            Text(model.fingerprint, modifier = Modifier.weight(1f).testTag("identity-fingerprint"))
            CopyButton("Copy this device's fingerprint", "copy-identity-fingerprint") {
                copyText(context, clipboardText(model.fingerprint), "Device fingerprint", false, model::clipboardError, { model.confirm("Copied") })
            }
        }
        OutlinedTextField(value = model.publicKey, onValueChange = {}, readOnly = true,
            label = { Text("Public key for server enrollment") }, modifier = Modifier.fillMaxWidth().testTag("identity-public"),
            trailingIcon = {
                CopyButton("Copy this device's public enrollment key", "copy-identity-public") {
                    copyText(context, clipboardText(model.publicKey), "Public enrollment key", false, model::clipboardError, { model.confirm("Copied") })
                }
            })
        Text("Add this public key to your server's authorized keys. Losing this app's identity requires re-enrollment and prevents reading transfers addressed to it.")
        Text("Server", style = MaterialTheme.typography.titleMedium)
        Field("Address", "host-address", address, model.busy) { address = it }
        Field("Port", "host-port", port, model.busy) { port = it }
        Field("SSH username", "host-user", user, model.busy) { user = it }
        if (host?.trusted == true) {
            Text("Approved server: ${host.address}:${host.port}")
            val fingerprint = Bridge.serverFingerprint(host.key)
            Row {
                Text("Server fingerprint: $fingerprint", modifier = Modifier.weight(1f).testTag("server-fingerprint"))
                CopyButton("Copy the server fingerprint", "copy-server-fingerprint") {
                    copyText(context, clipboardText(fingerprint), "Server fingerprint", false, model::clipboardError, { model.confirm("Copied") })
                }
            }
            TextButton(onClick = { forgetApproval = true }, enabled = !model.busy,
                modifier = Modifier.testTag("forget-host")) { Text("Forget server approval") }
        }
        formError?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        Button(onClick = {
            try {
                val candidate = Host(address.trim(), port.toIntOrNull() ?: 0, user.trim(), root = root.trim(), discovery = discovery.trim(), inboxMode = inboxMode)
                candidate.validateConnection()
                formError = null
                model.connectHost(candidate)
            } catch (error: Exception) { formError = error.message }
        }, enabled = !model.busy,
            modifier = Modifier.testTag("connect-host")) { Text("Connect") }
        TextButton(onClick = { advanced = !advanced }, modifier = Modifier.testTag("advanced-settings")) {
            Text(if (advanced) "Hide advanced settings" else "Advanced settings")
        }
        if (advanced) {
            Field("Storage path", "host-root", root, model.busy) { root = it }
            Field("Discovery path", "host-discovery", discovery, model.busy) { discovery = it }
            Text("Inbox listing", style = MaterialTheme.typography.titleSmall)
            Row {
                FilterChip(selected = inboxMode == "auto", onClick = { inboxMode = "auto" }, enabled = !model.busy,
                    label = { Text("Automatic") }, modifier = Modifier.testTag("inbox-mode-auto"))
                FilterChip(selected = inboxMode == "sftp", onClick = { inboxMode = "sftp" }, enabled = !model.busy,
                    label = { Text("SFTP only") }, modifier = Modifier.testTag("inbox-mode-sftp"))
            }
            if (host?.trusted == true) {
                OutlinedTextField(value = host.key, onValueChange = {}, readOnly = true,
                    label = { Text("Approved server public key") }, modifier = Modifier.fillMaxWidth().testTag("host-key"))
            }
        }
    }
    model.pendingHost?.let { candidate ->
        val fingerprint = Bridge.serverFingerprint(candidate.key)
        AlertDialog(onDismissRequest = model::cancelHostApproval,
            title = { Text("Approve server") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("${candidate.address}:${candidate.port}", modifier = Modifier.testTag("candidate-server"))
                    Text(candidate.key.substringBefore(' '), modifier = Modifier.testTag("candidate-key-type"))
                    Row {
                        Text(fingerprint, modifier = Modifier.weight(1f).testTag("candidate-fingerprint"))
                        CopyButton("Copy the server fingerprint", "copy-host-candidate-fingerprint") {
                            copyText(context, clipboardText(fingerprint), "Server fingerprint", false, model::clipboardError, { model.confirm("Copied") })
                        }
                    }
                    Text("Compare this fingerprint with the server through a trusted source before approving.")
                }
            },
            confirmButton = { TextButton(onClick = model::approveHost, modifier = Modifier.testTag("approve-host")) { Text("Approve & connect") } },
            dismissButton = { TextButton(onClick = model::cancelHostApproval, modifier = Modifier.testTag("cancel-host")) { Text("Cancel") } })
    }
    if (forgetApproval) {
        AlertDialog(onDismissRequest = { forgetApproval = false },
            title = { Text("Forget server approval?") },
            text = { Text("Connect again to approve a key. Verify the server independently before accepting a replacement.") },
            confirmButton = { TextButton(onClick = { forgetApproval = false; model.forgetHostApproval() },
                modifier = Modifier.testTag("confirm-forget-host")) { Text("Forget") } },
            dismissButton = { TextButton(onClick = { forgetApproval = false }) { Text("Cancel") } })
    }
}

@Composable
private fun Field(label: String, tag: String, value: String, busy: Boolean, singleLine: Boolean = true, change: (String) -> Unit) {
    OutlinedTextField(value = value, onValueChange = change, enabled = !busy, singleLine = singleLine,
        label = { Text(label) }, modifier = Modifier.fillMaxWidth().testTag(tag))
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DevicesScreen(model: AppModel) {
    val context = LocalContext.current
    var candidate by remember { mutableStateOf<Device?>(null) }
    var name by remember { mutableStateOf("") }
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Devices", style = MaterialTheme.typography.titleLarge)
            IconButton(onClick = model::refreshDevices, enabled = !model.busy && model.settings.host?.trusted == true,
                modifier = Modifier.testTag("refresh-devices")) {
                Icon(painterResource(R.drawable.ic_refresh), contentDescription = "Refresh devices")
            }
            RefreshCancel(model)
        }
        PullToRefreshBox(isRefreshing = model.refreshing && !model.busy,
            onRefresh = { if (!model.busy && model.settings.host?.trusted == true) model.refreshDevices() },
            modifier = Modifier.fillMaxSize().testTag("devices-pull-refresh")) {
            Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("Compare fingerprints through a trusted channel before approval.")
                if (!model.devicesLoaded) Text("Press Refresh to discover enrolled devices.")
                else if (model.devices.isEmpty()) Text("No devices found")
                LazyColumn(Modifier.fillMaxSize().testTag("device-list"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    items(model.settings.peers.entries.toList(), key = { "approved-${it.key}" }) { (peerName, public) ->
                        Card(Modifier.fillMaxWidth()) {
                            Column(Modifier.padding(16.dp)) {
                                Text("$peerName · approved")
                                val fp = Bridge.fingerprint(public)
                                Row {
                                    Text(fp, style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
                                    CopyButton("Copy $peerName's fingerprint", "copy-approved-$peerName") {
                                        copyText(context, clipboardText(fp), "Device fingerprint", false, model::clipboardError, { model.confirm("Copied") })
                                    }
                                }
                                TextButton(onClick = { model.forget(peerName) }, enabled = !model.busy) { Text("Remove approval") }
                            }
                        }
                    }
                    items(model.devices.filter { it.key !in model.settings.peers.values }, key = { it.fingerprint }) { device ->
                        Card(Modifier.fillMaxWidth()) {
                            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                                Text(if (device.self) "This device" else device.comment.ifEmpty { "Unnamed device" })
                                Row {
                                    Text(device.fingerprint, modifier = Modifier.weight(1f).testTag("device-${device.fingerprint}"))
                                    CopyButton("Copy ${if (device.self) "this device's" else "the discovered device's"} fingerprint",
                                        "copy-device-${device.fingerprint}") {
                                        copyText(context, clipboardText(device.fingerprint), "Device fingerprint", false, model::clipboardError, { model.confirm("Copied") })
                                    }
                                }
                                if (!device.supported) Text("Unsupported transfer key")
                                else if (!device.self) Button(onClick = {
                                    candidate = device; name = model.suggestedPeerName(device)
                                }, enabled = !model.busy, modifier = Modifier.testTag("approve-${device.fingerprint}")) { Text("Approve…") }
                            }
                        }
                    }
                }
            }
        }
    }
    candidate?.let { device ->
        AlertDialog(onDismissRequest = { if (!model.busy) candidate = null },
            title = { Text("Approve device") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Row {
                        Text(device.fingerprint, modifier = Modifier.weight(1f))
                        CopyButton("Copy the selected device's fingerprint", "copy-candidate-fingerprint") {
                            copyText(context, clipboardText(device.fingerprint), "Device fingerprint", false, model::clipboardError, { model.confirm("Copied") })
                        }
                    }
                    Text("Compare this fingerprint through a trusted channel before approving.")
                    OutlinedTextField(value = name, onValueChange = { name = it }, singleLine = true,
                        label = { Text("Device name") }, modifier = Modifier.testTag("peer-name"))
                }
            },
            confirmButton = {
                TextButton(onClick = { model.approve(device, name); candidate = null },
                    enabled = !model.busy && name.isNotEmpty(), modifier = Modifier.testTag("confirm-peer")) { Text("Approve") }
            },
            dismissButton = { TextButton(onClick = { candidate = null }) { Text("Cancel") } })
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun SendScreen(model: AppModel, chooseFile: () -> Unit) {
    val (text, recipient, fileMode) = model.draft
    val editable = !model.busy && !model.sendLocked
    var adding by remember(model.settings, model.publicKey) { mutableStateOf(false) }
    val candidates = availableRecipients(model.draft, model.settings, model.publicKey)
    val sendLabel = if (model.draft.recipients.size > 1) "Send to ${model.draft.recipients.size} devices" else "Send"
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(if (fileMode) "Send file" else "Send text", style = MaterialTheme.typography.titleLarge)
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                FilterChip(selected = !fileMode, onClick = { model.draft = model.draft.copy(fileMode = false) }, enabled = editable,
                    label = { Text("Text") }, modifier = Modifier.testTag("send-text-mode"))
                FilterChip(selected = fileMode, onClick = { model.draft = model.draft.copy(fileMode = true) }, enabled = editable,
                    label = { Text("File") }, modifier = Modifier.testTag("send-file-mode"))
            }
            if (model.settings.host?.trusted != true) Text("Connect a server in Settings before sending.")
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                model.draft.recipients.forEach { name ->
                    InputChip(selected = true, onClick = { model.draft = model.draft.remove(name) }, enabled = editable,
                        label = { Text("${recipientLabel(name)} ×") },
                        modifier = Modifier.testTag("remove-recipient-$name").semantics { contentDescription = "Remove recipient ${recipientLabel(name)}" })
                }
                TextButton(onClick = { adding = true }, enabled = editable && candidates.isNotEmpty(),
                    modifier = Modifier.testTag("add-recipient")) {
                    Text(if (model.draft.recipients.isEmpty()) "Choose recipient…" else "+ Add another")
                }
            }
            SendFeedbackArea(model)
            if (fileMode) {
                OutlinedButton(onClick = chooseFile, enabled = editable,
                    modifier = Modifier.testTag("choose-file")) { Text("Choose file") }
                model.selectedFile?.let { file ->
                    Text(file.name, modifier = Modifier.testTag("selected-filename"))
                    Text(file.size?.let { Bridge.formatSize(it) } ?: "Size unknown", modifier = Modifier.testTag("selected-size"))
                }
            } else {
                OutlinedTextField(value = text, onValueChange = { model.draft = model.draft.copy(text = it) }, enabled = editable,
                    label = { Text("Message") }, minLines = 4, modifier = Modifier.fillMaxWidth().testTag("message"))
            }
            TextButton(onClick = model::discardDraft, enabled = !model.busy,
                modifier = Modifier.testTag("discard-draft")) { Text("Discard draft") }
        }
        if (fileMode) {
            Button(onClick = { model.sendFile(recipient) }, enabled = editable && model.settings.host?.trusted == true && recipient.isNotEmpty() && model.selectedFile != null,
                modifier = Modifier.testTag("send-file")) { Text(sendLabel) }
        } else {
            Button(onClick = { model.send(recipient, text) }, enabled = editable && model.settings.host?.trusted == true && recipient.isNotEmpty() && text.isNotEmpty(),
                modifier = Modifier.testTag("send-message")) { Text(sendLabel) }
        }
    }
    if (adding) AlertDialog(onDismissRequest = { adding = false }, title = { Text("Choose recipient") },
        text = { Column(Modifier.verticalScroll(rememberScrollState())) { candidates.forEach { name ->
            TextButton(onClick = { model.draft = model.draft.add(name); adding = false }, enabled = editable,
                modifier = Modifier.testTag("add-recipient-$name")) { Text(recipientLabel(name)) }
        } } }, confirmButton = {}, dismissButton = { TextButton(onClick = { adding = false }) { Text("Cancel") } })
}

@Composable
private fun SendFeedbackArea(model: AppModel) {
    val feedback = model.sendFeedback?.takeIf { it.visible } ?: return
    val context = LocalContext.current
    Column(Modifier.fillMaxWidth().testTag("send-feedback"), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        TextButton(onClick = model::toggleSendFeedback,
            modifier = Modifier.testTag("send-feedback-toggle").semantics {
                stateDescription = if (feedback.expanded) "Expanded" else "Collapsed"
            }) { Text("${if (feedback.expanded) "▾" else "▸"} ${feedback.report.headline()}") }
        if (feedback.expanded) feedback.report.deliveries.forEach { delivery ->
            Text("${recipientLabel(delivery.recipient)}: ${delivery.outcome()}",
                modifier = Modifier.testTag("send-outcome-${delivery.recipient}"))
            if (delivery.id.isNotEmpty()) {
                Text(delivery.id, modifier = Modifier.testTag("sent-id-${delivery.recipient}"))
                TextButton(onClick = {
                    copyText(context, clipboardText(delivery.id), "Transfer ID", false, model::clipboardError, { model.confirm("Copied") })
                }, modifier = Modifier.testTag("copy-sent-id-${delivery.recipient}").semantics {
                    contentDescription = "Copy sent transfer ID for ${recipientLabel(delivery.recipient)}"
                }) { Text("Copy ID") }
            }
        }
        if (model.sendLocked) {
            Button(onClick = model::retrySend, enabled = !model.busy && model.canRetrySend,
                modifier = Modifier.testTag("retry-send")) { Text("Retry failed recipients") }
            TextButton(onClick = model::editSend, enabled = !model.busy,
                modifier = Modifier.testTag("edit-send")) { Text("Edit draft") }
        }
    }
}

@Composable
private fun TextScreen(model: AppModel, save: () -> Unit) {
    val viewed = model.viewed ?: return
    val context = LocalContext.current
    val clipboard = LocalClipboard.current
    val selectionClipboard = remember(clipboard, context, model) {
        ReceivedTextClipboard(clipboard) { copyText(context, it, "Received text", true, model::clipboardError, { model.confirm("Copied") }) }
    }
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Parcel", style = MaterialTheme.typography.titleLarge)
        Text("The remote copy remains until you choose Remove & close.")
        if (viewed.preview?.truncated == true) Text("Showing only the beginning of this large text. The complete transfer remains on the server.")
        if (viewed.preview == null) Text(UNSUPPORTED_PREVIEW, modifier = Modifier.testTag("unsupported-preview"))
        else {
            CompositionLocalProvider(LocalClipboard provides selectionClipboard) {
                OutlinedTextField(value = requireNotNull(viewed.preview).text, onValueChange = {}, readOnly = true,
                    modifier = Modifier.fillMaxWidth().weight(1f).testTag("received-text"),
                    trailingIcon = {
                        CopyButton("Copy the complete received text", "copy-received-text", enabled = !model.busy) {
                            copyText(context, viewed.content.clipboard(), "Received text", true, model::clipboardError, { model.confirm("Copied") })
                        }
                    })
            }
        }
        Button(onClick = save, enabled = !model.busy, modifier = Modifier.testTag("save-view")) { Text("Save file") }
        Button(onClick = { model.removeReceived() }, enabled = !model.busy,
            modifier = Modifier.testTag("remove-close")) { Text("Remove & close") }
    }
}

@Composable
private fun FileScreen(model: AppModel) {
    val file = model.savedFile ?: return
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("File saved — remote copy retained", style = MaterialTheme.typography.titleLarge)
        Text("Transfer: ${file.name}", modifier = Modifier.testTag("saved-file"))
        Text("The remote copy remains until you choose Remove & close.")
        Button(onClick = { model.removeReceived() }, enabled = !model.busy,
            modifier = Modifier.testTag("remove-close")) { Text("Remove & close") }
    }
}
