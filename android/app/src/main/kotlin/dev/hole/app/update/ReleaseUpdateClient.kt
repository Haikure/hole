package dev.hole.app.update

import android.app.DownloadManager
import android.content.Context
import android.net.Uri
import android.os.Environment
import dev.hole.app.BuildConfig
import java.net.URL
import java.util.UUID
import javax.net.ssl.HttpsURLConnection

data class ReleaseDownloadRecord(val id: Long, val tag: String)

data class ReleaseDownloadSnapshot(
    val status: Int,
    val downloadedBytes: Long,
    val totalBytes: Long,
    val reason: Int,
) {
    val active: Boolean get() = status == DownloadManager.STATUS_PENDING ||
        status == DownloadManager.STATUS_RUNNING || status == DownloadManager.STATUS_PAUSED
    val progress: Float? get() = if (totalBytes > 0)
        (downloadedBytes.toDouble() / totalBytes).coerceIn(0.0, 1.0).toFloat() else null
}

/** A two-second rolling window, using monotonic timestamps from the caller. */
internal class ReleaseDownloadSpeed {
    private data class Sample(val time: Long, val bytes: Long)
    private val samples = ArrayDeque<Sample>()

    fun sample(snapshot: ReleaseDownloadSnapshot, nowMs: Long): Long? {
        if (snapshot.status != DownloadManager.STATUS_RUNNING || snapshot.downloadedBytes < 0) {
            samples.clear()
            return null
        }
        val previous = samples.lastOrNull()
        if (previous != null && (nowMs <= previous.time || nowMs - previous.time > WINDOW_MS ||
                snapshot.downloadedBytes < previous.bytes)) samples.clear()
        samples.addLast(Sample(nowMs, snapshot.downloadedBytes))
        val cutoff = nowMs - WINDOW_MS
        while (samples.size > 1 && samples.elementAt(1).time <= cutoff) samples.removeFirst()
        if (samples.size < 2) return null
        val first = samples.first()
        val start = maxOf(cutoff, first.time)
        // Interpolate only the boundary interval; do not include an older burst
        // in the window just because polling isn't aligned to exactly 2 seconds.
        val next = samples.elementAt(1)
        val startBytes = first.bytes.toDouble() + (next.bytes - first.bytes).toDouble() *
            (start - first.time) / (next.time - first.time)
        return ((snapshot.downloadedBytes - startBytes).coerceAtLeast(0.0) * 1000 / (nowMs - start)).toLong()
    }

    private companion object { const val WINDOW_MS = 2_000L }
}

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

    fun enqueueApkDownload(release: GitHubRelease, acceleratorId: String, customPrefix: String): ReleaseDownloadRecord = synchronized(downloadLock) {
        val asset = release.appApk ?: throw IllegalStateException("最新发行版没有找到手机版 APK")
        val accelerator = ReleaseAccelerators.resolve(acceleratorId, customPrefix)
        val url = githubRequestUrl(asset.downloadUrl, accelerator.prefix)
        val filename = asset.name.replace(Regex("[^A-Za-z0-9._-]"), "_")
        require(filename.endsWith(".apk", ignoreCase = true)) { "发行版 APK 文件名无效" }
        val manager = appContext.getSystemService(DownloadManager::class.java)
            ?: throw IllegalStateException("Android 下载服务不可用")
        val previous = savedDownload()
        if (previous?.tag == release.tag && queryDownload(previous.id)?.status in setOf(
                DownloadManager.STATUS_PENDING, DownloadManager.STATUS_RUNNING, DownloadManager.STATUS_PAUSED)) {
            return@synchronized previous
        }
        // DownloadManager never overwrites an existing destination, including
        // a completed APK left behind after reinstalling the same version.
        val destination = "${filename.removeSuffix(".apk")}-${UUID.randomUUID()}.apk"
        val request = DownloadManager.Request(Uri.parse(url)).apply {
            setTitle("Hole ${release.tag}")
            setDescription("正在下载 Android 安装包")
            setMimeType(APK_MIME_TYPE)
            setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            setDestinationInExternalFilesDir(appContext, Environment.DIRECTORY_DOWNLOADS, destination)
        }
        val record = ReleaseDownloadRecord(manager.enqueue(request), release.tag)
        preferences.edit().putLong(KEY_DOWNLOAD_ID, record.id).putString(KEY_DOWNLOAD_TAG, record.tag).apply()
        previous?.let { manager.remove(it.id) }
        record
    }

    fun savedDownload(): ReleaseDownloadRecord? = synchronized(downloadLock) {
        val id = preferences.getLong(KEY_DOWNLOAD_ID, INVALID_DOWNLOAD_ID)
        val tag = preferences.getString(KEY_DOWNLOAD_TAG, null)
        if (id == INVALID_DOWNLOAD_ID || tag.isNullOrBlank()) null else ReleaseDownloadRecord(id, tag)
    }

    fun cancelDownload(record: ReleaseDownloadRecord) = synchronized(downloadLock) {
        val manager = appContext.getSystemService(DownloadManager::class.java)
            ?: throw IllegalStateException("Android 下载服务不可用")
        // remove also deletes the partial APK. A late cancellation must never
        // clear the persisted identity of a replacement download.
        manager.remove(record.id)
        if (savedDownload()?.id == record.id) {
            preferences.edit().remove(KEY_DOWNLOAD_ID).remove(KEY_DOWNLOAD_TAG).apply()
        }
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
        val downloadLock = Any()
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
