package dev.hole.app.config

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import android.util.Base64

/**
 * 用 Android Keystore 管理的 AES-256-GCM 密钥保护用户输入的两种密码。
 * 密钥不可导出；系统清除（锁屏凭据变更等）后解密失败，调用方视为"未保存"，
 * 要求用户重新填写，而不是用明文兜底。这里只做本地存储保护，
 * 不是新的设备认证协议。
 */
class SecretCipher(
    private val keyAlias: String = "hole_config_v1",
) {
    fun encrypt(plaintext: CharArray): String {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey())
        val iv = cipher.iv
        val encrypted = cipher.doFinal(String(plaintext).toByteArray(Charsets.UTF_8))
        val blob = iv + encrypted
        return Base64.encodeToString(blob, Base64.NO_WRAP)
    }

    /** 返回 null 表示密钥失效或密文损坏：调用方应要求重新输入密码。 */
    fun decrypt(ciphertext: String): CharArray? {
        if (ciphertext.isEmpty()) return CharArray(0)
        return try {
            val blob = Base64.decode(ciphertext, Base64.NO_WRAP)
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, getOrCreateKey(), GCMParameterSpec(GCM_TAG_BITS, blob, 0, GCM_IV_BYTES))
            String(cipher.doFinal(blob, GCM_IV_BYTES, blob.size - GCM_IV_BYTES), Charsets.UTF_8).toCharArray()
        } catch (_: Exception) {
            null
        }
    }

    private fun getOrCreateKey(): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (keyStore.getEntry(keyAlias, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(keyAlias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    companion object {
        private const val ANDROID_KEYSTORE = "AndroidKeyStore"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private const val GCM_IV_BYTES = 12
        private const val GCM_TAG_BITS = 128
    }
}
