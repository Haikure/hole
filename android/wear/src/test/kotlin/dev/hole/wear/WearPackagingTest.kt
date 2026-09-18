package dev.hole.wear

import android.content.pm.PackageManager
import dev.hole.app.EngineService
import dev.hole.app.MainActivity
import dev.hole.app.ResumeReceiver
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26, 36], qualifiers = "w192dp-h192dp-round-watch")
class WearPackagingTest {
    @Suppress("DEPRECATION")
    @Test
    fun manifestDeclaresStandaloneWatchAndSharedHost() {
        val context = RuntimeEnvironment.getApplication()
        val info = context.packageManager.getPackageInfo(context.packageName,
            PackageManager.GET_CONFIGURATIONS or PackageManager.GET_META_DATA or
                PackageManager.GET_ACTIVITIES or PackageManager.GET_SERVICES or PackageManager.GET_RECEIVERS)
        val features = info.reqFeatures.orEmpty().associateBy { it.name }
        assertTrue(features.getValue("android.hardware.type.watch").flags and 1 != 0)
        for (name in listOf("android.hardware.touchscreen", "android.hardware.faketouch")) {
            assertEquals(0, features.getValue(name).flags and 1)
        }
        assertTrue(requireNotNull(info.applicationInfo).metaData.getBoolean("com.google.android.wearable.standalone"))
        assertTrue(info.activities.orEmpty().any { it.name == MainActivity::class.java.name && it.exported })
        assertTrue(info.services.orEmpty().any { it.name == EngineService::class.java.name && !it.exported })
        assertTrue(info.receivers.orEmpty().any { it.name == ResumeReceiver::class.java.name && !it.exported })
        assertFalse(info.applicationInfo!!.flags and android.content.pm.ApplicationInfo.FLAG_ALLOW_BACKUP != 0)
        assertEquals(36, info.applicationInfo!!.targetSdkVersion)
        assertEquals(26, info.applicationInfo!!.minSdkVersion)
    }
}
