package dev.keyparcel.storage;

import android.content.Context;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.AtomicFile;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.ByteBuffer;
import java.security.KeyStore;
import java.util.Arrays;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;
import dev.keyparcel.bridge.Bridge;

// Internal storage boundary: only public keys and fingerprints leave the app.
public final class IdentityStore {
    private static final String ALIAS = "keyparcel-identity-encryption-v1";
    private static final byte[] AAD = "keyparcel-identity-v1".getBytes(java.nio.charset.StandardCharsets.US_ASCII);
    private final AtomicFile file;
    private final KeyStore store;

    public IdentityStore(Context context) throws Exception {
        file = new AtomicFile(new File(context.getNoBackupFilesDir(), "identity.bin"));
        store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
    }

    public byte[] loadOrCreate() throws Exception {
        if (file.getBaseFile().exists()) return decrypt(file.readFully());
        if (store.containsAlias(ALIAS)) {
            throw new IllegalStateException("Identity file is missing; refusing to silently replace the device identity");
        }
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
        generator.init(new KeyGenParameterSpec.Builder(ALIAS,
                KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256).build());
        generator.generateKey();
        byte[] plain = Bridge.generateIdentity();
        try {
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(Cipher.ENCRYPT_MODE, key());
            cipher.updateAAD(AAD);
            byte[] encrypted = cipher.doFinal(plain);
            byte[] encoded = ByteBuffer.allocate(4 + 12 + encrypted.length)
                    .putInt(1).put(cipher.getIV()).put(encrypted).array();
            FileOutputStream output = file.startWrite();
            try {
                output.write(encoded);
                file.finishWrite(output);
            } catch (Exception error) {
                file.failWrite(output);
                throw error;
            }
        } finally {
            Arrays.fill(plain, (byte) 0);
        }
        return decrypt(file.readFully());
    }

    private SecretKey key() throws Exception {
        SecretKey key = (SecretKey) store.getKey(ALIAS, null);
        if (key == null) throw new IllegalStateException("Identity encryption key is missing; the old identity cannot be recovered");
        return key;
    }

    private byte[] decrypt(byte[] encoded) throws Exception {
        if (encoded.length < 32 || encoded.length > 16384) throw new IllegalStateException("Invalid encrypted identity size");
        ByteBuffer input = ByteBuffer.wrap(encoded);
        if (input.getInt() != 1) throw new IllegalStateException("Unsupported encrypted identity format");
        byte[] iv = new byte[12];
        input.get(iv);
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.DECRYPT_MODE, key(), new GCMParameterSpec(128, iv));
        cipher.updateAAD(AAD);
        return cipher.doFinal(encoded, 16, encoded.length - 16);
    }
}
