package dev.hole.app

import android.content.Intent
import android.os.Looper
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.shadows.ShadowPowerManager
import java.time.Duration
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class BackgroundSupportTest {
    private val context get() = RuntimeEnvironment.getApplication()
    @Test fun recoveryLockExpiresAndRetriesDoNotReacquireOrExtendIt() {
        val holder = RecoveryWakeLock(context)
        holder.update(true)
        val lock = ShadowPowerManager.getLatestWakeLock()
        assertTrue(lock.isHeld)
        repeat(20) { holder.update(true) }
        assertEquals(1, shadowOf(lock).timesHeld)
        shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(31))
        assertFalse(lock.isHeld)
        holder.update(true)
        assertEquals(1, shadowOf(lock).timesHeld)
        holder.update(false); holder.update(true)
        assertTrue(lock.isHeld)
        holder.close(); holder.update(true)
        assertFalse(lock.isHeld)
    }
    @Test fun stoppedIntentAndOptOutNeverAutoStartOnBootOrUpgrade() {
        val store = RunStateStore(context)
        val receiver = ResumeReceiver()
        store.setRequested(false); store.setResumeAfterBoot(true)
        receiver.onReceive(context, Intent(Intent.ACTION_BOOT_COMPLETED))
        receiver.onReceive(context, Intent(Intent.ACTION_MY_PACKAGE_REPLACED))
        assertNull(shadowOf(context).nextStartedService)
        store.setRequested(true); store.setResumeAfterBoot(false)
        receiver.onReceive(context, Intent(Intent.ACTION_BOOT_COMPLETED))
        assertNull(shadowOf(context).nextStartedService)
        receiver.onReceive(context, Intent(Intent.ACTION_MY_PACKAGE_REPLACED))
        assertEquals(EngineService.ACTION_START_RUN, shadowOf(context).nextStartedService.action)
    }
    @Test fun explicitBootRecoveryOnlyRestoresRequestedRuns() {
        RunStateStore(context).apply { setRequested(true); setResumeAfterBoot(true) }
        ResumeReceiver().onReceive(context, Intent(Intent.ACTION_BOOT_COMPLETED))
        assertEquals(EngineService.ACTION_START_RUN, shadowOf(context).nextStartedService.action)
    }
}
