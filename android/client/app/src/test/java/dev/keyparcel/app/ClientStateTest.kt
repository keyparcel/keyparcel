package dev.keyparcel.app

import org.junit.Assert.*
import org.junit.Test

class ClientStateTest {
    @Test fun refreshCannotRestoreRowsAfterDeleteOrTrustChanges() {
        val original = Settings(Host("server", 22, "user", "pin"), mapOf("bob" to "approved"))
        val poll = RefreshContext(original, 4, 10)
        assertTrue(poll.matches(original, 4, 10, true))
        assertFalse("Delete supersedes the older Inbox list", poll.matches(original, 4, 11, true))
        assertFalse("Reconnect supersedes the old transport", poll.matches(original, 5, 10, true))
        assertFalse("Removed approvals invalidate verified results", poll.matches(original.copy(peers = emptyMap()), 4, 10, true))
        assertFalse("Changed pin invalidates results", poll.matches(original.copy(host = original.host!!.copy(key = "new pin")), 4, 10, true))
        assertFalse("Background results cannot publish", poll.matches(original, 4, 10, false))
    }

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

    @Test fun inboxDeleteBelongsToCapturedContext() {
        val host = Host("server", 22, "user", "approved-key")
        val settings = Settings(host, mapOf("bob" to "key"))
        val item = InboxItem("0123456789abcdef0123456789abcdef", "bob", "file", "file.bin", 1, "")
        val request = InboxDeleteRequest(item.id, settings)
        assertTrue(request.matches(settings, listOf(item)))
        for (changed in listOf(
            settings.copy(host = host.copy(address = "other")),
            settings.copy(host = host.copy(port = 2222)),
            settings.copy(host = host.copy(user = "other")),
            settings.copy(host = host.copy(key = "replacement")),
            settings.copy(host = host.copy(root = "other")),
            settings.copy(host = host.copy(discovery = "other")),
            settings.copy(peers = emptyMap()),
        )) assertFalse("Same-ID entry accepted in changed context", request.matches(changed, listOf(item)))
        assertFalse(request.matches(settings, emptyList()))
        assertFalse(request.matches(settings, listOf(item.copy(id = "other"))))
    }
    @Test fun recipientTokensPreventDuplicatesAndPromoteOnRemoval() {
        val draft = SendDraft("keep", "self", true).add("bob").add("bob")
        assertEquals(listOf("self", "bob"), draft.recipients)
        assertEquals(listOf("bob"), draft.remove("self").recipients)
        assertEquals(listOf("self"), draft.remove("bob").recipients)
        assertEquals(listOf("bob"), draft.select("bob").recipients)
        assertEquals("keep", draft.remove("self").text)
        val settings = Settings(Host("server", 22, "user"), mapOf("bob" to "ssh-ed25519 BBB comment", "alias" to "ssh-ed25519 AAA other"))
        assertEquals(listOf("bob"), availableRecipients(SendDraft(recipient = "self"), settings, "ssh-ed25519 AAA own"))
        assertTrue(availableRecipients(draft, settings, "ssh-ed25519 AAA own").isEmpty())
    }

    @Test fun changedAdditionalApprovalInvalidatesWholeSelection() {
        val settings = Settings(Host("server", 22, "user"), mapOf("bob" to "key"))
        val draft = SendDraft("keep", "self", true).add("bob")
        assertEquals(draft.copy(recipient = "", additionalRecipients = emptyList()),
            draft.reconcile(settings, settings.copy(peers = emptyMap())))
        val context = SendContext(settings, draft.recipients, "own key")
        assertTrue(context.matches(settings.copy(peers = settings.peers + ("other" to "other key"))))
        assertFalse(context.matches(settings.copy(peers = emptyMap())))
        assertFalse(context.matches(settings, "replacement own key"))
        assertFalse(context.matches(settings.copy(host = settings.host!!.copy(root = "other"))))
    }

    @Test fun retryOnlyFailedRecipientsAndKeepDeliveredOutcomes() {
        val report = SendReport(listOf(SendDelivery("self", "original"),
            SendDelivery("bob", "", "permission denied"), SendDelivery("unknown", "", "lost acknowledgement", true)))
        assertEquals(listOf("bob"), report.failed)
        val retried = report.merge(SendReport(listOf(SendDelivery("bob", "retry"))))
        assertEquals("original", retried.deliveries.first().id)
        assertEquals("retry", retried.deliveries[1].id)
        assertTrue(retried.deliveries.last().unconfirmed)
        assertTrue(retried.failed.isEmpty())
        assertFalse(retried.complete)
        assertTrue(retried.deliveries.first().outcome() == "delivered")
    }
    @Test fun feedbackPausesWhileOpenAndRetainsOutcomesAfterExpiry() {
        val report = SendReport(listOf(SendDelivery("self", "0123456789abcdef")))
        var feedback = SendFeedback(report).expand(false, 100)
        assertEquals("Sent to This device · 01234567", report.headline())
        assertTrue(feedback.expire(5_099).visible)
        feedback = feedback.expand(true, 101)
        assertTrue(feedback.expire(100_000).visible)
        feedback = feedback.expand(false, 100_000)
        assertFalse(feedback.expire(105_000).visible)
        assertEquals(report, feedback.expire(105_000).report)
    }

    @Test fun problemsPersistAndRetriesNeverConflateIds() {
        val report = SendReport(listOf(SendDelivery("self", "original"),
            SendDelivery("bob", "", "permission denied")))
        val feedback = SendFeedback(report)
        assertTrue(feedback.expanded)
        assertEquals("Send incomplete", report.headline())
        assertTrue(feedback.expand(false, 100).expire(100_000).visible)
        val retried = report.merge(SendReport(listOf(SendDelivery("bob", "retry"))))
        assertEquals("Sent to 2 devices", retried.headline())
        assertEquals(listOf("original", "retry"), retried.deliveries.map { it.id })
        val uncertain = SendFeedback(SendReport(listOf(SendDelivery("bob", "", "lost", true))))
        assertTrue(uncertain.expire(100_000).visible)
        assertTrue(uncertain.expanded)
        assertEquals("unconfirmed — check the inbox before sending again", uncertain.report.deliveries.single().outcome())
        assertEquals("Sent to 2 devices · shared-i", SendReport(listOf(
            SendDelivery("self", "shared-id"), SendDelivery("bob", "shared-id"))).headline())
    }

}
