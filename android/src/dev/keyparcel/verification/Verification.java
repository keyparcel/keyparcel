package dev.keyparcel.verification;

import android.app.Activity;
import android.app.Instrumentation;
import android.content.Context;
import android.os.Bundle;
import android.util.Log;
import java.io.File;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.Arrays;
import org.json.JSONArray;
import org.json.JSONObject;
import dev.keyparcel.bridge.Bridge;
import dev.keyparcel.bridge.Client;
import dev.keyparcel.bridge.Delivery;
import dev.keyparcel.storage.IdentityStore;

public final class Verification extends Instrumentation {
    private Bundle arguments;
    private final JSONArray checks = new JSONArray();

    @Override public void onCreate(Bundle arguments) {
        super.onCreate(arguments);
        this.arguments = arguments;
        start();
    }

    @Override public void onStart() {
        Bundle result = new Bundle();
        byte[] identity = null;
        Client client = null;
        try {
            Context context = getTargetContext();
            go.Seq.setContext(context);
            Bridge.initialize(context.getCacheDir().getAbsolutePath());
            identity = new IdentityStore(context).loadOrCreate();
            String publicKey = Bridge.publicKey(identity);
            JSONObject report = new JSONObject();
            report.put("public_key", publicKey);
            report.put("fingerprint", Bridge.fingerprint(publicKey));
            report.put("pid", android.os.Process.myPid());
            report.put("checks", checks);
            String phase = arguments.getString("phase");
            if ("identity".equals(phase)) {
                byte[] ciphertext = Files.readAllBytes(new File(context.getNoBackupFilesDir(), "identity.bin").toPath());
                require(!new String(ciphertext, StandardCharsets.US_ASCII).contains("BEGIN OPENSSH PRIVATE KEY"),
                        "encrypted identity contains no plaintext SSH key");
                require((context.getApplicationInfo().flags & android.content.pm.ApplicationInfo.FLAG_ALLOW_BACKUP) == 0,
                        "verification app disables automatic backup");
                File stored = new File(context.getNoBackupFilesDir(), "identity.bin");
                byte[] tampered = ciphertext.clone();
                tampered[tampered.length - 1] ^= 1;
                boolean rejected = false;
                try {
                    Files.write(stored.toPath(), tampered);
                    try {
                        byte[] unexpected = new IdentityStore(context).loadOrCreate();
                        Arrays.fill(unexpected, (byte) 0);
                    } catch (javax.crypto.AEADBadTagException expected) {
                        rejected = true;
                    }
                } finally {
                    Files.write(stored.toPath(), ciphertext);
                }
                require(rejected, "tampered encrypted identity is rejected");
                byte[] reloaded = new IdentityStore(context).loadOrCreate();
                try {
                    require(publicKey.equals(Bridge.publicKey(reloaded)), "encrypted identity reload keeps the same public key");
                } finally {
                    Arrays.fill(reloaded, (byte) 0);
                }
            } else if ("exchange".equals(phase) || "restart".equals(phase)) {
                require(publicKey.equals(required("expected_public")), "device identity survives app/process restart");
                String observed = Bridge.probeServerKey("127.0.0.1", Integer.parseInt(required("port")), 30000);
                require(Bridge.serverFingerprint(observed).equals(Bridge.serverFingerprint(required("host_key"))),
                        "server-key discovery matches the independent fixture key");
                if ("exchange".equals(phase)) {
                    boolean rejected = false;
                    try {
                        Client wrong = connect(identity, required("peer_public"));
                        wrong.close();
                    } catch (Exception error) {
                        if (!error.toString().contains("host key does not match trusted key")) throw error;
                        rejected = true;
                    }
                    require(rejected, "incorrect server key is rejected");
                }
                client = connect(identity, required("host_key"));
                Arrays.fill(identity, (byte) 0);
                identity = null;
                client.approve("linux", required("peer_public"));
                JSONObject inbox = new JSONObject(client.inbox());
                require(hasItem(inbox, required("text_id")), "inbox lists the approved Linux text transfer");
                if ("exchange".equals(phase)) rejectDelivery(client, required("text_id"));
                receiveExact(client, context, required("text_id"), "received-text.txt", text());
                if ("exchange".equals(phase)) {
                    require(hasItem(inbox, required("binary_id")), "inbox lists the approved Linux binary transfer");
                    receiveExact(client, context, required("binary_id"), "received-binary.bin", binary());
                    JSONArray rejected = inbox.getJSONArray("rejected");
                    boolean found = false;
                    for (int i = 0; i < rejected.length(); i++) {
                        JSONObject entry = rejected.getJSONObject(i);
                        if (required("unapproved_id").equals(entry.getString("ID")) && entry.getString("Reason").contains("unapproved sender")) found = true;
                    }
                    require(found, "inbox rejects the unapproved sender");
                    rejectReceive(client, context, required("unapproved_id"), "unapproved sender");
                    rejectReceive(client, context, required("corrupted_id"), "failed to decrypt and authenticate");
                    report.put("text_id", client.sendText("linux", new String(text(), StandardCharsets.UTF_8)));
                    File binary = new File(context.getCacheDir(), "android-binary.bin");
                    try {
                        Files.write(binary.toPath(), binary());
                        report.put("binary_id", client.sendFile("linux", binary.getAbsolutePath()));
                    } finally {
                        Files.deleteIfExists(binary.toPath());
                    }
                }
            } else {
                throw new IllegalArgumentException("Unknown verification phase: " + phase);
            }
            if (client != null) {
                client.close();
                client = null;
            }
            result.putString("report", report.toString());
            result.putBoolean("verification_passed", true);
            finish(Activity.RESULT_OK, result);
        } catch (Throwable error) {
            result.putBoolean("verification_passed", false);
            result.putString("stream", Log.getStackTraceString(error));
            finish(Activity.RESULT_CANCELED, result);
        } finally {
            if (identity != null) Arrays.fill(identity, (byte) 0);
            if (client != null) {
                try { client.close(); } catch (Exception error) { Log.e("KeyparcelVerification", "Close failed", error); }
            }
        }
    }

