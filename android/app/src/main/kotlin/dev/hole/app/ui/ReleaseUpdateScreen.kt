package dev.hole.app.ui

import android.app.DownloadManager
import android.content.Intent
import android.net.Uri
import android.os.Build
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
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import dev.hole.app.BuildConfig
import dev.hole.app.update.GitHubRelease
import dev.hole.app.update.ReleaseAccelerators
import dev.hole.app.update.ReleaseDownloadRecord
import dev.hole.app.update.ReleaseDownloadSnapshot
import dev.hole.app.update.ReleaseUpdateClient
import dev.hole.app.update.compareReleaseVersions
import dev.hole.app.update.validateAcceleratorPrefix
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
fun ReleaseUpdateScreen(onBack: () -> Unit) {
    val context = LocalContext.current
    val client = remember(context) { ReleaseUpdateClient(context) }
    val scope = rememberCoroutineScope()
    var acceleratorId by rememberSaveable { mutableStateOf(client.selectedAccelerator()) }
    var customPrefix by rememberSaveable { mutableStateOf(client.customPrefix()) }
    var release by remember { mutableStateOf<GitHubRelease?>(null) }
    var download by remember { mutableStateOf(client.savedDownload()) }
    var downloadStatus by remember { mutableStateOf<ReleaseDownloadSnapshot?>(null) }
    var checking by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(download?.id) {
        val current = download ?: return@LaunchedEffect
        while (true) {
            val latest = withContext(Dispatchers.IO) { client.queryDownload(current.id) }
            downloadStatus = latest
            if (latest == null || latest.status == DownloadManager.STATUS_SUCCESSFUL || latest.status == DownloadManager.STATUS_FAILED) {
                break
            }
            delay(900)
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
        checking = true
        scope.launch {
            try {
                release = withContext(Dispatchers.IO) { client.fetchLatest(acceleratorId, customPrefix) }
                error = null
                val record = download
                if (record != null && record.tag == release?.tag) {
                    downloadStatus = withContext(Dispatchers.IO) { client.queryDownload(record.id) }
                }
            } catch (failure: Exception) {
                error = failure.message ?: "检查更新失败，请稍后重试"
            } finally {
                checking = false
            }
        }
    }

    fun downloadApk(item: GitHubRelease) {
        error = null
        if (acceleratorId == ReleaseAccelerators.CUSTOM && customPrefix.isBlank()) {
            error = "请输入自定义加速服务的 HTTPS 前缀，或选择其他线路"
            return
        }
        runCatching {
            if (acceleratorId == ReleaseAccelerators.CUSTOM) validateAcceleratorPrefix(customPrefix)
            client.enqueueApkDownload(item, acceleratorId, customPrefix)
        }.onSuccess {
            download = it
            downloadStatus = null
        }.onFailure {
            error = it.message ?: "无法开始下载"
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
                error = "请允许 Hole 安装未知应用，然后返回此页点“安装更新”"
                return
            }
            val uri = client.downloadedUri(record.id) ?: throw IllegalStateException("找不到已下载的 APK")
            context.startActivity(
                Intent(Intent.ACTION_VIEW).apply {
                    setDataAndType(uri, APK_MIME_TYPE)
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                },
            )
        } catch (failure: Exception) {
            error = failure.message ?: "无法打开 Android 安装程序"
        }
    }

    HoleScaffold(title = "应用更新", navigationIcon = { HoleBackButton(onBack) }) { insets ->
        Column(
            Modifier.fillMaxWidth().padding(insets).verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            HoleSettingsGroup("当前版本") {
                Text("Hole ${BuildConfig.VERSION_NAME}", style = MaterialTheme.typography.titleMedium)
                Text("检查最新正式发行版。", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                HoleButton("检查更新", ::checkForUpdates, enabled = !checking)
                if (checking) Text("正在获取 GitHub 最新发行版…", style = MaterialTheme.typography.bodyMedium)
            }

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

            error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium) }

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
                            comparison == 0 -> "当前已是最新版本。"
                            else -> "本机版本高于 GitHub 当前正式发行版。"
                        },
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    val matchingDownload = download?.takeIf { it.tag == item.tag && hasUpdate }
                    val status = if (matchingDownload != null) downloadStatus else null
                    when {
                        matchingDownload != null && status?.status == DownloadManager.STATUS_SUCCESSFUL ->
                            HoleButton("安装更新", { installApk(matchingDownload) })
                        matchingDownload != null && status?.status?.let { it in setOf(
                            DownloadManager.STATUS_PENDING,
                            DownloadManager.STATUS_RUNNING,
                            DownloadManager.STATUS_PAUSED,
                        ) } == true -> Text(downloadStatusText(requireNotNull(status)))
                        hasUpdate && item.appApk != null ->
                            HoleButton("下载 APK", { downloadApk(item) })
                        item.appApk == null ->
                            Text("发行版没有附带手机版 APK，可打开发行页面查看。", style = MaterialTheme.typography.bodyMedium)
                    }
                    item.appApk?.let { Text("安装包：${it.name} · ${formatBytes(it.size)}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                    HoleTextButton("查看 GitHub 发行页面", { openReleasePage(item) })
                }
                if (item.notes.isNotBlank()) {
                    HoleSettingsGroup("发行说明") {
                        Text(item.notes, style = MaterialTheme.typography.bodyMedium)
                    }
                }
            }
            Spacer(Modifier.height(LocalFloatingInset.current + 12.dp))
        }
    }
}

private fun downloadStatusText(status: ReleaseDownloadSnapshot): String = when (status.status) {
    DownloadManager.STATUS_PENDING -> "下载已排队…"
    DownloadManager.STATUS_PAUSED -> "下载已暂停（${status.reason}），稍后会自动继续。"
    DownloadManager.STATUS_RUNNING -> {
        val total = status.totalBytes
        if (total > 0L) "正在下载：${formatBytes(status.downloadedBytes)} / ${formatBytes(total)}"
        else "正在下载 APK…"
    }
    else -> "下载中…"
}

private fun formatBytes(value: Long): String = when {
    value >= 1024L * 1024L -> "%.1f MiB".format(value / (1024.0 * 1024.0))
    value >= 1024L -> "%.0f KiB".format(value / 1024.0)
    else -> "$value B"
}

private const val APK_MIME_TYPE = "application/vnd.android.package-archive"
