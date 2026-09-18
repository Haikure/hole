package dev.hole.app

import android.app.ActivityManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.PowerManager
import androidx.core.app.NotificationManagerCompat

data class BackgroundInfo(
    val batteryExempt: Boolean = false,
    val backgroundRestricted: Boolean = false,
    val notificationsEnabled: Boolean = false,
    val resumeAfterBoot: Boolean = false,
    val lastResumeError: String = "",
)

fun readBackgroundInfo(context: Context) = BackgroundInfo(
    batteryExempt = context.getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(context.packageName),
    backgroundRestricted = Build.VERSION.SDK_INT >= 28 && context.getSystemService(ActivityManager::class.java).isBackgroundRestricted,
    notificationsEnabled = NotificationManagerCompat.from(context).areNotificationsEnabled(),
    resumeAfterBoot = RunStateStore(context).resumeAfterBoot(),
    lastResumeError = RunStateStore(context).lastResumeError(),
)

/** One bounded acquisition per recovery episode; repeated retries never extend it. */
class RecoveryWakeLock(context: Context) : AutoCloseable {
    private val wakeLock = context.getSystemService(PowerManager::class.java)
        .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "hole:network-recovery").apply { setReferenceCounted(false) }
    private var recovering = false
    private var closed = false
    @Synchronized fun update(value: Boolean) {
        if (closed) return
        if (value && !recovering) wakeLock.acquire(30_000L)
        if (!value && wakeLock.isHeld) wakeLock.release()
        recovering = value
    }
    @Synchronized override fun close() {
        update(false)
        closed = true
    }
}

/** Only system boot/update events. Explicit user Stop always wins persisted intent. */
class ResumeReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val store = RunStateStore(context)
        val permitted = when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED -> store.resumeAfterBoot()
            Intent.ACTION_MY_PACKAGE_REPLACED -> true
            else -> false
        }
        if (!permitted || !store.isRequested()) return
        try {
            EngineService.startRun(context)
            store.setResumeError("")
        } catch (failure: RuntimeException) {
            // No repeated alarms or forced process relaunch. Show the reason next
            // time the user opens the app and can explicitly start foreground work.
            store.setResumeError("系统未启动后台恢复，请打开应用重新开启连接（${failure.javaClass.simpleName}）")
        }
    }
}
