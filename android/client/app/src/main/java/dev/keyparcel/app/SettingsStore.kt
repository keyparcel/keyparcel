package dev.keyparcel.app

import android.content.Context
import android.util.AtomicFile
import dev.keyparcel.bridge.Bridge
import java.io.File
import org.json.JSONObject

data class Host(
    val address: String,
    val port: Int,
    val user: String,
    val key: String = "",
    val root: String = ".local/share/keyparcel",
    val discovery: String = ".ssh/authorized_keys",
) {
    val trusted get() = key.isNotEmpty()
    fun sameServer(other: Host) = address == other.address && port == other.port

    fun validateConnection() {
        require(address.isNotBlank() && !address.any { it.isWhitespace() || it == '\u0000' }) {
            "Enter a server address without whitespace"
        }
        require(port in 1..65535) { "Port must be between 1 and 65535" }
        require(user.isNotBlank() && !user.any { it.isWhitespace() || it == '\u0000' }) { "Enter an SSH username" }
        require(root.isNotBlank() && discovery.isNotBlank()) { "Remote paths must not be empty" }
    }

    fun validate() {
        validateConnection()
        if (trusted) Bridge.serverFingerprint(key)
    }

    fun json() = JSONObject().put("address", address).put("port", port).put("user", user)
        .put("key", key).put("root", root).put("discovery", discovery)

    companion object {
        fun from(json: JSONObject) = Host(
            json.getString("address"), json.getInt("port"), json.getString("user"),
            json.getString("key"), json.getString("root"), json.getString("discovery"),
        )
    }
}

data class Settings(val host: Host? = null, val peers: Map<String, String> = emptyMap())

class SettingsStore(context: Context) {
    private val file = AtomicFile(File(context.noBackupFilesDir, "settings.json"))

    fun load(): Settings {
        if (!file.baseFile.exists()) return Settings()
        val json = JSONObject(String(file.readFully(), Charsets.UTF_8))
        require(json.getInt("version") == 1) { "Unsupported settings format" }
        require(json.has("host")) { "Saved host settings are missing" }
        val host = if (json.isNull("host")) null else Host.from(json.getJSONObject("host"))
        val peers = json.getJSONObject("peers")
        return Settings(host, peers.keys().asSequence().associateWith { peers.getString(it) }).also(::validate)
    }

    fun save(settings: Settings) {
        validate(settings)
        val json = JSONObject().put("version", 1).put("host", settings.host?.json() ?: JSONObject.NULL)
            .put("peers", JSONObject(settings.peers))
        val output = file.startWrite()
        try {
            output.write(json.toString().toByteArray(Charsets.UTF_8))
            file.finishWrite(output)
        } catch (error: Exception) {
            file.failWrite(output)
            throw error
        }
    }

    private fun validate(settings: Settings) {
        settings.host?.validate()
        settings.peers.forEach { (name, key) -> Bridge.validatePeer(name, key) }
    }
}
