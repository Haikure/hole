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
    HoleScaffold(title = "后台保持", navigationIcon = { HoleBackButton(onBack) }) { insets ->
        Column(Modifier.fillMaxWidth().padding(insets).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)) {
            HoleSettingsGroup("当前运行条件") {
                DetailRow("电池优化", if (info.batteryExempt) "已允许不受电池优化限制" else "系统仍在优化电池使用")
                DetailRow("后台限制", if (info.backgroundRestricted) "系统限制了后台运行" else "未检测到系统后台限制")
                DetailRow("状态通知", if (info.notificationsEnabled) "已允许显示" else "未允许显示；前台服务状态仍可在系统任务管理器查看")
                HoleButton(if (info.batteryExempt) "查看电池优化设置" else "允许不受电池优化限制", onClick = {
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
                    summary = "仅恢复重启前已开启的连接；手动停止后保持停止，首次启动默认关闭。",
                    onCheckedChange = { RunStateStore(context).setResumeAfterBoot(it); info = readBackgroundInfo(context) },
                )
                Text("退出页面或划掉最近任务保留当前前台服务。系统允许的进程重建、覆盖升级按已保存的运行意图重新连接。",
                    style = MaterialTheme.typography.bodyMedium)
                if (info.lastResumeError.isNotEmpty()) Text(info.lastResumeError, color = MaterialTheme.colorScheme.error)
            }
            HoleSettingsGroup("恢复方式") {
                Text("切换 Wi-Fi / 移动网络后自动重新加入信令并恢复有效会话；新网络无公网 IPv6 时等待网络恢复，不反复启动服务。")
                Text("每次进入恢复阶段最多持有 30 秒唤醒锁，连接恢复或用户停止即释放；持续重试不延长这一时限。")
                Text("强制停止、厂商清理和系统终止会结束进程；再次启动创建新运行实例，原 TCP socket 不跨进程恢复。")
                Text("在有额外后台管理的机型上，可在系统中允许自启动、设为不受限制，并锁定最近任务。",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        }
    }
}
