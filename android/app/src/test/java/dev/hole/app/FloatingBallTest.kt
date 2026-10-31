package dev.hole.app

import android.app.AppOpsManager
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Looper
import android.provider.Settings
import android.view.MotionEvent
import android.view.ViewGroup
import android.view.WindowManager
import android.widget.ImageButton
import androidx.core.content.edit
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode
import org.robolectric.shadow.api.Shadow
import org.robolectric.shadows.ShadowSettings
import org.robolectric.shadows.ShadowWindowManagerImpl
import kotlin.math.roundToInt
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
@LooperMode(LooperMode.Mode.PAUSED)
class FloatingBallTest {
    private val context get() = RuntimeEnvironment.getApplication()
    private val preferences get() = FloatingBallPreferences(context)
    private fun windows(owner: android.content.Context) =
        Shadow.extract<ShadowWindowManagerImpl>(owner.getSystemService(WindowManager::class.java))

    @Before fun prepare() {
        context.getSharedPreferences("floating_ball", 0).edit(commit = true) { clear() }
        RunStateStore(context).setRequested(false)
        FloatingBallStateStore.setPanelExpanded(false)
        ShadowSettings.setCanDrawOverlays(false)
    }

    @Test fun permissionIsRequiredAndDoesNotStartForwarding() {
        preferences.setEnabled(true)
        FloatingBallService.sync(context)
        assertFalse(preferences.isEnabled())
        assertNull(shadowOf(context).nextStartedService)
        assertFalse(RunStateStore(context).isRequested())

        ShadowSettings.setCanDrawOverlays(true)
        preferences.setEnabled(true)
        FloatingBallService.sync(context)
        assertEquals(FloatingBallService::class.java.name, shadowOf(context).nextStartedService.component!!.className)
        assertFalse(RunStateStore(context).isRequested())
    }

    @Test fun serviceWithoutPermissionStopsWithoutAWindow() {
        preferences.setEnabled(true)
        val controller = Robolectric.buildService(FloatingBallService::class.java).create()
        val service = controller.get()
        try {
            assertEquals(Service.START_NOT_STICKY, service.onStartCommand(null, 0, 1))
            assertTrue(shadowOf(service).isStoppedBySelf)
            assertFalse(preferences.isEnabled())
            assertTrue(windows(service).views.isEmpty())
        } finally { controller.destroy() }
    }

    @Test fun enabledServiceSurvivesTaskRemovalAndNotificationStopOnlyDisablesTheBall() {
        ShadowSettings.setCanDrawOverlays(true)
        preferences.setEnabled(true)
        RunStateStore(context).setRequested(true)
        val controller = Robolectric.buildService(FloatingBallService::class.java).create()
        val service = controller.get()
        val windows = windows(service)
        try {
            assertEquals(Service.START_STICKY, service.onStartCommand(null, 0, 1))
            assertEquals(1, windows.views.size)
            val notification = assertNotNull(shadowOf(service).lastForegroundNotification)
            assertEquals("关闭悬浮球", notification.actions.single().title)
            service.onTaskRemoved(Intent())
            assertFalse(shadowOf(service).isStoppedBySelf)
            assertEquals(1, windows.views.size)
            service.onStartCommand(Intent().setAction(FloatingBallService.ACTION_DISABLE), 0, 2)
            assertTrue(windows.views.isEmpty())
            assertTrue(shadowOf(service).isStoppedBySelf)
            assertFalse(preferences.isEnabled())
            assertTrue(RunStateStore(context).isRequested())
        } finally { controller.destroy() }
    }

    @Test fun destructionRemovesWindowAndStickyRecreationRestoresOneWindow() {
        ShadowSettings.setCanDrawOverlays(true)
        preferences.setEnabled(true)
        val first = Robolectric.buildService(FloatingBallService::class.java).create()
        val manager = windows(first.get())
        first.get().onStartCommand(null, 0, 1)
        first.destroy()
        assertTrue(manager.views.isEmpty())
        assertTrue(preferences.isEnabled())

        val second = Robolectric.buildService(FloatingBallService::class.java).create()
        try {
            second.get().onStartCommand(null, 0, 1)
            second.get().onStartCommand(null, 0, 2)
            assertEquals(1, windows(second.get()).views.size)
        } finally { second.destroy() }
    }

