package dev.keyparcel.app

import org.junit.Assert.*
import org.junit.Test

class ConfirmationTest {
    @Test fun expiryAndReplacement() {
        val copied = Confirmation("Copied", 3_000)
        assertSame(copied, copied.expire(2_999))
        assertNull(copied.expire(3_000))
        val deleted = Confirmation("Parcel deleted", 5_000)
        assertSame(deleted, deleted.expire(3_000))
        assertNull(deleted.expire(5_000))
    }
}
