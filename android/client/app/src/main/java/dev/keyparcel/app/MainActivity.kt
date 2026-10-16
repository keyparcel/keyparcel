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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
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
        setContent { MaterialTheme { ClientScreen(model) } }
    }
}

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
        Column(Modifier.fillMaxSize().padding(insets).consumeWindowInsets(insets).imePadding()
            .padding(horizontal = 20.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text("keyparcel", style = MaterialTheme.typography.headlineMedium, modifier = Modifier.weight(1f))
                if (model.page != Page.Inbox) {
                    val parent = model.navigation.back().page.name
                    TextButton(onClick = model::back, enabled = !model.busy,
                        modifier = Modifier.testTag("back").semantics { contentDescription = "Back to $parent" }) { Text("Back") }
                }
                if (model.page != Page.Settings && model.page != Page.Text && model.page != Page.File) {
                    TextButton(onClick = { model.navigate(Page.Settings) }, enabled = !model.busy && model.ready,
                        modifier = Modifier.testTag("settings")) { Text("Settings") }
                }
            }
            if (model.ready) {
                TextButton(onClick = { connectionDetails = true }, modifier = Modifier.testTag("connection-status")) {
                    Text(model.syncStatus, style = MaterialTheme.typography.labelSmall)
                }
            }
            if (model.busy) {
                LinearProgressIndicator(Modifier.fillMaxWidth().testTag("working"))
                Text(model.progress ?: "Working…", modifier = Modifier.testTag("operation-progress"))
            }
            model.error?.let {
                Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("error"))
            }
            model.notice?.let { Text(it, modifier = Modifier.testTag("notice")) }
            if (!model.ready) {
                if (!model.busy) Button(onClick = { model.load() }) { Text("Retry initialization") }
            } else when (model.page) {
                Page.Inbox -> InboxScreen(model) { item ->
                    model.beginFileSave(item)
                    saveFile.launch(item.filename)
                }
                Page.Settings -> SettingsScreen(model)
                Page.Devices -> DevicesScreen(model)
                Page.Send -> SendScreen(model) { pickFile.launch(arrayOf("*/*")) }
                Page.Text -> TextScreen(model)
                Page.File -> FileScreen(model)
            }
        }
    }
    if (connectionDetails) {
        AlertDialog(onDismissRequest = { connectionDetails = false }, title = { Text("Connection") },
            text = { Text(model.syncError ?: when(model.syncStatus) {
                "Current" -> "Inbox and Devices refresh automatically."
                "Not connected" -> "Connect and approve a server in Settings."
                else -> "Keeping the last successful data while connecting or refreshing."
            }) },
            confirmButton = { TextButton(onClick = { connectionDetails = false }) { Text("Close") } },
            dismissButton = { TextButton(onClick = { connectionDetails = false; model.refreshInbox() }) { Text("Refresh now") } })
    }
}

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
    Text("Inbox", style = MaterialTheme.typography.titleLarge)
    Button(onClick = { model.refreshInbox() }, enabled = !model.busy && model.settings.host?.trusted == true,
        modifier = Modifier.testTag("refresh-inbox")) { Text("Refresh") }
    if (model.settings.host?.trusted != true) {
        Text("Set up your server and enroll this device through Settings to get started.")
    } else if (!model.inboxLoaded) {
        Text("Press Refresh to check for incoming transfers.")
    } else if (model.inbox.isEmpty() && model.rejected.isEmpty()) {
        Text("Inbox is empty", modifier = Modifier.testTag("empty-inbox"))
    }
    LazyColumn(Modifier.testTag("inbox-list"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
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
                    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        if (item.type == "text") {
                            Button(onClick = { model.receive(item) }, enabled = !model.busy,
                                modifier = Modifier.testTag("receive-${item.id}")) { Text("Receive") }
                        } else Button(onClick = { save(item) }, enabled = !model.busy,
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
                                copyText(context, clipboardText(item.id), "Transfer ID", false, model::clipboardError)
                            }
                        }
                    }
                }
            }
        }
        items(model.rejected) { Text("Not verified: $it", color = MaterialTheme.colorScheme.error) }
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
    var forgetApproval by remember { mutableStateOf(false) }
    var advanced by rememberSaveable { mutableStateOf(false) }
    var formError by remember { mutableStateOf<String?>(null) }
    Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Settings", style = MaterialTheme.typography.titleLarge)
        Text("This device", style = MaterialTheme.typography.titleMedium)
        Row {
            Text(model.fingerprint, modifier = Modifier.weight(1f).testTag("identity-fingerprint"))
            CopyButton("Copy this device's fingerprint", "copy-identity-fingerprint") {
                copyText(context, clipboardText(model.fingerprint), "Device fingerprint", false, model::clipboardError)
            }
        }
        OutlinedTextField(value = model.publicKey, onValueChange = {}, readOnly = true,
            label = { Text("Public key for server enrollment") }, modifier = Modifier.fillMaxWidth().testTag("identity-public"),
            trailingIcon = {
                CopyButton("Copy this device's public enrollment key", "copy-identity-public") {
                    copyText(context, clipboardText(model.publicKey), "Public enrollment key", false, model::clipboardError)
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
                    copyText(context, clipboardText(fingerprint), "Server fingerprint", false, model::clipboardError)
                }
            }
            TextButton(onClick = { forgetApproval = true }, enabled = !model.busy,
                modifier = Modifier.testTag("forget-host")) { Text("Forget server approval") }
        }
        formError?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        Button(onClick = {
            try {
                val candidate = Host(address.trim(), port.toIntOrNull() ?: 0, user.trim(), root = root.trim(), discovery = discovery.trim())
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
                            copyText(context, clipboardText(fingerprint), "Server fingerprint", false, model::clipboardError)
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

@Composable
private fun DevicesScreen(model: AppModel) {
    val context = LocalContext.current
    var candidate by remember { mutableStateOf<Device?>(null) }
    var name by remember { mutableStateOf("") }
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Devices", style = MaterialTheme.typography.titleLarge)
        Button(onClick = { model.refreshDevices() }, enabled = !model.busy && model.settings.host?.trusted == true,
            modifier = Modifier.testTag("refresh-devices")) { Text("Refresh") }
        Text("Compare fingerprints through a trusted channel before approval.")
        if (!model.devicesLoaded) Text("Press Refresh to discover enrolled devices.")
        else if (model.devices.isEmpty()) Text("No devices found")
        LazyColumn(Modifier.testTag("device-list"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            items(model.settings.peers.entries.toList(), key = { "approved-${it.key}" }) { (peerName, public) ->
                Card(Modifier.fillMaxWidth()) {
                    Column(Modifier.padding(16.dp)) {
                        Text("$peerName · approved")
                        val fp = Bridge.fingerprint(public)
                        Row {
                            Text(fp, style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
                            CopyButton("Copy $peerName's fingerprint", "copy-approved-$peerName") {
                                copyText(context, clipboardText(fp), "Device fingerprint", false, model::clipboardError)
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
                                copyText(context, clipboardText(device.fingerprint), "Device fingerprint", false, model::clipboardError)
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
    candidate?.let { device ->
        AlertDialog(onDismissRequest = { if (!model.busy) candidate = null },
            title = { Text("Approve device") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Row {
                        Text(device.fingerprint, modifier = Modifier.weight(1f))
                        CopyButton("Copy the selected device's fingerprint", "copy-candidate-fingerprint") {
                            copyText(context, clipboardText(device.fingerprint), "Device fingerprint", false, model::clipboardError)
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

@Composable
private fun SendScreen(model: AppModel, chooseFile: () -> Unit) {
    val (text, recipient, fileMode) = model.draft
    Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(if (fileMode) "Send file" else "Send text", style = MaterialTheme.typography.titleLarge)
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            FilterChip(selected = !fileMode, onClick = { model.draft = model.draft.copy(fileMode = false) }, enabled = !model.busy,
                label = { Text("Text") }, modifier = Modifier.testTag("send-text-mode"))
            FilterChip(selected = fileMode, onClick = { model.draft = model.draft.copy(fileMode = true) }, enabled = !model.busy,
                label = { Text("File") }, modifier = Modifier.testTag("send-file-mode"))
        }
        if (model.settings.host?.trusted != true) Text("Connect a server in Settings before sending.")
        OutlinedButton(onClick = { model.draft = model.draft.copy(recipient = "self") }, enabled = !model.busy,
            modifier = Modifier.testTag("recipient-self")) { Text(if (recipient == "self") "✓ This device" else "This device") }
        if (model.settings.peers.isEmpty()) Text("Send to this device, or approve another device in Devices.")
        model.settings.peers.keys.sorted().forEach { name ->
            OutlinedButton(onClick = { model.draft = model.draft.copy(recipient = name) }, enabled = !model.busy,
                modifier = Modifier.testTag("recipient-$name")) { Text(if (recipient == name) "✓ To $name" else "To $name") }
        }
        if (fileMode) {
            OutlinedButton(onClick = chooseFile, enabled = !model.busy,
                modifier = Modifier.testTag("choose-file")) { Text("Choose file") }
            model.selectedFile?.let { file ->
                Text(file.name, modifier = Modifier.testTag("selected-filename"))
                Text(file.size?.let { Bridge.formatSize(it) } ?: "Size unknown", modifier = Modifier.testTag("selected-size"))
            }
            Button(onClick = { model.sendFile(recipient) }, enabled = !model.busy && model.settings.host?.trusted == true && recipient.isNotEmpty() && model.selectedFile != null,
                modifier = Modifier.testTag("send-file")) { Text("Send file") }
        } else {
            OutlinedTextField(value = text, onValueChange = { model.draft = model.draft.copy(text = it) }, enabled = !model.busy,
                label = { Text("Message") }, minLines = 4, modifier = Modifier.fillMaxWidth().testTag("message"))
            Button(onClick = { model.send(recipient, text) }, enabled = !model.busy && model.settings.host?.trusted == true && recipient.isNotEmpty() && text.isNotEmpty(),
                modifier = Modifier.testTag("send-message")) { Text("Send") }
        }
        TextButton(onClick = model::discardDraft, enabled = !model.busy,
            modifier = Modifier.testTag("discard-draft")) { Text("Discard draft") }
    }
}

@Composable
private fun TextScreen(model: AppModel) {
    val viewed = model.viewed ?: return
    val context = LocalContext.current
    val clipboard = LocalClipboard.current
    val selectionClipboard = remember(clipboard, context, model) {
        ReceivedTextClipboard(clipboard) { copyText(context, it, "Received text", true, model::clipboardError) }
    }
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Received text", style = MaterialTheme.typography.titleLarge)
        Text("The remote copy remains until you choose Remove & close. Back keeps it in your inbox.")
        if (viewed.preview.truncated) Text("Showing only the beginning of this large text. The complete transfer remains on the server.")
        Button(onClick = { model.removeReceived() }, enabled = !model.busy,
            modifier = Modifier.testTag("remove-close")) { Text("Remove & close") }
        CompositionLocalProvider(LocalClipboard provides selectionClipboard) {
            OutlinedTextField(value = viewed.preview.text, onValueChange = {}, readOnly = true,
                modifier = Modifier.fillMaxWidth().weight(1f).testTag("received-text"),
                trailingIcon = {
                    CopyButton("Copy the complete received text", "copy-received-text", enabled = !model.busy) {
                        copyText(context, viewed.clipboard, "Received text", true, model::clipboardError)
                    }
                })
        }
    }
}

@Composable
private fun FileScreen(model: AppModel) {
    val file = model.savedFile ?: return
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("File saved", style = MaterialTheme.typography.titleLarge)
        Text("Transfer: ${file.name}", modifier = Modifier.testTag("saved-file"))
        Text("The remote copy remains until you choose Remove & close. Back keeps it in your inbox.")
        Button(onClick = { model.removeReceived() }, enabled = !model.busy,
            modifier = Modifier.testTag("remove-close")) { Text("Remove & close") }
    }
}
