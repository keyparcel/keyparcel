package dev.keyparcel.app

import org.junit.Assert.*
import org.junit.Test

class HostTest {
    @Test fun approvalBelongsToAddressAndPort() {
        val host = Host("server", 22, "user", "approved key")
        assertTrue(host.trusted)
        assertTrue(host.sameServer(host.copy(user = "other", root = "other-root")))
        assertFalse(host.sameServer(host.copy(address = "other-server")))
        assertFalse(host.sameServer(host.copy(port = 2222)))
        assertFalse(host.copy(key = "").trusted)
    }

    @Test fun discoverySettingsNeedNoKey() {
        val host = Host("server", 22, "user")
        host.validateConnection()
        for (invalid in listOf(host.copy(address = "bad server"), host.copy(address = ""),
            host.copy(port = 0), host.copy(port = 65536), host.copy(user = ""), host.copy(root = ""))) {
            assertThrows(IllegalArgumentException::class.java) { invalid.validateConnection() }
        }
    }
}