    private Client connect(byte[] identity, String hostKey) throws Exception {
        return Bridge.connect(identity, "127.0.0.1", Integer.parseInt(required("port")), "parcel", hostKey,
                ".local/share/keyparcel", ".ssh/authorized_keys");
    }

    private String required(String name) {
        String value = arguments.getString(name);
        if (value == null || value.isEmpty()) throw new IllegalArgumentException("Missing " + name);
        return value;
    }

    private void require(boolean condition, String message) {
        if (!condition) throw new AssertionError(message);
        checks.put(message);
        Log.i("KeyparcelVerification", "PASS: " + message);
    }

    private boolean hasItem(JSONObject inbox, String id) throws Exception {
        JSONArray items = inbox.getJSONArray("items");
        for (int i = 0; i < items.length(); i++) {
            if (id.equals(items.getJSONObject(i).getString("transfer_id"))) return true;
        }
        return false;
    }

    private void receiveExact(Client client, Context context, String id, String name, byte[] expected) throws Exception {
        final String[] stagingPath = new String[1];
        client.receive(id, new Delivery() {
            @Override public void deliver(String metadata, String verifiedFile) throws Exception {
                require(id.equals(new JSONObject(metadata).getString("transfer_id")), name + " has the expected signed ID");
                require(new File(verifiedFile).getParentFile().equals(context.getCacheDir()), name + " uses app-private staging");
                require(Arrays.equals(expected, Files.readAllBytes(new File(verifiedFile).toPath())), name + " preserves exact bytes");
                stagingPath[0] = verifiedFile;
            }
        }, false);
        require(stagingPath[0] != null && !new File(stagingPath[0]).exists(), name + " cleans up verified staging");
    }

    private void rejectReceive(Client client, Context context, String id, String reason) throws Exception {
        final boolean[] delivered = {false};
        boolean rejected = false;
        try {
            client.receive(id, new Delivery() {
                @Override public void deliver(String metadata, String verifiedFile) {
                    delivered[0] = true;
                }
            }, false);
        } catch (Exception error) {
            if (!error.toString().contains(reason)) throw error;
            rejected = true;
        }
        require(rejected, "invalid incoming transfer is rejected: " + id);
        require(!delivered[0], "failed incoming transfer never reaches app delivery: " + id);
    }

    private void rejectDelivery(Client client, String id) throws Exception {
        boolean rejected = false;
        try {
            client.receive(id, new Delivery() {
                @Override public void deliver(String metadata, String verifiedFile) throws Exception {
                    throw new java.io.IOException("simulated local delivery failure");
                }
            }, false);
        } catch (Exception error) {
            if (!error.toString().contains("delivery failed; remote package retained:") ||
                    !error.toString().contains("simulated local delivery failure")) throw error;
            rejected = true;
        }
        require(rejected, "Java delivery errors propagate through the Go bridge");
        require(hasItem(new JSONObject(client.inbox()), id), "failed app delivery retains the incoming transfer");
    }

    private static byte[] text() { return "  Keyparcel 世界 🌍\n\n".getBytes(StandardCharsets.UTF_8); }
    private static byte[] binary() {
        byte[] bytes = new byte[4096];
        for (int i = 0; i < bytes.length; i++) bytes[i] = (byte) i;
        return bytes;
    }
}
