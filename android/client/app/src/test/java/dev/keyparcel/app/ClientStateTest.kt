package dev.keyparcel.app

import org.junit.Assert.*
import org.junit.Test

class ClientStateTest {
    @Test fun settingsReturnsToTheScreenThatOpenedIt() {
        for (parent in listOf(Page.Inbox, Page.Send, Page.Devices)) {
            val navigation = NavigationState().open(parent).open(Page.Settings).open(Page.Settings)
            assertEquals(parent, navigation.back().page)
        }
    }

    @Test fun resultsReturnToInboxInsteadOfAnOldSettingsParent() {
        for (result in listOf(Page.Text, Page.File)) {
            val navigation = NavigationState().open(Page.Send).open(Page.Settings).open(result)
            assertEquals(Page.Inbox, navigation.back().page)
        }
    }

    @Test fun changingHostOrSelectedApprovalClearsOnlyTheRecipient() {
        val settings = Settings(Host("server", 22, "user", "server key"), mapOf("laptop" to "peer key"))
        val draft = SendDraft("  世界\n", "laptop", true)
        val changed = listOf(settings.copy(host = settings.host!!.copy(address = "other")),
            settings.copy(host = settings.host!!.copy(key = "replacement server key")),
            settings.copy(peers = emptyMap()), settings.copy(peers = mapOf("laptop" to "replacement peer key")))
        for (current in changed) {
            assertEquals(draft.copy(recipient = ""), draft.reconcile(settings, current))
        }
        val self = draft.copy(recipient = "self")
        assertEquals(self.copy(recipient = ""), self.reconcile(settings, changed.first()))
    }

    @Test fun unrelatedApprovalChangesKeepTheDraftAndSelection() {
        val settings = Settings(Host("server", 22, "user"), mapOf("laptop" to "peer key"))
        val current = settings.copy(peers = settings.peers + ("other" to "other key"))
        val draft = SendDraft("draft", "laptop", true)
        assertEquals(draft, draft.reconcile(settings, current))
        val self = draft.copy(recipient = "self")
        assertEquals(self, self.reconcile(settings, settings.copy(peers = emptyMap())))
    }
    @Test fun retriesBackOffAndManualRefreshCanRetryNow() {
        var retry = RefreshRetry()
        var now = 0L
        for (delay in listOf(1_000L, 2_000L, 4_000L, 8_000L, 16_000L, 30_000L, 30_000L)) {
            retry = retry.failed(now)
            assertEquals(now + delay, retry.nextAttemptMillis)
            now = retry.nextAttemptMillis
        }
        assertEquals(0L, retry.copy(nextAttemptMillis = 0).nextAttemptMillis)
    }
}
