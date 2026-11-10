package dev.hole.app.update

import android.app.DownloadManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ReleaseDownloadTest {
    @Test fun cancellationRemovesSystemTaskAndSurvivesPageRecreation() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val client = ReleaseUpdateClient(context)
        val manager = context.getSystemService(DownloadManager::class.java)
        val release = GitHubRelease("v1.0.6", "Hole", "", "https://github.com/Haikure/hole/releases/tag/v1.0.6", "",
            listOf(ReleaseAsset("hole-1.0.6-release.apk", "https://github.com/Haikure/hole/releases/download/v1.0.6/hole-1.0.6-release.apk", 100)))
        for (state in listOf(DownloadManager.STATUS_PENDING, DownloadManager.STATUS_RUNNING, DownloadManager.STATUS_PAUSED)) {
            val first = client.enqueueApkDownload(release, ReleaseAccelerators.DIRECT, "")
            shadowOf(shadowOf(manager).getRequest(first.id)).setStatus(state)
            val restored = ReleaseUpdateClient(context)
            assertEquals(first, restored.savedDownload())
            restored.cancelDownload(first)
            assertNull(client.queryDownload(first.id))
            assertNull(client.savedDownload())
            val second = client.enqueueApkDownload(release, ReleaseAccelerators.DIRECT, "")
            restored.cancelDownload(first) // A delayed callback from the old page.
            assertEquals(second, client.savedDownload())
            assertNotNull(client.queryDownload(second.id))
            client.cancelDownload(second)
        }
    }

    @Test fun speedUsesRecentTwoSecondsIncludingStallsAndResets() {
        val speed = ReleaseDownloadSpeed()
        fun sample(ms: Long, bytes: Long, status: Int = DownloadManager.STATUS_RUNNING) =
            speed.sample(ReleaseDownloadSnapshot(status, bytes, -1, 0), ms)
        assertNull(sample(0, 5_000_000)) // Resuming a large download isn't a speed spike.
        assertEquals(1_000L, sample(500, 5_000_500))
        assertEquals(2_000L, sample(1_000, 5_002_000))
        assertEquals(2_000L, sample(2_000, 5_004_000))
        // Boundary at 250ms: interpolate 250 bytes of the first interval.
        assertEquals(1_875L, sample(2_250, 5_004_000))
        assertEquals(500L, sample(3_500, 5_004_000))
        assertEquals(0L, sample(4_000, 5_004_000))
        assertNull(sample(4_100, 5_004_000, DownloadManager.STATUS_PAUSED))
        assertNull(sample(4_200, 5_004_000))
        assertEquals(1_000L, sample(4_700, 5_004_500))
        assertNull(sample(5_000, 0)) // Download counter reset.
        assertNull(sample(8_000, 20_000)) // The app wasn't polling in the background.
        assertNull(sample(8_000, 20_000)) // Duplicate or nonmonotonic time.
        assertNull(sample(8_100, 20_000, DownloadManager.STATUS_SUCCESSFUL))
    }

    @Test fun progressHandlesUnknownSizeAndOutOfRangeSystemCounters() {
        fun progress(bytes: Long, total: Long) = ReleaseDownloadSnapshot(DownloadManager.STATUS_RUNNING, bytes, total, 0).progress
        assertNull(progress(20, -1))
        assertNull(progress(20, 0))
        assertEquals(0f, progress(-1, 100))
        assertEquals(0.5f, progress(50, 100))
        assertEquals(1f, progress(Long.MAX_VALUE, 100))
    }

    @Test fun redownloadUsesNewDestinationAndKeepsOnlyOneRecord() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val client = ReleaseUpdateClient(context)
        val manager = context.getSystemService(DownloadManager::class.java)
        val shadow = shadowOf(manager)
        val release = GitHubRelease("v1.0.6", "Hole", "", "https://github.com/Haikure/hole/releases/tag/v1.0.6", "",
            listOf(ReleaseAsset("hole-1.0.6-release.apk", "https://github.com/Haikure/hole/releases/download/v1.0.6/hole-1.0.6-release.apk", 100)))
        val first = client.enqueueApkDownload(release, ReleaseAccelerators.DIRECT, "")
        val firstRequest = shadow.getRequest(first.id)
        shadowOf(firstRequest).setStatus(DownloadManager.STATUS_RUNNING)
        assertEquals(first, client.enqueueApkDownload(release, ReleaseAccelerators.DIRECT, ""))
        shadowOf(firstRequest).setStatus(DownloadManager.STATUS_SUCCESSFUL)
        val second = client.enqueueApkDownload(release, ReleaseAccelerators.DIRECT, "")
        assertNotEquals(first.id, second.id)
        assertNotEquals(shadowOf(firstRequest).destination, shadowOf(shadow.getRequest(second.id)).destination)
        assertEquals(second, client.savedDownload())
        assertNull(client.queryDownload(first.id))
    }
}
