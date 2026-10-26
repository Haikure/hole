package dev.hole.app.ui

import android.content.Intent
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.net.toUri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.Slider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import dev.hole.app.RunStateStore
import dev.hole.app.FloatingBallPreferences
import dev.hole.app.FloatingBallService
import dev.hole.app.readBackgroundInfo
import kotlin.math.roundToInt

@Composable
fun BackgroundScreen(onBack: () -> Unit) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current
    var info by remember { mutableStateOf(readBackgroundInfo(context)) }
    var error by remember { mutableStateOf<String?>(null) }
    val ballPreferences = remember(context) { FloatingBallPreferences(context) }
    var ballEnabled by remember { mutableStateOf(ballPreferences.isEnabled()) }
    var ballSize by remember { mutableStateOf(ballPreferences.sizeDp().toFloat()) }
    var overlayGranted by remember { mutableStateOf(Settings.canDrawOverlays(context)) }
    var awaitingOverlayPermission by rememberSaveable { mutableStateOf(false) }
    fun setBallEnabled(enabled: Boolean) {
        error = null
        ballPreferences.setEnabled(enabled)
        try { FloatingBallService.sync(context) }
        catch (_: RuntimeException) { error = "悬浮球启动失败，请检查悬浮窗权限后重试" }
        ballEnabled = ballPreferences.isEnabled()
    }
    fun refresh() {
        info = readBackgroundInfo(context)
        overlayGranted = Settings.canDrawOverlays(context)
        if (awaitingOverlayPermission && overlayGranted) {
            awaitingOverlayPermission = false
            setBallEnabled(true)
        }
        if (!overlayGranted && ballPreferences.isEnabled()) setBallEnabled(false)
        ballEnabled = ballPreferences.isEnabled()
        ballSize = ballPreferences.sizeDp().toFloat()
    }
    val overlayPermissionLauncher = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        refresh()
        if (awaitingOverlayPermission) {
            awaitingOverlayPermission = false
            error = "尚未获得“显示在其他应用上层”权限，悬浮球未开启"
        }
    }
    DisposableEffect(lifecycle, context) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_RESUME) refresh() }
        lifecycle.lifecycle.addObserver(observer)
        onDispose { lifecycle.lifecycle.removeObserver(observer) }
    }
    fun open(intent: Intent) {
        try { context.startActivity(intent) } catch (_: RuntimeException) { error = "请从系统应用信息中打开电池与后台运行设置" }
    }
    HoleScaffold(title = "后台运行", navigationIcon = { HoleBackButton(onBack) }) { insets ->
        Column(Modifier.fillMaxWidth().padding(insets).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)) {
            HoleSettingsGroup("悬浮球") {
                HoleSwitchPreference(
                    title = "开启悬浮球", checked = ballEnabled,
                    summary = "在其他应用上方显示应用图标，配合前台服务保持后台运行；拖动移动，点按返回应用。",
                    onCheckedChange = { enabled ->
                        if (!enabled || Settings.canDrawOverlays(context)) setBallEnabled(enabled)
                        else {
                            error = null
                            awaitingOverlayPermission = true
                            try {
                                overlayPermissionLauncher.launch(Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION,
                                    "package:${context.packageName}".toUri()))
                            } catch (_: RuntimeException) {
                                awaitingOverlayPermission = false
                                error = "无法打开悬浮窗授权页，请在系统应用信息中允许“显示在其他应用上层”后重试"
                            }
                        }
                    },
                )
                DetailRow("悬浮窗权限", if (overlayGranted) "已允许" else "未允许")
                DetailRow("悬浮球大小", "${ballSize.roundToInt()} dp")
                Slider(
                    value = ballSize, onValueChange = { ballSize = it },
                    onValueChangeFinished = {
                        ballPreferences.setSizeDp(ballSize.roundToInt())
                        if (ballEnabled) setBallEnabled(true)
                    },
                    valueRange = FloatingBallPreferences.MIN_SIZE_DP.toFloat()..FloatingBallPreferences.MAX_SIZE_DP.toFloat(),
                    steps = 15,
                    modifier = Modifier.fillMaxWidth().semantics { contentDescription = "悬浮球大小" },
                )
                Text("开启后立即显示，无需先开启连接；关闭悬浮球不会停止转发。系统强制停止或清理后需重新打开应用。",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
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
