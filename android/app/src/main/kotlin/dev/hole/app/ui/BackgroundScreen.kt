package dev.hole.app.ui

import android.content.Intent
import android.provider.Settings
import androidx.core.net.toUri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import dev.hole.app.RunStateStore
import dev.hole.app.readBackgroundInfo

@Composable
fun BackgroundScreen(onBack: () -> Unit) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current
    var info by remember { mutableStateOf(readBackgroundInfo(context)) }
    var error by remember { mutableStateOf<String?>(null) }
    DisposableEffect(lifecycle, context) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_RESUME) info = readBackgroundInfo(context) }
        lifecycle.lifecycle.addObserver(observer)
        onDispose { lifecycle.lifecycle.removeObserver(observer) }
    }
    fun open(intent: Intent) {
        try { context.startActivity(intent) } catch (_: RuntimeException) { error = "请从系统应用信息中打开电池与后台运行设置" }
    }
    HoleScaffold(title = "后台运行", navigationIcon = { HoleBackButton(onBack) }) { insets ->
        Column(Modifier.fillMaxWidth().padding(insets).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)) {
            HoleSettingsGroup("运行状态") {
                DetailRow("电池优化", if (info.batteryExempt) "已豁免" else "受优化")
                DetailRow("后台限制", if (info.backgroundRestricted) "受限" else "未受限")
                DetailRow("状态通知", if (info.notificationsEnabled) "已开启" else "未开启")
                HoleButton(if (info.batteryExempt) "电池优化设置" else "允许忽略电池优化", onClick = {
                    if (info.batteryExempt) open(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
                    else open(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, "package:${context.packageName}".toUri()))
                }, secondary = true)
                HoleButton("应用信息与后台权限", onClick = {
                    open(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, "package:${context.packageName}".toUri()))
                }, secondary = true)
                HoleButton("通知设置", onClick = {
                    open(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName))
                }, secondary = true)
            }
            HoleSettingsGroup("自动恢复") {
                HoleSwitchPreference(
                    title = "设备重启后恢复", checked = info.resumeAfterBoot,
                    summary = "仅恢复重启前运行的连接；手动停止后不会恢复。",
                    onCheckedChange = { RunStateStore(context).setResumeAfterBoot(it); info = readBackgroundInfo(context) },
                )
                HoleHelp("进程恢复", "划掉最近任务不会停止转发；进程重建或升级后重新连接。")
                if (info.lastResumeError.isNotEmpty()) Text(info.lastResumeError, color = MaterialTheme.colorScheme.error)
            }
            HoleSettingsGroup("连接恢复") {
                HoleHelp("网络恢复", "网络切换后自动重连；仅 IPv6 模式下无公网 IPv6 时等待网络恢复。")
                HoleHelp("强制停止", "强制停止或系统清理后需重新启动，原 TCP 连接不会恢复。")
            }
            error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        }
    }
}
