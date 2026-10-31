package dev.hole.app

import android.app.AppOpsManager
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.content.res.Configuration
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.provider.Settings
import android.widget.Toast
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch

/** A user-enabled visible overlay, independent of forwarding and microphone use. */
class FloatingBallService : Service() {
    private val preferences by lazy { FloatingBallPreferences(this) }
    private val ball by lazy { FloatingBallWindow(this, preferences) }
    private val handler = Handler(Looper.getMainLooper())
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var destroyed = false
    private val permissionWatcher = AppOpsManager.OnOpChangedListener { _, _ ->
        handler.post {
            if (!destroyed && !Settings.canDrawOverlays(this)) disable()
        }
    }

    override fun onCreate() {
        super.onCreate()
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL_ID, "悬浮球后台运行", NotificationManager.IMPORTANCE_LOW),
        )
        getSystemService(AppOpsManager::class.java).startWatchingMode(
            AppOpsManager.OPSTR_SYSTEM_ALERT_WINDOW, packageName, permissionWatcher,
        )
        serviceScope.launch {
            FloatingBallStateStore.snapshot.collect { snapshot ->
                ball.updateSnapshot(snapshot)
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_DISABLE || !preferences.isEnabled() || !Settings.canDrawOverlays(this)) {
            disable()
            return START_NOT_STICKY
        }
        try {
            val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java),
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
            val stop = PendingIntent.getService(this, 0, Intent(this, FloatingBallService::class.java).setAction(ACTION_DISABLE),
                PendingIntent.FLAG_IMMUTABLE)
            val notification = NotificationCompat.Builder(this, CHANNEL_ID)
                .setSmallIcon(R.drawable.ic_notification).setContentTitle("hole 悬浮球")
                .setContentText("悬浮球已开启，点按返回应用")
                .setOngoing(true).setOnlyAlertOnce(true).setContentIntent(open)
                .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
                .addAction(0, "关闭悬浮球", stop).build()
            val type = if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0
            ServiceCompat.startForeground(this, NOTIFICATION_ID, notification, type)
            ball.updateSnapshot(FloatingBallStateStore.snapshot.value)
            ball.show()
        } catch (_: RuntimeException) {
            disable()
            Toast.makeText(this, "无法显示悬浮球，请检查悬浮窗权限后重新开启", Toast.LENGTH_LONG).show()
            return START_NOT_STICKY
        }
        return START_STICKY
    }

    private fun disable() {
        preferences.setEnabled(false)
        ball.close()
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        // Recalculate size and normalized position after rotation / density changes.
        if (preferences.isEnabled()) onStartCommand(null, 0, 0)
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        destroyed = true
        getSystemService(AppOpsManager::class.java).stopWatchingMode(permissionWatcher)
        handler.removeCallbacksAndMessages(null)
        ball.close()
        serviceScope.cancel()
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        super.onDestroy()
    }

    companion object {
        const val ACTION_DISABLE = "dev.hole.app.action.DISABLE_FLOATING_BALL"
        private const val CHANNEL_ID = "floating_ball"
        private const val NOTIFICATION_ID = 2

        /** Called only from visible UI; never starts a forwarding connection. */
        fun sync(context: Context) {
            val preferences = FloatingBallPreferences(context)
            val intent = Intent(context, FloatingBallService::class.java)
            if (!preferences.isEnabled() || !Settings.canDrawOverlays(context)) {
                preferences.setEnabled(false)
                context.stopService(intent)
            } else {
                try { ContextCompat.startForegroundService(context, intent) }
                catch (failure: RuntimeException) {
                    preferences.setEnabled(false)
                    context.stopService(intent)
                    throw failure
                }
            }
        }
    }
}
