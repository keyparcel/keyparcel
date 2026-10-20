package dev.keyparcel.app

import android.os.Bundle
import androidx.test.platform.app.InstrumentationRegistry
import dev.keyparcel.bridge.Bridge
import dev.keyparcel.storage.IdentityStore
import org.junit.Test

// Test APK only: fixture setup is not an exported interface in the client app.
class SmokeSetup {
    @Test fun prepare() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val context = instrumentation.targetContext
        require(context.packageName == "io.github.keyparcel.keyparcel.smoke") { "Smoke setup may only target the smoke variant" }
        go.Seq.setContext(context)
        Bridge.initialize(context.cacheDir.absolutePath)
        val args = InstrumentationRegistry.getArguments()
        val desired = Host("127.0.0.1", 2333, "parcel", requireNotNull(args.getString("host_key")))
        val store = SettingsStore(context)
        val settings = store.load()
        require(settings.host == null || settings.host == desired) {
            "Smoke app has different server settings; reset the smoke environment explicitly to replace them"
        }
        if (settings.host == null) store.save(settings.copy(host = desired))
        val identity = IdentityStore(context).loadOrCreate()
        try {
            val public = Bridge.publicKey(identity)
            instrumentation.sendStatus(0, Bundle().apply {
                putString("phone_public", public)
                putString("phone_fingerprint", Bridge.fingerprint(public))
            })
        } finally { identity.fill(0) }
    }
}