    @Test fun revokingPermissionRemovesWindowAndClearsOptIn() {
        ShadowSettings.setCanDrawOverlays(true)
        preferences.setEnabled(true)
        val controller = Robolectric.buildService(FloatingBallService::class.java).create()
        val service = controller.get()
        try {
            service.onStartCommand(null, 0, 1)
            ShadowSettings.setCanDrawOverlays(false)
            val appOps = service.getSystemService(AppOpsManager::class.java)
            shadowOf(appOps).setMode(AppOpsManager.OPSTR_SYSTEM_ALERT_WINDOW,
                context.applicationInfo.uid, context.packageName, AppOpsManager.MODE_ERRORED)
            shadowOf(Looper.getMainLooper()).idle()
            assertFalse(preferences.isEnabled())
            assertTrue(shadowOf(service).isStoppedBySelf)
            assertTrue(windows(service).views.isEmpty())
            assertFalse(Settings.canDrawOverlays(context))
        } finally { controller.destroy() }
    }

    @Test @Config(sdk = [34])
    fun modernServiceUsesSpecialUseWithoutMicrophone() {
        ShadowSettings.setCanDrawOverlays(true)
        preferences.setEnabled(true)
        val controller = Robolectric.buildService(FloatingBallService::class.java).create()
        try {
            controller.get().onStartCommand(null, 0, 1)
            assertEquals(ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE, controller.get().foregroundServiceType)
            assertEquals(1, windows(controller.get()).views.size)
        } finally { controller.destroy() }
    }

    @Test fun dragStaysOnScreenAndTapExpandsTheSummaryPanel() {
        val window = FloatingBallWindow(context, preferences)
        try {
            window.show()
            val icon = assertNotNull(window.view)
            val layout = icon.layoutParams as WindowManager.LayoutParams
            assertEquals(WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY, layout.type)
            assertTrue(layout.flags and WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE != 0)
            fun touch(action: Int, x: Float, y: Float) {
                MotionEvent.obtain(0, 1, action, x, y, 0).also { icon.dispatchTouchEvent(it); it.recycle() }
            }
            touch(MotionEvent.ACTION_DOWN, 20f, 20f)
            touch(MotionEvent.ACTION_MOVE, -10_000f, -10_000f)
            touch(MotionEvent.ACTION_UP, -10_000f, -10_000f)
            assertTrue(layout.x >= 0)
            assertTrue(layout.y >= 0)
            assertNull(shadowOf(context).nextStartedActivity)
            window.close()
            preferences.setSizeDp(96)
            window.show()
            val restored = assertNotNull(window.view).layoutParams as WindowManager.LayoutParams
            assertTrue(restored.x >= 0)
            assertTrue(restored.y >= 0)
            assertEquals((96 * context.resources.displayMetrics.density).roundToInt(), restored.width)
            window.view!!.performClick()
            val root = assertNotNull(window.view) as ViewGroup
            val details = assertNotNull(root.findViewWithTag<ImageButton>("floating-details-action"))
            assertEquals("打开详情", details.contentDescription)
            details.performClick()
            assertEquals(MainActivity::class.java.name, shadowOf(context).nextStartedActivity.component!!.className)
        } finally { window.close() }
    }

    @Test fun resizeUpdatesAnExistingWindowAndClampsPersistedSizes() {
        val window = FloatingBallWindow(context, preferences)
        try {
            preferences.setSizeDp(Int.MAX_VALUE)
            window.show()
            val icon = assertNotNull(window.view)
            val layout = icon.layoutParams as WindowManager.LayoutParams
            assertEquals((96 * context.resources.displayMetrics.density).roundToInt(), layout.width)
            preferences.setSizeDp(Int.MIN_VALUE)
            window.show()
            assertTrue(icon === window.view)
            assertEquals((32 * context.resources.displayMetrics.density).roundToInt(), layout.width)
            assertTrue(layout.x >= 0 && layout.y >= 0)
        } finally { window.close() }
    }
}
