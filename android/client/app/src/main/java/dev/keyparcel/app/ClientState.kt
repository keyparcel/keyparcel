package dev.keyparcel.app

enum class Page { Inbox, Settings, Devices, Send, Text, File }

data class NavigationState(val page: Page = Page.Inbox, val settingsParent: Page = Page.Inbox) {
    fun open(target: Page): NavigationState = copy(
        page = target,
        settingsParent = if (target == Page.Settings && page != Page.Settings) {
            if (page == Page.Send || page == Page.Devices) page else Page.Inbox
        } else settingsParent,
    )

    fun back(): NavigationState = copy(page = if (page == Page.Settings) settingsParent else Page.Inbox)
}

data class SendDraft(val text: String = "", val recipient: String = "", val fileMode: Boolean = false,
    val additionalRecipients: List<String> = emptyList()) {
    val recipients get() = (listOf(recipient) + additionalRecipients).filter { it.isNotEmpty() }.distinct()
    fun select(name: String) = copy(recipient = name, additionalRecipients = additionalRecipients.filterNot { it == name })
    fun add(name: String) = if (name in recipients) this else if (recipient.isEmpty()) select(name)
        else copy(additionalRecipients = additionalRecipients + name)
    fun remove(name: String) = if (recipient == name) copy(recipient = additionalRecipients.firstOrNull().orEmpty(),
        additionalRecipients = additionalRecipients.drop(1)) else copy(additionalRecipients = additionalRecipients - name)
    fun reconcile(previous: Settings, current: Settings): SendDraft {
        val changed = previous.host != current.host || recipients.any { it != "self" &&
            (current.peers[it] == null || current.peers[it] != previous.peers[it]) }
        return if (changed) copy(recipient = "", additionalRecipients = emptyList()) else this
    }
}

fun recipientLabel(name: String) = if (name == "self") "This device" else name
private fun recipientKey(name: String, settings: Settings, publicKey: String) =
    (if (name == "self") publicKey else settings.peers[name].orEmpty()).trim().split(Regex("\\s+")).take(2).joinToString(" ")
fun availableRecipients(draft: SendDraft, settings: Settings, publicKey: String): List<String> {
    val selected = draft.recipients.map { recipientKey(it, settings, publicKey) }.toSet()
    return (listOf("self") + settings.peers.keys.sorted()).distinctBy { recipientKey(it, settings, publicKey) }
        .filter { recipientKey(it, settings, publicKey) !in selected }
}

data class SendContext(val settings: Settings, val names: List<String>, val publicKey: String) {
    fun matches(current: Settings, currentPublicKey: String = publicKey) = settings.host == current.host &&
        ("self" !in names || currentPublicKey == publicKey) && names.all {
        it == "self" || settings.peers[it] != null && settings.peers[it] == current.peers[it]
    }
}
data class SendDelivery(val recipient: String, val id: String, val error: String = "", val unconfirmed: Boolean = false)
data class SendReport(val deliveries: List<SendDelivery>) {
    val failed get() = deliveries.filter { it.error.isNotEmpty() && !it.unconfirmed }.map { it.recipient }
    val complete get() = deliveries.isNotEmpty() && deliveries.all { it.error.isEmpty() && !it.unconfirmed }
    fun merge(next: SendReport) = SendReport(deliveries.map { old -> next.deliveries.find { it.recipient == old.recipient } ?: old })
    fun headline(): String {
        if (!complete) return "Send incomplete"
        val title = if (deliveries.size == 1) "Sent to ${recipientLabel(deliveries.single().recipient)}"
            else "Sent to ${deliveries.size} devices"
        val id = deliveries.map { it.id }.distinct().singleOrNull().orEmpty()
        return title + if (id.isEmpty()) "" else " · ${id.take(8)}"
    }
}

fun SendDelivery.outcome() = when {
    unconfirmed -> "unconfirmed — check the inbox before sending again"
    error.isNotEmpty() -> "failed — $error"
    else -> "delivered"
}

const val SEND_FEEDBACK_MILLIS = 5_000L

data class SendFeedback(val report: SendReport, val expanded: Boolean = !report.complete,
    val visible: Boolean = true, val dismissAt: Long = 0) {
    fun expand(open: Boolean, now: Long) = copy(expanded = open,
        dismissAt = if (report.complete && !open) now + SEND_FEEDBACK_MILLIS else 0)
    fun expire(now: Long) = if (visible && report.complete && !expanded && dismissAt != 0L && now >= dismissAt)
        copy(visible = false) else this
}

// A fixed polling tick is skipped during work; only connection retries back off.
data class RefreshRetry(val delayMillis: Long = 1_000, val nextAttemptMillis: Long = 0) {
    fun failed(now: Long) = RefreshRetry(minOf(delayMillis * 2, 30_000), now + delayMillis)
}

// Keep confirmation tied to the original server and approval snapshot.
data class InboxDeleteRequest(val id: String, val settings: Settings) {
    fun matches(current: Settings, items: List<InboxItem>) = settings == current && items.any { it.id == id }
}

// One replaceable, non-blocking confirmation. Expiry is testable without a clock.
data class Confirmation(val message: String, val dismissAt: Long) {
    fun expire(now: Long): Confirmation? = if (now >= dismissAt) null else this
}

// Results belong to both a transport epoch and the latest local mutation/trust snapshot.
data class RefreshContext(val settings: Settings, val epoch: Long, val revision: Long) {
    fun matches(current: Settings, currentEpoch: Long, currentRevision: Long, foreground: Boolean) =
        foreground && settings == current && epoch == currentEpoch && revision == currentRevision
}

// Percentages count encrypted transfer bytes for one recipient, never successful
// delivery. Publication, verification and local delivery have no percentage.
data class TransferProgress(val text: String, val fraction: Float?)
fun transferProgress(phase: String, bytes: Long, total: Long, item: Long, items: Long): TransferProgress {
    if (total <= 0) return TransferProgress(phase, null)
    val fraction = (bytes.toDouble()/total).coerceIn(0.0,1.0).toFloat()
    val count = if (items > 1) " $item/$items" else ""
    return TransferProgress("$phase$count · ${(fraction*100).toInt()}%", fraction)
}

internal data class TransferProgressContext(val sequence: Long, val settings: Settings, val epoch: Long) {
    fun matches(currentSequence: Long, current: Settings, currentEpoch: Long, foreground: Boolean, busy: Boolean) =
        busy && foreground && sequence == currentSequence && settings == current && epoch == currentEpoch
}
