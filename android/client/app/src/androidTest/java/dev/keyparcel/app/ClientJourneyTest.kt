package dev.keyparcel.app

import android.graphics.Bitmap
import android.accessibilityservice.AccessibilityServiceInfo
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.ContextWrapper
import android.os.Bundle
import android.os.Build
import android.os.PersistableBundle
import android.provider.DocumentsContract
import kotlinx.coroutines.runBlocking
import android.view.accessibility.AccessibilityNodeInfo
import android.view.KeyEvent
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.activity.result.contract.ActivityResultContracts
import androidx.lifecycle.ViewModelProvider
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsAnimationCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.keyparcel.bridge.Bridge
import java.io.File
import org.junit.Rule
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class ClientJourneyTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()
    private val arguments get() = InstrumentationRegistry.getArguments()
    private val input = "  Keyparcel 世界 🌍\n\n"
    private val imeAnimations = mutableSetOf<WindowInsetsAnimationCompat>()
    @Volatile private var imeAnimating = false

    @Before fun waitForAppFocus() {
        compose.runOnUiThread {
            ViewCompat.setWindowInsetsAnimationCallback(compose.activity.window.decorView,
                object : WindowInsetsAnimationCompat.Callback(DISPATCH_MODE_CONTINUE_ON_SUBTREE) {
                    override fun onPrepare(animation: WindowInsetsAnimationCompat) {
                        if (animation.typeMask and WindowInsetsCompat.Type.ime() != 0) {
                            imeAnimations.add(animation)
                            imeAnimating = true
                        }
                    }
                    override fun onProgress(insets: WindowInsetsCompat,
                        runningAnimations: MutableList<WindowInsetsAnimationCompat>) = insets
                    override fun onEnd(animation: WindowInsetsAnimationCompat) {
                        imeAnimations.remove(animation)
                        imeAnimating = imeAnimations.isNotEmpty()
                    }
                })
        }
        val automation = InstrumentationRegistry.getInstrumentation().uiAutomation
        automation.serviceInfo = automation.serviceInfo.apply {
            flags = flags or AccessibilityServiceInfo.FLAG_REPORT_VIEW_IDS
        }
        compose.waitUntil(60_000) {
            if (compose.activity.hasWindowFocus()) true
            else {
                val root = automation.rootInActiveWindow
                val systemStartup = listOf("Quickstep isn't responding", "System UI isn't responding")
                    .any { root?.findAccessibilityNodeInfosByText(it)?.isNotEmpty() == true }
                if (systemStartup) root?.findAccessibilityNodeInfosByText("Wait")?.forEach {
                    it.performAction(AccessibilityNodeInfo.ACTION_CLICK)
                }
                false
            }
        }
    }

    private fun arg(name: String) = requireNotNull(arguments.getString(name)) { "Missing fixture argument $name" }

    private fun waitFor(tag: String) {
        try {
            compose.waitUntil(120_000) { compose.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty() }
        } catch (timeout: ComposeTimeoutException) {
            screenshot("failure")
            val model = ViewModelProvider(compose.activity)[AppModel::class.java]
            throw AssertionError("Waiting for $tag: page=${model.page}, busy=${model.busy}, error=${model.error}, pendingSave=${model.pendingSave?.item?.id}", timeout)
        }
    }

    private fun finished() {
        compose.waitUntil(120_000) { compose.onAllNodesWithTag("working").fetchSemanticsNodes().isEmpty() }
    }

    private fun click(tag: String) {
        val model = ViewModelProvider(compose.activity)[AppModel::class.java]
        val revision = model.refreshRevision
        compose.onNodeWithTag(tag).performClick()
        if (tag == "refresh-inbox" || tag == "refresh-devices") {
            compose.waitUntil(150_000) { model.refreshRevision > revision }
        }
    }
    private fun field(tag: String, text: String) {
        compose.onNodeWithTag(tag).performScrollTo().performTextReplacement(text)
        // Compose idle does not include the system keyboard's layout animation.
        compose.waitUntil(30_000) {
            !imeAnimating && ViewCompat.getRootWindowInsets(compose.activity.window.decorView)
                ?.isVisible(WindowInsetsCompat.Type.ime()) == true
        }
    }
    private fun value(tag: String) = compose.onNodeWithTag(tag).fetchSemanticsNode()
        .config[SemanticsProperties.EditableText].text
    private fun refresh() { waitFor("refresh-inbox"); click("refresh-inbox"); finished() }

    private fun pickerNode(predicate: (AccessibilityNodeInfo) -> Boolean): AccessibilityNodeInfo? {
        fun find(node: AccessibilityNodeInfo?): AccessibilityNodeInfo? {
            if (node == null) return null
            if (node.isVisibleToUser && node.isEnabled && predicate(node)) return node
            for (i in 0 until node.childCount) find(node.getChild(i))?.let { return it }
            return null
        }
        return find(InstrumentationRegistry.getInstrumentation().uiAutomation.rootInActiveWindow)
    }

    private fun pickerClick(scrollFiles: Boolean = false, predicate: (AccessibilityNodeInfo) -> Boolean) {
        var forward = true
        compose.waitUntil(30_000) {
            var node = pickerNode(predicate)
            while (node != null && !node.isClickable) node = node.parent
            if (node == null && scrollFiles) {
                val list = pickerNode { it.viewIdResourceName?.endsWith(":id/dir_list") == true && it.isScrollable }
                val action = if (forward) AccessibilityNodeInfo.ACTION_SCROLL_FORWARD else AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD
                if (list?.performAction(action) == false) forward = !forward
            }
            node?.let { it.isVisibleToUser && it.isEnabled && it.performAction(AccessibilityNodeInfo.ACTION_CLICK) } == true
        }
    }

    private fun pickerRoot() {
        pickerClick { it.contentDescription?.toString() == "Show roots" }
        pickerClick { node ->
            node.text?.toString() == "keyparcel test files" && generateSequence(node.parent) { it.parent }
                .any { it.viewIdResourceName?.endsWith(":id/roots_list") == true }
        }
    }

    private fun pickerExistingFile(name: String) = pickerClick(scrollFiles = true) { node ->
        node.text?.toString() == name && generateSequence(node.parent) { it.parent }
            .any { it.viewIdResourceName?.endsWith(":id/dir_list") == true }
    }

    private fun appHasFocus(): Boolean = compose.activity.hasWindowFocus() &&
        InstrumentationRegistry.getInstrumentation().uiAutomation.rootInActiveWindow
            ?.packageName?.toString() == compose.activity.packageName

    private fun pickerReturned() {
        compose.waitUntil(30_000) { appHasFocus() }
        compose.waitForIdle(); finished()
    }

    private fun pickerCancel() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        compose.waitUntil(30_000) { pickerNode { it.packageName?.toString() == "com.android.documentsui" } != null }
        // Back first dismisses a filename keyboard or open drawer, then the picker.
        for (attempt in 1..3) {
            if (appHasFocus()) break
            instrumentation.sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
            try {
                compose.waitUntil(5_000) { appHasFocus() }
                // Stop immediately: focus may flicker during the closing animation.
                break
            } catch (_: ComposeTimeoutException) {
                check(pickerNode { it.packageName?.toString() == "com.android.documentsui" } != null) {
                    "Picker lost focus without returning to the app"
                }
            }
        }
        pickerReturned()
    }

    private fun saveFile(id: String, outputName: String) {
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("save-$id"))
        click("save-$id")
        var filename: AccessibilityNodeInfo? = null
        compose.waitUntil(30_000) { pickerNode { it.className?.toString() == "android.widget.EditText" }.also { filename = it } != null }
        check(filename!!.text.toString() == "file-世界.bin") { "Save dialog did not suggest the original filename: ${filename!!.text}" }
        pickerRoot()
        filename = pickerNode { it.className?.toString() == "android.widget.EditText" }
        check(filename?.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, Bundle().apply {
            putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, outputName)
        }) == true) { "Could not rename destination in system save dialog" }
        pickerClick { it.text?.toString()?.equals("Save", ignoreCase = true) == true }
        pickerReturned()
    }

    private fun receive(id: String) {
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("receive-$id"))
        click("receive-$id")
        waitFor("received-text")
        check(value("received-text") == input) { "Received text changed its Unicode or whitespace" }
    }

    private fun withClipboard(check: (ClipboardManager) -> Unit) {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val clipboard = context.getSystemService(ClipboardManager::class.java)
        var previous: ClipData? = null
        compose.runOnIdle { previous = clipboard.primaryClip }
        try { check(clipboard) }
        finally {
            compose.runOnIdle {
                if (previous != null) {
                    // Keep restored user clipboard content out of screenshot previews.
                    previous!!.description.extras = PersistableBundle(previous!!.description.extras ?: PersistableBundle()).apply {
                        putBoolean("android.content.extra.IS_SENSITIVE", true)
                    }
                    clipboard.setPrimaryClip(previous!!)
                }
                else if (Build.VERSION.SDK_INT >= 28) clipboard.clearPrimaryClip()
                else clipboard.setPrimaryClip(ClipData.newPlainText("", ""))
            }
        }
    }

    private fun assertClipboard(clipboard: ClipboardManager, expected: String) {
        compose.waitUntil(10_000) { clipboard.primaryClip?.getItemAt(0)?.text?.toString() == expected }
    }

    private fun reportIdentity() {
        val public = value("identity-public")
        val bundle = Bundle().apply {
            putString("phone_public", public)
            putString("phone_fingerprint", Bridge.fingerprint(public))
        }
        InstrumentationRegistry.getInstrumentation().sendStatus(0, bundle)
    }

    private fun screenshot(name: String) {
        compose.waitForIdle()
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val bitmap = requireNotNull(instrumentation.uiAutomation.takeScreenshot()) { "Could not capture phone screen" }
        try {
            File(instrumentation.targetContext.filesDir, "$name.png").outputStream().use {
                check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        } finally { bitmap.recycle() }
    }

    @Test fun enrollment() {
        waitFor("refresh-inbox")
        compose.onNodeWithTag("refresh-inbox").assertIsNotEnabled()
        click("settings")
        waitFor("identity-public")
        reportIdentity()
        withClipboard { clipboard ->
            val public = value("identity-public")
            compose.onNodeWithTag("copy-identity-public").performScrollTo().performClick()
            assertClipboard(clipboard, public)
            compose.onNodeWithTag("copy-identity-fingerprint").performScrollTo().performClick()
            assertClipboard(clipboard, Bridge.fingerprint(public))
        }
        field("host-address", "127.0.0.1")
        field("host-port", arg("port"))
        field("host-user", "parcel")
        compose.onNodeWithTag("host-key").assertDoesNotExist()
        compose.onNodeWithTag("connect-host").performScrollTo().performClick()
        waitFor("candidate-fingerprint"); finished()
        compose.onNodeWithTag("candidate-server").assertTextEquals("127.0.0.1:" + arg("port"))
        compose.onNodeWithTag("candidate-key-type").assertTextEquals("ssh-ed25519")
        compose.onNodeWithTag("candidate-fingerprint").assertTextEquals(Bridge.serverFingerprint(arg("host_key")))
        withClipboard { clipboard ->
            click("copy-host-candidate-fingerprint")
            assertClipboard(clipboard, Bridge.serverFingerprint(arg("host_key")))
        }
        click("cancel-host")
        check(SettingsStore(compose.activity).load().host == null) { "Cancel established server trust" }
        compose.onNodeWithTag("connect-host").performScrollTo().performClick()
        waitFor("approve-host"); finished()
        screenshot("server-approval")
        click("approve-host")
        finished()
        compose.onNodeWithTag("error").assertTextContains("unable to authenticate", substring = true)
        check(Bridge.serverFingerprint(SettingsStore(compose.activity).load().host!!.key) == Bridge.serverFingerprint(arg("host_key"))) {
            "Explicit approval was lost when device enrollment was missing"
        }
        withClipboard { clipboard ->
            compose.onNodeWithTag("copy-server-fingerprint").performScrollTo().performClick()
            assertClipboard(clipboard, Bridge.serverFingerprint(arg("host_key")))
        }
        click("back")
        screenshot("setup")
    }

    @Test fun connectRememberedServer() {
        waitFor("refresh-inbox")
        click("settings")
        compose.onNodeWithTag("connect-host").performScrollTo().performClick()
        finished()
        compose.onNodeWithTag("approve-host").assertDoesNotExist()
        compose.onNodeWithTag("notice").assertTextEquals("Server connected. Inbox and Devices update automatically.")
    }

    @Test fun foregroundRefresh() {
        waitFor("refresh-inbox")
        val model = ViewModelProvider(compose.activity)[AppModel::class.java]
        compose.waitUntil(150_000) { model.syncStatus == "Current" }
        click("send-screen"); field("message", input)
        val revision = model.refreshRevision
        compose.runOnIdle { model.setForeground(false) }
        check(model.syncStatus == "Stale") { "Background suspension did not mark cached data stale" }
        check(model.inboxLoaded && model.devicesLoaded) { "Background suspension discarded cached data" }
        compose.runOnIdle { model.setForeground(true) }
        compose.waitUntil(150_000) { model.refreshRevision > revision && model.syncStatus == "Current" }
        check(value("message") == input) { "Foreground reconnection discarded the draft" }
        compose.onNodeWithTag("discard-draft").performScrollTo().performClick()
    }

    @Test fun changedServerKey() {
        waitFor("refresh-inbox")
        val before = SettingsStore(compose.activity).load()
        click("refresh-inbox"); finished()
        compose.onNodeWithTag("error").assertTextContains("host key does not match trusted key", substring = true)
        check(SettingsStore(compose.activity).load() == before) { "Key mismatch replaced saved trust" }
        compose.onNodeWithTag("approve-host").assertDoesNotExist()
        click("settings")
        compose.onNodeWithTag("connect-host").performScrollTo().performClick()
        finished()
        compose.onNodeWithTag("error").assertTextContains("host key does not match trusted key", substring = true)
        check(SettingsStore(compose.activity).load().host!!.key == before.host!!.key) { "Connect rediscovered a changed key instead of enforcing the pin" }
        compose.onNodeWithTag("approve-host").assertDoesNotExist()
        screenshot("host-key-changed")
        compose.onNodeWithTag("forget-host").performScrollTo().performClick()
        click("confirm-forget-host"); finished()
        check(!SettingsStore(compose.activity).load().host!!.trusted) { "Forgetting server approval retained the pin" }
        compose.onNodeWithTag("connect-host").performScrollTo().performClick()
        waitFor("candidate-fingerprint"); finished()
        compose.onNodeWithTag("candidate-fingerprint").assertTextEquals(Bridge.serverFingerprint(arg("host_key")))
        click("approve-host"); finished()
        check(Bridge.serverFingerprint(SettingsStore(compose.activity).load().host!!.key) == Bridge.serverFingerprint(arg("host_key"))) {
            "Explicit re-verification did not remember the new key"
        }
        check(SettingsStore(compose.activity).load().peers == before.peers) { "Server approval reset changed device approvals" }
    }

    @Test fun changedEndpoint() {
        waitFor("refresh-inbox")
        val before = SettingsStore(compose.activity).load()
        click("send-screen"); click("recipient-self")
        field("message", input)
        repeat(3) {
            click("settings")
            field("host-port", arg("port"))
            compose.onNodeWithTag("connect-host").performScrollTo().assertIsDisplayed().performClick()
            waitFor("candidate-fingerprint"); finished()
            compose.onNodeWithTag("candidate-server").assertTextEquals("127.0.0.1:" + arg("port"))
            check(SettingsStore(compose.activity).load() == before) { "Unapproved endpoint replaced saved settings" }
            click("cancel-host")
            check(SettingsStore(compose.activity).load() == before) { "Endpoint cancellation changed trust" }
            click("back")
            compose.onNodeWithTag("send-screen").assertIsSelected()
            check(value("message") == input) { "Canceled host approval discarded the send draft" }
            compose.onNodeWithTag("recipient-self").assertTextEquals("✓ This device")
        }
        click("settings")
        field("host-port", arg("port"))
        compose.onNodeWithTag("connect-host").performScrollTo().performClick()
        waitFor("approve-host"); finished()
        click("approve-host"); finished()
        check(SettingsStore(compose.activity).load().host!!.port.toString() == arg("port")) { "Endpoint approval did not save the new port" }
        check(SettingsStore(compose.activity).load().peers == before.peers) { "Changing endpoint lost device approvals" }
        click("send-screen")
        check(value("message") == input) { "Changing host discarded draft contents" }
        compose.onNodeWithTag("recipient-self").assertTextEquals("This device")
        compose.onNodeWithTag("send-message").assertIsNotEnabled()
        compose.onNodeWithTag("discard-draft").performScrollTo().performClick()
    }

    @Test fun discoveryTimeout() {
        waitFor("refresh-inbox")
        val before = SettingsStore(compose.activity).load()
        click("settings")
        field("host-port", arg("port"))
        val model = ViewModelProvider(compose.activity)[AppModel::class.java]
        compose.runOnIdle { model.requestTimeoutMillis = 1200 }
        try {
            compose.onNodeWithTag("connect-host").performScrollTo().performClick()
            finished()
            compose.onNodeWithTag("error").assertTextContains("Request timed out", substring = true)
            compose.onNodeWithTag("connect-host").assertIsEnabled()
            compose.onNodeWithTag("approve-host").assertDoesNotExist()
            check(SettingsStore(compose.activity).load() == before) { "Failed discovery changed server trust" }
        } finally { compose.runOnIdle { model.requestTimeoutMillis = 120_000 } }
        click("back")
    }

    @Test fun exchange() {
        waitFor("refresh-inbox")
        val liveModel = ViewModelProvider(compose.activity)[AppModel::class.java]
        compose.waitUntil(150_000) { liveModel.inboxLoaded && liveModel.devicesLoaded }
        check(liveModel.syncStatus == "Current") { "Initial automatic refresh did not publish current data" }
        compose.onNodeWithTag("received-text").assertDoesNotExist()
        check(compose.onAllNodesWithText("Not verified:", substring = true).fetchSemanticsNodes().isNotEmpty()) {
            "Inbox did not report the unapproved sender"
        }
        click("devices-screen")
        compose.onNodeWithTag("devices-screen").assertIsSelected()
        click("settings")
        compose.onNodeWithTag("host-root").assertDoesNotExist()
        compose.onNodeWithTag("host-discovery").assertDoesNotExist()
        compose.onNodeWithTag("host-key").assertDoesNotExist()
        screenshot("settings-simple")
        compose.onNodeWithTag("advanced-settings").performScrollTo().performClick()
        check(value("host-root") == ".local/share/keyparcel") { "Advanced settings changed the storage path" }
        check(value("host-discovery") == ".ssh/authorized_keys") { "Advanced settings changed the discovery path" }
        compose.onNodeWithTag("host-key").assertExists()
        compose.onNodeWithTag("advanced-settings").performScrollTo().performClick()
        compose.onNodeWithTag("host-key").assertDoesNotExist()
        click("back")
        compose.onNodeWithTag("devices-screen").assertIsSelected()
        click("refresh-devices"); finished()
        val fingerprint = arg("peer_fingerprint")
        withClipboard { clipboard ->
            compose.onNodeWithTag("device-list").performScrollToNode(hasTestTag("copy-device-$fingerprint"))
            click("copy-device-$fingerprint")
            assertClipboard(clipboard, fingerprint)
        }
        val otherFingerprint = arg("other_peer_fingerprint")
        compose.onNodeWithTag("device-list").performScrollToNode(hasTestTag("approve-$otherFingerprint"))
        click("approve-$otherFingerprint")
        check(value("peer-name") == "bob") { "Bob's approval prompt did not suggest his SSH comment" }
        compose.onNodeWithTag("peer-verified").assertDoesNotExist()
        compose.onNodeWithTag("confirm-peer").assertIsEnabled()
        check("bob" !in SettingsStore(compose.activity).load().peers) { "Prefilling a name approved Bob" }
        compose.onNodeWithTag("peer-name").performTextReplacement("edited-but-canceled")
        compose.onNodeWithText("Cancel").performClick()
        compose.onNodeWithTag("device-list").performScrollToNode(hasTestTag("approve-$fingerprint"))
        click("approve-$fingerprint")
        check(value("peer-name") == "alice") { "Alice's approval prompt retained the canceled name instead of her SSH comment" }
        compose.onNodeWithTag("peer-name").performTextReplacement("")
        compose.onNodeWithTag("confirm-peer").assertIsNotEnabled()
        compose.onNodeWithTag("peer-name").performTextReplacement("alice")
        screenshot("peer-approval")
        val approvalModel = ViewModelProvider(compose.activity)[AppModel::class.java]
        val peer = approvalModel.devices.single { it.fingerprint == fingerprint }
        for (comment in listOf("phone", "世界", "user@host", "Self", "tab\u000bname")) {
            check(approvalModel.suggestedPeerName(peer.copy(comment = comment)) == comment) {
                "Valid unused SSH comment was not suggested: $comment"
            }
        }
        for (comment in listOf("", "self", "two words", " leading", "trailing ", "a\tb", "a\rb", "a\nb", "a\u0000b") + approvalModel.settings.peers.keys) {
            check(approvalModel.suggestedPeerName(peer.copy(comment = comment)).isEmpty()) {
                "Invalid or already-used SSH comment was suggested"
            }
        }
        compose.onNodeWithTag("peer-name").performTextReplacement("laptop")
        click("confirm-peer"); finished()
        compose.onNodeWithText("laptop · approved").assertExists()
        check(approvalModel.suggestedPeerName(peer.copy(comment = "laptop")).isEmpty()) {
            "An already-approved peer name was suggested"
        }
        withClipboard { clipboard -> click("copy-approved-laptop"); assertClipboard(clipboard, fingerprint) }
        click("back")
        refresh()
        compose.onNodeWithTag("settings").assertIsDisplayed()
        compose.onNodeWithTag("refresh-inbox").assertIsDisplayed()
        screenshot("inbox")
        val textID = arg("text_id")
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("item-$textID"))
        compose.onNodeWithTag("transfer-id-$textID").assertDoesNotExist()
        click("details-$textID")
        compose.onNodeWithTag("transfer-id-$textID").assertTextEquals(textID)
        val timestamps = compose.onAllNodesWithText("Uploaded ", substring = true).fetchSemanticsNodes()
            .flatMap { it.config[SemanticsProperties.Text] }.map { it.text }
        check(timestamps.isNotEmpty() && timestamps.all {
            Regex("Uploaded \\d{4}-\\d{2}-\\d{2} \\d{2}:\\d{2}:\\d{2} [+-]\\d{2}:\\d{2}").matches(it)
        }) { "Inbox timestamps lost seconds or their explicit timezone" }
        withClipboard { clipboard -> click("copy-transfer-$textID"); assertClipboard(clipboard, textID) }
        screenshot("inbox-details")
        click("details-$textID")
        compose.onNodeWithTag("transfer-id-$textID").assertDoesNotExist()
        val fileID = arg("file_id")
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("item-$fileID"))
        compose.onNodeWithTag("save-$fileID").assertIsEnabled()
        compose.onNodeWithTag("receive-$fileID").assertDoesNotExist()
        receive(arg("text_id"))
        screenshot("received")
        click("back")
        click("send-screen"); click("recipient-laptop")
        compose.onNodeWithTag("message").performTextInput(input)
        click("devices-screen"); click("settings"); click("back")
        compose.onNodeWithTag("devices-screen").assertIsSelected()
        click("send-screen")
        check(value("message") == input) { "Navigation discarded the text draft" }
        compose.onNodeWithTag("recipient-laptop").assertTextEquals("✓ To laptop")
        click("settings"); click("back")
        compose.onNodeWithTag("send-screen").assertIsSelected()
        check(value("message") == input) { "Settings Back did not preserve the send draft" }
        screenshot("send-draft")
        val revision = liveModel.refreshRevision
        compose.waitUntil(150_000) { liveModel.refreshRevision >= revision + 2 }
        check(value("message") == input) { "Timed refresh discarded the text draft" }
        compose.onNodeWithTag("recipient-laptop").assertTextEquals("✓ To laptop")
        compose.onNodeWithTag("send-message").performScrollTo().performClick()
        finished()
        compose.onNodeWithTag("notice").assertTextContains("Sent to laptop", substring = true)
        screenshot("sent")
    }

    @Test fun removalFailure() {
        refresh()
        val model = ViewModelProvider(compose.activity)[AppModel::class.java]
        for (id in listOf(arg("text_id"), arg("file_id"))) {
            compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("delete-$id"))
            click("delete-$id"); click("confirm-inbox-delete"); finished()
            compose.onNodeWithTag("error").assertExists()
            check(model.inbox.any { it.id == id }) { "Failed inbox delete lost the parcel" }
            compose.onNodeWithTag("delete-$id").assertIsEnabled()
        }

        receive(arg("text_id"))
        click("remove-close"); finished()
        compose.onNodeWithTag("error").assertExists()
        check(value("received-text") == input) { "Failed removal changed the text viewer" }
        compose.onNodeWithTag("remove-close").assertIsEnabled()
        screenshot("removal-error")
        click("back"); refresh()
        saveFile(arg("file_id"), "removal-retry.bin")
        waitFor("saved-file")
        click("remove-close"); finished()
        compose.onNodeWithTag("saved-file").assertExists()
        compose.onNodeWithTag("error").assertExists()
        compose.onNodeWithTag("remove-close").assertIsEnabled()
    }

    @Test fun inboxDeletion() {
        refresh()
        val model = ViewModelProvider(compose.activity)[AppModel::class.java]
        val unrelated = arg("file_id")
        val first = arg("delete_text_id")
        val peer = model.devices.first { it.key == model.settings.peers["laptop"] }
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("delete-$first"))
        click("delete-$first")
        // A trust edit while confirmation is open must invalidate the request.
        compose.runOnIdle { model.forget("laptop") }
        finished()
        compose.onNodeWithTag("confirm-inbox-delete").assertDoesNotExist()
        compose.runOnIdle { model.confirmInboxDelete() }
        compose.runOnIdle { model.approve(peer, "laptop") }
        finished(); refresh()
        check(model.inbox.any { it.id == first }) { "Trust change deleted the pending parcel" }
        for (id in listOf(first, arg("delete_file_id"))) {
            compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("delete-$id"))
            click("delete-$id")
            compose.onNodeWithText("Delete this device's remote inbox copy without downloading it? Other recipients' copies and saved local files remain untouched.").assertExists()
            click("cancel-inbox-delete")
            check(model.inbox.any { it.id == id }) { "Cancel removed the parcel" }
            click("delete-$id")
            InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
            compose.waitUntil(30_000) { model.pendingDelete == null }
            check(model.inbox.any { it.id == id }) { "Back removed the parcel" }
            click("delete-$id"); click("confirm-inbox-delete"); finished()
            compose.onNodeWithTag("notice").assertTextEquals("Deleted remote inbox copy; saved local files kept")
            check(model.inbox.none { it.id == id }) { "Successful delete retained the parcel" }
            check(model.inbox.any { it.id == unrelated }) { "Delete removed an unrelated parcel" }
        }
    }

    @Test fun restartAndRemove() {
        waitFor("refresh-inbox")
        click("settings")
        waitFor("identity-public")
        val public = value("identity-public")
        check(public == arg("expected_public")) { "App restart changed the device identity" }
        compose.onNodeWithTag("host-address").performScrollTo()
        check(value("host-address") == "127.0.0.1") { "App restart changed the saved server address" }
        click("devices-screen")
        compose.onNodeWithText("laptop · approved").assertExists()
        click("back"); refresh()
        receive(arg("text_id")); click("remove-close"); finished()
        compose.onNodeWithTag("notice").assertTextEquals("Transfer removed")
        compose.onNodeWithTag("item-${arg("text_id")}").assertDoesNotExist()
        receive(arg("new_text_id")); click("remove-close"); finished()
        screenshot("removed")
    }

    @Test fun stalledRequest() {
        waitFor("refresh-inbox")
        val model = ViewModelProvider(compose.activity)[AppModel::class.java]
        compose.waitUntil(150_000) { model.syncStatus == "Current" && model.inbox.any { it.id == arg("file_id") } }
        val beforeRevision = model.refreshRevision
        compose.runOnIdle {
            // Reconnect with the short deadline before the fixture stalls SSH.
            model.setForeground(false)
            model.requestTimeoutMillis = 10_000L
            model.setForeground(true)
        }
        compose.waitUntil(150_000) { model.syncStatus == "Current" && model.refreshRevision > beforeRevision }
        val beforeInbox = model.inbox
        val beforeRejected = model.rejected
        val beforeDevices = model.devices
        File(compose.activity.cacheDir, "stalled-ready").writeText("ready")
        compose.waitUntil(150_000) { File(compose.activity.cacheDir, "stalled-go").exists() }
        compose.onNodeWithTag("refresh-inbox").assertIsEnabled()
        compose.onNodeWithTag("send-screen").assertIsEnabled()
        compose.waitUntil(150_000) { model.syncStatus == "Stale" }
        val revision = model.refreshRevision
        compose.onNodeWithTag("refresh-inbox").performClick()
        compose.onNodeWithTag("refresh-inbox").assertIsEnabled()
        compose.onNodeWithTag("send-screen").assertIsEnabled()
        compose.waitUntil(150_000) { model.refreshRevision > revision }
        finished()
        compose.onNodeWithTag("error").assertTextEquals("Request timed out. Try again.")
        check(model.syncStatus == "Stale") { "Failed refresh did not mark cached data stale" }
        check(model.inbox == beforeInbox && model.rejected == beforeRejected) { "Failed refresh replaced the last good inbox" }
        check(model.devices == beforeDevices) { "Failed refresh replaced the last good discovery snapshot" }
        compose.onNodeWithTag("refresh-inbox").assertIsEnabled()
        compose.onNodeWithTag("send-screen").assertIsEnabled()
        compose.onNodeWithTag("settings").assertIsEnabled()
        click("settings")
        compose.onNodeWithTag("host-address").performScrollTo().assertIsEnabled()
        screenshot("timeout")
    }

    @Test fun retryAfterStall() {
        refresh()
        compose.onNodeWithTag("error").assertDoesNotExist()
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("item-${arg("file_id")}"))
        compose.onNodeWithTag("save-${arg("file_id")}").assertIsEnabled()
    }

    @Test fun sendToThisDevice() {
        waitFor("refresh-inbox")
        click("send-screen")
        compose.onNodeWithTag("recipient-laptop").assertDoesNotExist()
        click("recipient-self")
        field("message", "discard me")
        compose.onNodeWithTag("discard-draft").performScrollTo().performClick()
        check(value("message").isEmpty()) { "Discard left draft text behind" }
        compose.onNodeWithTag("recipient-self").assertTextEquals("This device")
        compose.onNodeWithTag("recipient-self").performScrollTo().performClick()
        compose.onNodeWithTag("recipient-self").assertTextEquals("✓ This device")
        compose.onNodeWithTag("message").performTextInput(input)
        compose.onNodeWithTag("send-message").assertIsEnabled()
        compose.onNodeWithTag("send-message").performScrollTo().performClick()
        finished()
        compose.onNodeWithTag("error").assertDoesNotExist()
        val notice = compose.onNodeWithTag("notice").fetchSemanticsNode().config[SemanticsProperties.Text].single().text
        check(notice.startsWith("Sent to this device")) { "Self-send did not report success" }
        val id = notice.substringAfterLast(" · ")
        click("send-screen")
        check(value("message").isEmpty()) { "Successful send retained the sent draft" }
        compose.onNodeWithTag("recipient-self").assertTextEquals("This device")
        click("inbox-screen")
        refresh(); receive(id)
        withClipboard { clipboard ->
            click("copy-received-text")
            assertClipboard(clipboard, input)
            compose.onNodeWithTag("received-text").performClick()
            compose.onNodeWithTag("received-text").performSemanticsAction(SemanticsActions.SetSelection) { it(2, 11, false) }
            compose.onNodeWithTag("received-text").performSemanticsAction(SemanticsActions.CopyText) { it() }
            assertClipboard(clipboard, "Keyparcel")
        }
        click("back"); refresh()
        compose.onNodeWithTag("receive-$id").assertExists()
        receive(id); click("remove-close"); finished()
    }

    @Test fun receivedClipboardCases() {
        refresh()
        val largeID = arg("large_id")
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("item-$largeID"))
        compose.onNodeWithText("Text · 189.1 KiB").assertExists()
        click("receive-$largeID"); waitFor("received-text")
        val prefix = "prefix\n"
        val tail = " 世界 🌍\ncomplete-tail\n"
        val complete = prefix + "x".repeat(193638 - prefix.toByteArray().size - tail.toByteArray().size) + tail
        check(value("received-text") != complete && value("received-text").length <= 65_536) { "Viewer exceeded the preview bound" }
        compose.onNodeWithText("Showing only the beginning", substring = true).assertExists()
        withClipboard { clipboard ->
            click("copy-received-text"); assertClipboard(clipboard, complete)
            check(clipboard.primaryClip?.description?.extras?.getBoolean("android.content.extra.IS_SENSITIVE") == true) {
                "Received clipboard content was not marked sensitive"
            }
            val context = InstrumentationRegistry.getInstrumentation().targetContext
            val unavailable = object : ContextWrapper(context) {
                override fun getSystemService(name: String): Any? = if (name == Context.CLIPBOARD_SERVICE) null else super.getSystemService(name)
            }
            var problem: String? = null
            compose.runOnIdle { copyText(unavailable, clipboardText("replacement"), "Received text", true) { problem = it } }
            check(problem?.contains("unavailable") == true) { "Missing clipboard service did not report an error" }
            assertClipboard(clipboard, complete)
            screenshot("received-copy")
        }
        click("back"); refresh()
        val nulID = arg("nul_id")
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("receive-$nulID"))
        click("receive-$nulID"); waitFor("received-text")
        withClipboard { clipboard ->
            compose.runOnIdle { clipboard.setPrimaryClip(ClipData.newPlainText("sentinel", "keep-clipboard")) }
            click("copy-received-text")
            compose.onNodeWithTag("error").assertTextContains("NUL", substring = true)
            assertClipboard(clipboard, "keep-clipboard")
            compose.onNodeWithTag("received-text").performClick()
            compose.onNodeWithTag("received-text").performSemanticsAction(SemanticsActions.SetSelection) { it(0, 12, false) }
            compose.onNodeWithTag("received-text").performSemanticsAction(SemanticsActions.CopyText) { it() }
            assertClipboard(clipboard, "keep-clipboard")
            compose.onNodeWithTag("received-text").performSemanticsAction(SemanticsActions.SetSelection) { it(0, 6, false) }
            compose.onNodeWithTag("received-text").performSemanticsAction(SemanticsActions.CopyText) { it() }
            assertClipboard(clipboard, "before")
        }
        click("back"); refresh()
        val platformID = arg("platform_id")
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("receive-$platformID"))
        click("receive-$platformID"); waitFor("received-text")
        withClipboard { clipboard ->
            compose.runOnIdle { clipboard.setPrimaryClip(ClipData.newPlainText("sentinel", "keep-clipboard")) }
            click("copy-received-text")
            compose.onNodeWithTag("error").assertTextContains("Android cannot copy", substring = true)
            assertClipboard(clipboard, "keep-clipboard")
            screenshot("clipboard-unavailable")
        }
    }

    @Test fun fileTransfers() {
        waitFor("refresh-inbox")
        val directory = File(compose.activity.filesDir, "document-fixtures").apply { mkdirs() }
        File(directory, "empty").writeBytes(byteArrayOf())
        File(directory, "stall-read").writeBytes(byteArrayOf())
        File(directory, "fail-read").writeBytes(byteArrayOf())
        // Keep the stalled source below the first picker screen even in focused runs.
        File(directory, "previous-save.bin").writeBytes(byteArrayOf())
        val block = ByteArray(64 * 1024) { (it % 251).toByte() }
        File(directory, "source").outputStream().use { output ->
            repeat(17 * 16) { output.write(block) }
            output.write(block, 0, 31)
        }
        val model = ViewModelProvider(compose.activity)[AppModel::class.java]
        blockedProviderRecovery(directory, model)
        click("send-screen"); click("send-file-mode"); click("recipient-laptop")
        compose.onNodeWithTag("send-file").assertIsNotEnabled()
        click("choose-file"); pickerRoot()
        pickerClick(scrollFiles = true) { it.text?.toString() == "fail-read" }; pickerReturned()
        compose.onNodeWithTag("send-file").performScrollTo().performClick(); finished()
        compose.onNodeWithTag("error").assertTextContains("Fixture source failed", substring = true)
        compose.onNodeWithTag("notice").assertDoesNotExist()
        compose.onNodeWithTag("send-file").assertIsEnabled()
        click("choose-file"); pickerRoot()
        pickerClick(scrollFiles = true) { it.text?.toString() == "stall-read" }; pickerReturned()
        compose.runOnIdle { model.documentTimeoutMillis = 3_000L }
        try {
            val started = android.os.SystemClock.elapsedRealtime()
            compose.onNodeWithTag("send-file").performScrollTo().performClick(); finished()
            check(android.os.SystemClock.elapsedRealtime() - started < 15_000) { "Stalled source did not return promptly" }
            compose.onNodeWithTag("error").assertTextContains("Document operation timed out", substring = true)
            compose.onNodeWithTag("notice").assertDoesNotExist()
            compose.onNodeWithTag("send-file").assertIsEnabled()
            compose.onNodeWithTag("back").assertIsEnabled()
            compose.waitUntil(10_000) { File(directory, "stall-read-closed").exists() }
            check(File(directory, "stall-read-opened").exists()) { "Stalled source never opened its pipe" }
            check(compose.activity.cacheDir.listFiles()?.none { it.name.startsWith("keyparcel-send-") } != false) {
                "Stalled source leaked its staging file"
            }
        } finally { compose.runOnIdle { model.documentTimeoutMillis = 120_000L } }
        // The same journey retries with a healthy source after the timeout.
        click("back"); click("send-screen")
        compose.onNodeWithTag("choose-file").assertExists()
        compose.onNodeWithTag("selected-filename").assertTextEquals("stall-read")
        compose.onNodeWithTag("recipient-laptop").assertTextEquals("✓ To laptop")
        click("settings"); click("back")
        compose.onNodeWithTag("send-screen").assertIsSelected()
        compose.onNodeWithTag("selected-filename").assertTextEquals("stall-read")
        compose.onNodeWithTag("choose-file").performScrollTo().performClick(); pickerRoot(); pickerCancel()
        compose.onNodeWithTag("selected-filename").assertTextEquals("stall-read")
        compose.onNodeWithTag("recipient-laptop").assertTextEquals("✓ To laptop")
        compose.onNodeWithTag("discard-draft").performScrollTo().performClick()
        compose.onNodeWithTag("selected-filename").assertDoesNotExist()
        compose.onNodeWithTag("send-file-mode").performScrollTo().performClick(); click("recipient-laptop")
        compose.onNodeWithTag("send-file").assertIsNotEnabled()
        click("choose-file"); pickerRoot(); pickerCancel()
        compose.onNodeWithTag("send-file").assertIsNotEnabled()
        click("choose-file"); pickerRoot()
        pickerClick(scrollFiles = true) { it.text?.toString() == "payload-世界.bin" }
        pickerReturned()
        compose.onNodeWithTag("selected-filename").assertTextEquals("payload-世界.bin")
        compose.onNodeWithTag("selected-size").assertTextEquals("Size unknown")
        screenshot("send-file")
        compose.onNodeWithTag("send-file").performScrollTo().performClick(); finished()
        val binaryID = compose.onNodeWithTag("notice").fetchSemanticsNode().config[SemanticsProperties.Text].single().text.substringAfterLast(" · ")
        click("send-screen"); click("send-file-mode"); click("recipient-laptop")
        click("choose-file"); pickerRoot()
        pickerClick(scrollFiles = true) { it.text?.toString() == "empty.bin" }; pickerReturned()
        compose.onNodeWithTag("selected-size").assertTextEquals("0 B")
        compose.onNodeWithTag("send-file").performScrollTo().performClick(); finished()
        val emptyID = compose.onNodeWithTag("notice").fetchSemanticsNode().config[SemanticsProperties.Text].single().text.substringAfterLast(" · ")
        InstrumentationRegistry.getInstrumentation().sendStatus(0, Bundle().apply {
            putString("binary_reply_id", binaryID); putString("empty_reply_id", emptyID)
        })
        refresh()
        val id = arg("file_id")
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("save-$id"))
        click("save-$id"); pickerCancel()
        compose.onNodeWithTag("saved-file").assertDoesNotExist()
        refresh()
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("save-$id"))
        compose.onNodeWithTag("save-$id").assertIsEnabled()
        for (name in listOf("fail-output.bin", "fail-write.bin")) {
            saveFile(id, name)
            compose.onNodeWithTag("error").assertTextContains("remote package retained", substring = true)
            compose.onNodeWithTag("saved-file").assertDoesNotExist()
            compose.onNodeWithTag("refresh-inbox").assertIsEnabled()
            refresh()
            compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("save-$id"))
            compose.onNodeWithTag("save-$id").assertIsEnabled()
        }
        compose.runOnIdle { model.documentTimeoutMillis = 3_000L }
        try {
            saveFile(id, "stall-write.bin")
            compose.onNodeWithTag("error").assertTextContains("Document operation timed out", substring = true)
            compose.onNodeWithTag("error").assertTextContains("remote package retained", substring = true)
            compose.onNodeWithTag("saved-file").assertDoesNotExist()
            compose.onNodeWithTag("refresh-inbox").assertIsEnabled()
            compose.waitUntil(10_000) { File(directory, "stall-write-closed").exists() }
            check(File(directory, "stall-write-opened").exists()) { "Stalled destination never opened its pipe" }
            refresh()
            compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("save-$id"))
            compose.onNodeWithTag("save-$id").assertIsEnabled()
            check(compose.activity.cacheDir.listFiles()?.none { it.name.startsWith("keyparcel-") && it.isFile } != false) {
                "Stalled destination leaked verified staging files"
            }
        } finally { compose.runOnIdle { model.documentTimeoutMillis = 120_000L } }
        saveFile(id, "renamed-世界.bin")
        waitFor("saved-file")
        screenshot("saved-file")
        click("back"); refresh()
        compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("save-$id"))
        compose.onNodeWithTag("save-$id").assertIsEnabled()
        saveFile(id, "renamed-世界.bin")
        waitFor("saved-file")
        check(File(directory, "renamed-世界(1).bin").exists()) { "System/provider save flow did not keep the existing destination" }
        val expected = File(directory, "renamed-世界.bin").readBytes()
        val xml = File(directory, "keyparcel-clipboard-ui.xml")
        val previous = ByteArray(expected.size + 17) { 42 }
        xml.writeBytes(previous)
        fun selectXmlDestination() {
            refresh()
            compose.onNodeWithTag("inbox-list").performScrollToNode(hasTestTag("save-$id"))
            click("save-$id"); pickerRoot()
            pickerExistingFile(xml.name)
            pickerClick { it.text?.toString()?.equals("Save", ignoreCase = true) == true }
            compose.waitUntil(30_000) {
                pickerNode { it.text?.toString()?.contains("Overwrite ${xml.name}") == true } != null
            }
        }
        click("back")
        selectXmlDestination()
        pickerClick { it.text?.toString()?.equals("Cancel", ignoreCase = true) == true }
        pickerCancel()
        check(xml.readBytes().contentEquals(previous)) { "Canceling replacement changed the existing XML file" }
        compose.onNodeWithTag("item-$id").assertExists()
        selectXmlDestination()
        pickerClick { it.text?.toString()?.equals("OK", ignoreCase = true) == true }
        pickerReturned(); waitFor("saved-file")
        check(xml.readBytes().contentEquals(expected)) { "Confirmed XML replacement did not preserve exact bytes or truncate old contents" }
        check(!File(directory, "keyparcel-clipboard-ui(1).xml").exists()) { "Replacement created a numbered XML copy" }
        click("remove-close"); finished()
        compose.onNodeWithTag("item-$id").assertDoesNotExist()
        check(compose.activity.cacheDir.listFiles()?.none {
            it.name.startsWith("keyparcel-") && it.isFile
        } != false) { "File operations leaked private staging files" }
    }

    @Test fun savePickerAllowsXmlReplacement() {
        waitFor("refresh-inbox")
        val xml = File(compose.activity.filesDir, "document-fixtures/keyparcel-clipboard-ui.xml")
        xml.parentFile!!.mkdirs()
        val previous = "<clipboard>keep me</clipboard>".toByteArray()
        xml.writeBytes(previous)
        var returned = false
        var selected: android.net.Uri? = null
        lateinit var launcher: androidx.activity.result.ActivityResultLauncher<String>
        compose.runOnIdle {
            launcher = compose.activity.activityResultRegistry.register("xml-replacement-check", SaveDocument()) {
                selected = it; returned = true
            }
        }
        try {
            for (confirm in listOf(false, true)) {
                compose.runOnIdle { returned = false; launcher.launch(xml.name) }
                pickerRoot()
                pickerExistingFile(xml.name)
                pickerClick { it.text?.toString()?.equals("Save", ignoreCase = true) == true }
                compose.waitUntil(30_000) {
                    pickerNode { it.text?.toString()?.contains("Overwrite ${xml.name}") == true } != null
                }
                pickerClick { it.text?.toString()?.equals(if (confirm) "OK" else "Cancel", ignoreCase = true) == true }
                if (confirm) pickerReturned() else pickerCancel()
                compose.runOnIdle {
                    check(returned) { "System save picker did not return" }
                    if (confirm) {
                        check(selected != null && DocumentsContract.getDocumentId(selected!!) == xml.name) {
                            "Confirmed replacement did not return the existing XML document"
                        }
                    } else check(selected == null) { "Canceled replacement returned a destination" }
                }
                check(xml.readBytes().contentEquals(previous)) { "The picker modified XML before verified delivery" }
            }
        } finally { compose.runOnIdle { launcher.unregister() } }
    }

    private fun blockedProviderRecovery(directory: File, model: AppModel) {
        fun uri(id: String) = DocumentsContract.buildDocumentUri("${compose.activity.packageName}.documents", id)
        val release = File(directory, "blocked-open-release")
        release.delete()
        try {
            compose.runOnIdle { model.documentTimeoutMillis = 1_000L }
            File(directory, "blocked-open-1").writeBytes(byteArrayOf())
            compose.runOnIdle { model.selectFile(uri("blocked-open-1")) }; finished()
            compose.runOnIdle { model.sendFile("laptop") }; finished()
            compose.onNodeWithTag("error").assertTextContains("Document operation timed out", substring = true)
            check(File(directory, "blocked-open-1-opened").exists()) { "First provider open never started" }
            check(!File(directory, "blocked-open-1-returned").exists()) { "Provider returned before recovery was tested" }
            compose.onNodeWithTag("refresh-inbox").assertIsEnabled()

            compose.runOnIdle { model.documentTimeoutMillis = 5_000L; model.selectFile(uri("empty")) }; finished()
            check(model.selectedFile?.name == "empty.bin") { "Healthy selection queued behind the abandoned provider call" }
            runBlocking {
                withDocumentAccess(5_000L) { access ->
                    access.open(compose.activity.contentResolver, uri("empty"), "r").use { document ->
                        document.createInputStream().use { input -> check(input.read() == -1) { "Healthy empty document changed" } }
                    }
                }
            }
            check(!File(directory, "blocked-open-1-returned").exists()) { "Healthy open only succeeded after the blocked call returned" }

            File(directory, "blocked-open-2").writeBytes(byteArrayOf())
            compose.runOnIdle { model.selectFile(uri("blocked-open-2")) }; finished()
            compose.runOnIdle { model.documentTimeoutMillis = 1_000L; model.sendFile("laptop") }; finished()
            compose.onNodeWithTag("error").assertTextContains("Document operation timed out", substring = true)
            check(File(directory, "blocked-open-2-opened").exists()) { "Second provider open never started" }
            check(!File(directory, "blocked-open-2-returned").exists()) { "Second provider unexpectedly returned" }
            val started = android.os.SystemClock.elapsedRealtime()
            compose.runOnIdle { model.documentTimeoutMillis = 10_000L; model.selectFile(uri("empty")) }; finished()
            compose.onNodeWithTag("error").assertTextContains("Document providers are busy", substring = true)
            check(android.os.SystemClock.elapsedRealtime() - started < 5_000L) { "Exhausted provider capacity did not fail promptly" }
        } finally {
            release.writeText("release")
            compose.runOnIdle { model.documentTimeoutMillis = 120_000L }
        }
        compose.waitUntil(10_000L) { (1..2).all { File(directory, "blocked-open-$it-closed").exists() } }
        compose.runOnIdle { model.selectFile(uri("empty")) }; finished()
        compose.onNodeWithTag("error").assertDoesNotExist()
        check(model.selectedFile?.name == "empty.bin") { "Document workers did not recover after provider release" }
        check(compose.activity.cacheDir.listFiles()?.none { it.name.startsWith("keyparcel-send-") } != false) {
            "Abandoned provider opens leaked staging files"
        }
        compose.runOnIdle { model.discardDraft(); model.navigate(Page.Inbox) }
    }

    @Test fun pickerCancellation() {
        waitFor("refresh-inbox")
        click("send-screen"); click("send-file-mode"); click("recipient-self")
        click("choose-file"); pickerCancel()
        compose.onNodeWithTag("send-file").assertIsNotEnabled()
        var returned = false
        compose.runOnIdle {
            compose.activity.activityResultRegistry.register("cancel-save-check", ActivityResultContracts.CreateDocument("application/octet-stream")) { uri ->
                check(uri == null) { "Canceled save returned a destination" }
                returned = true
            }.launch("cancel-check.bin")
        }
        pickerRoot()
        pickerCancel()
        check(returned) { "Save cancellation did not reach the activity result callback" }
        compose.onNodeWithTag("send-file").assertIsNotEnabled()
    }
}
