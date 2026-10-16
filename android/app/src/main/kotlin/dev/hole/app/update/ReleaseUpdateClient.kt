package dev.hole.app.update

import android.app.DownloadManager
import android.content.Context
import android.net.Uri
import android.os.Environment
import dev.hole.app.BuildConfig
import java.net.URL
import javax.net.ssl.HttpsURLConnection

data class ReleaseDownloadRecord(val id: Long, val tag: String)

data class ReleaseDownloadSnapshot(
    val status: Int,
    val downloadedBytes: Long,
    val totalBytes: Long,
    val reason: Int,
)

class ReleaseUpdateClient(context: Context) {
    private val appContext = context.applicationContext
    private val preferences = appContext.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)

    fun selectedAccelerator(): String = preferences.getString(KEY_ACCELERATOR, ReleaseAccelerators.DIRECT)
        ?: ReleaseAccelerators.DIRECT

    fun saveSelectedAccelerator(id: String) {
        val valid = ReleaseAccelerators.presets.any { it.id == id }
        preferences.edit().putString(KEY_ACCELERATOR, if (valid) id else ReleaseAccelerators.DIRECT).apply()
    }

    fun customPrefix(): String = preferences.getString(KEY_CUSTOM_PREFIX, "").orEmpty()

    fun saveCustomPrefix(prefix: String) {
        preferences.edit().putString(KEY_CUSTOM_PREFIX, prefix).apply()
    }

    fun fetchLatest(acceleratorId: String, customPrefix: String): GitHubRelease {
        val accelerator = ReleaseAccelerators.resolve(acceleratorId, customPrefix)
        val apiPrefix = if (accelerator.proxyApi) accelerator.prefix else ""
        val requestUrl = githubRequestUrl(LATEST_RELEASE_API, apiPrefix)
        val connection = (URL(requestUrl).openConnection() as? HttpsURLConnection)
            ?: throw IllegalStateException("发行版地址必须使用 HTTPS")
        connection.connectTimeout = CONNECT_TIMEOUT_MS
        connection.readTimeout = READ_TIMEOUT_MS
        connection.requestMethod = "GET"
        connection.setRequestProperty("Accept", "application/vnd.github+json")
        connection.setRequestProperty("User-Agent", "Hole-Android/${BuildConfig.VERSION_NAME}")
        try {
            val status = connection.responseCode
            if (status !in 200..299) {
                val detail = connection.errorStream?.bufferedReader()?.use { it.readText().take(300) }
                throw IllegalStateException(detail?.takeIf(String::isNotBlank)?.let {
                    "发行版服务返回 HTTP $status：$it"
                } ?: "发行版服务返回 HTTP $status")
            }
            val body = connection.inputStream.bufferedReader().use { it.readText() }
            return GitHubReleaseParser.parseLatest(body)
        } finally {
            connection.disconnect()
        }
    }

    fun enqueueApkDownload(release: GitHubRelease, acceleratorId: String, customPrefix: String): ReleaseDownloadRecord {
        val asset = release.appApk ?: throw IllegalStateException("最新发行版没有找到手机版 APK")
        val accelerator = ReleaseAccelerators.resolve(acceleratorId, customPrefix)
        val url = githubRequestUrl(asset.downloadUrl, accelerator.prefix)
        val filename = asset.name.replace(Regex("[^A-Za-z0-9._-]"), "_")
        require(filename.endsWith(".apk", ignoreCase = true)) { "发行版 APK 文件名无效" }
        val request = DownloadManager.Request(Uri.parse(url)).apply {
            setTitle("Hole ${release.tag}")
            setDescription("正在下载 Android 安装包")
            setMimeType(APK_MIME_TYPE)
            setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            setDestinationInExternalFilesDir(appContext, Environment.DIRECTORY_DOWNLOADS, filename)
        }
        val manager = appContext.getSystemService(DownloadManager::class.java)
            ?: throw IllegalStateException("Android 下载服务不可用")
        val record = ReleaseDownloadRecord(manager.enqueue(request), release.tag)
        preferences.edit().putLong(KEY_DOWNLOAD_ID, record.id).putString(KEY_DOWNLOAD_TAG, record.tag).apply()
        return record
    }

    fun savedDownload(): ReleaseDownloadRecord? {
        val id = preferences.getLong(KEY_DOWNLOAD_ID, INVALID_DOWNLOAD_ID)
        val tag = preferences.getString(KEY_DOWNLOAD_TAG, null)
        return if (id == INVALID_DOWNLOAD_ID || tag.isNullOrBlank()) null else ReleaseDownloadRecord(id, tag)
    }

    fun queryDownload(id: Long): ReleaseDownloadSnapshot? {
        val manager = appContext.getSystemService(DownloadManager::class.java) ?: return null
        val cursor = manager.query(DownloadManager.Query().setFilterById(id)) ?: return null
        cursor.use {
            if (!it.moveToFirst()) return null
            return ReleaseDownloadSnapshot(
                status = it.getInt(it.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS)),
                downloadedBytes = it.getLong(it.getColumnIndexOrThrow(DownloadManager.COLUMN_BYTES_DOWNLOADED_SO_FAR)),
                totalBytes = it.getLong(it.getColumnIndexOrThrow(DownloadManager.COLUMN_TOTAL_SIZE_BYTES)),
                reason = it.getInt(it.getColumnIndexOrThrow(DownloadManager.COLUMN_REASON)),
            )
        }
    }

    fun downloadedUri(id: Long): Uri? = appContext.getSystemService(DownloadManager::class.java)
        ?.getUriForDownloadedFile(id)

    private companion object {
        const val PREFERENCES = "release_updates"
        const val KEY_ACCELERATOR = "accelerator"
        const val KEY_CUSTOM_PREFIX = "custom_prefix"
        const val KEY_DOWNLOAD_ID = "download_id"
        const val KEY_DOWNLOAD_TAG = "download_tag"
        const val INVALID_DOWNLOAD_ID = -1L
        const val CONNECT_TIMEOUT_MS = 12_000
        const val READ_TIMEOUT_MS = 20_000
        const val APK_MIME_TYPE = "application/vnd.android.package-archive"
        const val LATEST_RELEASE_API = "https://api.github.com/repos/Haikure/hole/releases/latest"
    }
}
