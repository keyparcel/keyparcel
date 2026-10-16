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

data class SendDraft(val text: String = "", val recipient: String = "", val fileMode: Boolean = false) {
    fun reconcile(previous: Settings, current: Settings): SendDraft {
        val changedPeer = recipient != "self" &&
            (current.peers[recipient] == null || current.peers[recipient] != previous.peers[recipient])
        return if (previous.host != current.host || changedPeer) copy(recipient = "") else this
    }
}

// A fixed polling tick is skipped during work; only connection retries back off.
data class RefreshRetry(val delayMillis: Long = 1_000, val nextAttemptMillis: Long = 0) {
    fun failed(now: Long) = RefreshRetry(minOf(delayMillis * 2, 30_000), now + delayMillis)
}
