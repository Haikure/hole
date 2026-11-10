package dev.hole.app.ui

import android.app.DownloadManager
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.SystemClock
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import android.provider.Settings
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.key
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import dev.hole.app.BuildConfig
import dev.hole.app.update.GitHubRelease
import dev.hole.app.update.ReleaseAccelerators
import dev.hole.app.update.ReleaseDownloadRecord
import dev.hole.app.update.ReleaseDownloadSnapshot
import dev.hole.app.update.ReleaseDownloadSpeed
import dev.hole.app.update.ReleaseUpdateClient
import dev.hole.app.update.compareReleaseVersions
import dev.hole.app.update.validateAcceleratorPrefix
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
fun ReleaseUpdateScreen(onBack: () -> Unit) {
    val context = LocalContext.current
    val client = remember(context) { ReleaseUpdateClient(context) }
    val scope = rememberCoroutineScope()
    val scrollState = rememberScrollState()
    var acceleratorId by rememberSaveable { mutableStateOf(client.selectedAccelerator()) }
    var customPrefix by rememberSaveable { mutableStateOf(client.customPrefix()) }
    var release by remember { mutableStateOf<GitHubRelease?>(null) }
    var download by remember { mutableStateOf<ReleaseDownloadRecord?>(null) }
    var downloadLoaded by remember { mutableStateOf(false) }
    var changingDownload by remember { mutableStateOf(false) }
    var bytesPerSecond by remember { mutableStateOf<Long?>(null) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    var downloadStatus by remember { mutableStateOf<ReleaseDownloadSnapshot?>(null) }
    var queriedDownloadId by remember { mutableStateOf<Long?>(null) }
    var checking by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(client) {
        download = withContext(Dispatchers.IO) { client.savedDownload() }
        downloadLoaded = true
    }

    LaunchedEffect(download?.id, lifecycle) {
        downloadStatus = null
        queriedDownloadId = null
        bytesPerSecond = null
        val current = download ?: return@LaunchedEffect
        launch { scrollState.animateScrollTo(0) }
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            val speed = ReleaseDownloadSpeed()
            bytesPerSecond = null
            while (true) {
                try {
                    val latest = withContext(Dispatchers.IO) { client.queryDownload(current.id) }
                    if (download?.id != current.id) return@repeatOnLifecycle
                    downloadStatus = latest
                    queriedDownloadId = current.id
                    bytesPerSecond = latest?.let { speed.sample(it, SystemClock.elapsedRealtime()) }
                    if (latest == null || !latest.active) break
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (failure: Exception) {
                    bytesPerSecond = null
                    error = failure.message ?: "读取下载状态失败，请稍后重试"
                }
                delay(500)
            }
        }
    }

    fun openReleasePage(item: GitHubRelease) {
        runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(item.pageUrl))) }
            .onFailure { error = "无法打开 GitHub 发行页面" }
    }

    fun checkForUpdates() {
        error = null
        if (acceleratorId == ReleaseAccelerators.CUSTOM && customPrefix.isBlank()) {
            error = "请输入自定义加速服务的 HTTPS 前缀，或选择其他线路"
            return
        }
        runCatching {
            if (acceleratorId == ReleaseAccelerators.CUSTOM) validateAcceleratorPrefix(customPrefix)
        }.onFailure {
            error = it.message ?: "加速服务地址无效"
            return
        }
        client.saveSelectedAccelerator(acceleratorId)
        client.saveCustomPrefix(customPrefix)
        if (checking) return
        val selectedId = acceleratorId
        val selectedPrefix = customPrefix
        checking = true
        scope.launch {
            try {
                release = withContext(Dispatchers.IO) { client.fetchLatest(selectedId, selectedPrefix) }
                error = null
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                error = failure.message ?: "检查更新失败，请稍后重试"
            } finally {
                checking = false
            }
        }
    }

    fun downloadApk(item: GitHubRelease) {
        if (changingDownload || !downloadLoaded) return
        error = null
        if (acceleratorId == ReleaseAccelerators.CUSTOM && customPrefix.isBlank()) {
            error = "请输入自定义加速服务的 HTTPS 前缀，或选择其他线路"
            return
        }
        val selectedId = acceleratorId
        val selectedPrefix = customPrefix
        changingDownload = true
        scope.launch {
            try {
                // Finish the short system-service transaction even if this page
                // closes; the saved record lets the next page recover the task.
                download = withContext(NonCancellable + Dispatchers.IO) {
                    client.enqueueApkDownload(item, selectedId, selectedPrefix)
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                error = failure.message ?: "无法开始下载"
            } finally {
                changingDownload = false
            }
        }
    }

    fun cancelDownload(record: ReleaseDownloadRecord) {
        if (changingDownload) return
        changingDownload = true
        error = null
        scope.launch {
            try {
                withContext(NonCancellable + Dispatchers.IO) { client.cancelDownload(record) }
                if (download?.id == record.id) {
                    download = null
                    downloadStatus = null
                    bytesPerSecond = null
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                error = failure.message ?: "取消下载失败，请重试"
            } finally {
                changingDownload = false
            }
        }
    }

    fun installApk(record: ReleaseDownloadRecord) {
        error = null
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !context.packageManager.canRequestPackageInstalls()) {
                context.startActivity(
                    Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES)
                        .setData(Uri.parse("package:${context.packageName}")),
                )
                error = "请允许 Hole 安装未知应用，然后返回此页安装 APK"
                return
            }
            scope.launch {
                try {
                    val uri = withContext(Dispatchers.IO) { client.downloadedUri(record.id) }
                        ?: throw IllegalStateException("找不到已下载的 APK")
                    context.startActivity(Intent(Intent.ACTION_VIEW).apply {
                        setDataAndType(uri, APK_MIME_TYPE)
                        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                    })
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (failure: Exception) {
                    error = failure.message ?: "无法打开 Android 安装程序"
                }
            }
        } catch (failure: Exception) {
            error = failure.message ?: "无法打开 Android 安装程序"
        }
    }

    HoleScaffold(title = "应用更新", navigationIcon = { HoleBackButton(onBack) }) { insets ->
        Column(
            Modifier.fillMaxWidth().padding(insets).verticalScroll(scrollState)
                .padding(horizontal = 20.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            HoleSettingsGroup("当前版本") {
                Text("Hole ${BuildConfig.VERSION_NAME}", style = MaterialTheme.typography.titleMedium)
                Text("检查最新正式发行版。", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                HoleButton("检查更新", ::checkForUpdates, enabled = !checking)
                if (checking) {
                    LinearProgressIndicator(Modifier.fillMaxWidth())
                    Text("正在获取 GitHub 最新发行版…", style = MaterialTheme.typography.bodyMedium)
                }
            }

            download?.let { record ->
                key(record.id) {
                    HoleSettingsGroup("安装包下载 · ${record.tag}") {
                        ReleaseApkActions(
                            compareReleaseVersions(record.tag, BuildConfig.VERSION_NAME), true, true,
                            queriedDownloadId != record.id, downloadStatus,
                            onDownload = release?.takeIf { it.tag == record.tag }?.let { item -> { downloadApk(item) } },
                            onInstall = { installApk(record) },
                            onCancel = { cancelDownload(record) },
                            bytesPerSecond = bytesPerSecond,
                            busy = changingDownload,
                        )
                    }
                }
            }

            error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium) }

            HoleSettingsGroup("GitHub 加速服务") {
                Column(Modifier.selectableGroup()) {
                    ReleaseAccelerators.presets.forEach { preset ->
                        Row(
                            Modifier.fillMaxWidth().selectable(
                                selected = acceleratorId == preset.id,
                                role = Role.RadioButton,
                                onClick = {
                                    acceleratorId = preset.id
                                    client.saveSelectedAccelerator(preset.id)
                                    error = null
                                },
                            ),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(selected = acceleratorId == preset.id, onClick = null)
                            Column(Modifier.padding(start = 8.dp)) {
                                Text(preset.title, style = MaterialTheme.typography.titleSmall)
                                Text(preset.summary, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                    }
                }
                if (acceleratorId == ReleaseAccelerators.CUSTOM) {
                    HoleTextField(
                        value = customPrefix,
                        onValueChange = {
                            customPrefix = it
                            client.saveCustomPrefix(it)
                            error = null
                        },
                        label = "自定义加速前缀",
                        supportingText = { Text("填写服务提供的 HTTPS URL 前缀；自定义线路会同时代理发行信息和 APK 下载。") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }

            release?.let { item ->
                val comparison = compareReleaseVersions(item.tag, BuildConfig.VERSION_NAME)
                val hasUpdate = comparison != null && comparison > 0
                HoleSettingsGroup("最新发行版") {
                    Text(item.name, style = MaterialTheme.typography.titleLarge)
                    Text(
                        listOf(item.tag, item.publishedAt.take(10)).filter(String::isNotBlank).joinToString(" · "),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Text(
                        when {
                            comparison == null -> "无法识别版本号，请查看发行说明确认是否需要更新。"
                            hasUpdate -> "发现新版本，可下载 APK 后按 Android 提示安装。"
                            comparison == 0 -> "当前已是最新版本，仍可重新下载并安装。"
                            else -> "本机版本高于 GitHub 当前正式发行版。"
                        },
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    val matchingDownload = download?.takeIf { it.tag == item.tag }
                    if (matchingDownload == null) {
                        ReleaseApkActions(comparison, item.appApk != null, false, false, null,
                            onDownload = { downloadApk(item) }, onInstall = {},
                            busy = changingDownload || !downloadLoaded)
                    }
                    item.appApk?.let { Text("安装包：${it.name} · ${formatBytes(it.size)}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                    HoleTextButton("查看 GitHub 发行页面", { openReleasePage(item) })
                }
                if (item.notes.isNotBlank()) {
                    HoleSettingsGroup("发行说明") {
                        ReleaseNotes(item.notes, item.pageUrl)
                    }
                }
            }
            Spacer(Modifier.height(LocalFloatingInset.current + 12.dp))
        }
    }
}

@Composable
internal fun ReleaseApkActions(
    comparison: Int?, hasApk: Boolean, hasDownload: Boolean,
    querying: Boolean, status: ReleaseDownloadSnapshot?,
    onDownload: (() -> Unit)?, onInstall: () -> Unit,
    onCancel: () -> Unit = {}, bytesPerSecond: Long? = null, busy: Boolean = false,
) {
    when {
        querying -> {
            Text("正在确认下载状态…")
            LinearProgressIndicator(Modifier.fillMaxWidth())
            HoleTextButton(if (busy) "正在取消…" else "取消下载", onCancel, enabled = !busy)
        }
        status?.active == true -> ReleaseDownloadProgress(status, bytesPerSecond, busy, onCancel)
        !hasApk -> Text("发行版没有附带手机版 APK，可打开发行页面查看。", style = MaterialTheme.typography.bodyMedium)
        comparison == null || comparison < 0 -> Unit
        else -> {
            if (status?.status == DownloadManager.STATUS_SUCCESSFUL) {
                Text("下载完成 · ${formatBytes(status.downloadedBytes.coerceAtLeast(0))}", style = MaterialTheme.typography.bodyMedium)
                HoleButton(if (comparison == 0) "重新安装" else "安装更新", onInstall, enabled = !busy)
                if (onDownload != null) HoleTextButton("重新下载 APK", onDownload, enabled = !busy)
            } else {
                if (status?.status == DownloadManager.STATUS_FAILED) Text("下载失败，请重新下载或切换线路。", color = MaterialTheme.colorScheme.error)
                if (onDownload != null) {
                    HoleButton(if (busy) "正在创建下载…" else if (comparison == 0 || hasDownload) "重新下载 APK" else "下载 APK",
                        onDownload, enabled = !busy)
                } else Text("请检查更新后重新下载。", style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}

@Composable
internal fun ReleaseDownloadProgress(status: ReleaseDownloadSnapshot, bytesPerSecond: Long?,
    cancelling: Boolean, onCancel: () -> Unit) {
    val progress = status.progress
    val animatedProgress by animateFloatAsState(progress ?: 0f, tween(450), label = "APK download progress")
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically) {
            Text(downloadStatusText(status), Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
            if (progress != null) Text("${(progress * 100).toInt()}%", style = MaterialTheme.typography.labelLarge)
        }
        if (progress == null) {
            LinearProgressIndicator(Modifier.fillMaxWidth().testTag("apk-progress"))
        } else {
            LinearProgressIndicator(progress = { animatedProgress },
                modifier = Modifier.fillMaxWidth().testTag("apk-progress"))
        }
        Text(
            if (status.totalBytes > 0) "${formatBytes(status.downloadedBytes.coerceAtLeast(0))} / ${formatBytes(status.totalBytes)}"
            else "已下载 ${formatBytes(status.downloadedBytes.coerceAtLeast(0))}",
            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (status.status == DownloadManager.STATUS_RUNNING) {
            Text(bytesPerSecond?.let { "${formatBytes(it)}/s · 近 2 秒" } ?: "正在测量速度…",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        HoleTextButton(if (cancelling) "正在取消…" else "取消下载", onCancel, enabled = !cancelling)
    }
}

private fun downloadStatusText(status: ReleaseDownloadSnapshot): String = when (status.status) {
    DownloadManager.STATUS_PENDING -> "等待开始下载…"
    DownloadManager.STATUS_PAUSED -> when (status.reason) {
        DownloadManager.PAUSED_WAITING_FOR_NETWORK -> "等待网络连接"
        DownloadManager.PAUSED_QUEUED_FOR_WIFI -> "等待 Wi-Fi"
        DownloadManager.PAUSED_WAITING_TO_RETRY -> "等待自动重试"
        else -> "下载暂停，等待自动恢复"
    }
    else -> "正在下载"
}

private fun formatBytes(value: Long): String = when {
    value >= 1024L * 1024L -> "%.1f MiB".format(value / (1024.0 * 1024.0))
    value >= 1024L -> "%.0f KiB".format(value / 1024.0)
    else -> "$value B"
}

private const val APK_MIME_TYPE = "application/vnd.android.package-archive"
