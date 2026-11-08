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
