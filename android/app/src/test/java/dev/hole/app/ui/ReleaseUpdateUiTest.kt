package dev.hole.app.ui

import android.app.DownloadManager
import android.content.Context
import android.graphics.Color
import android.text.TextPaint
import android.text.style.MetricAffectingSpan
import android.text.style.StrikethroughSpan
import android.widget.TextView
import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.*
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.test.core.app.ApplicationProvider
import dev.hole.app.config.ThemeMode
import dev.hole.app.update.ReleaseDownloadSnapshot
import dev.hole.app.update.ReleaseUpdateClient
import dev.hole.app.update.GitHubRelease
import dev.hole.app.update.ReleaseAsset
import dev.hole.app.update.ReleaseAccelerators
import io.noties.markwon.core.spans.HeadingSpan
import io.noties.markwon.core.spans.LinkSpan
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ReleaseUpdateUiTest {
    @get:Rule val compose = createComposeRule()

    @Test fun reopenedPageCanCancelSavedDownloadWithoutCheckingRelease() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val client = ReleaseUpdateClient(context)
        val release = GitHubRelease("v99.0.0", "Hole", "", "https://github.com/Haikure/hole/releases/tag/v99.0.0", "",
            listOf(ReleaseAsset("hole-99.0.0-release.apk", "https://github.com/Haikure/hole/releases/download/v99.0.0/hole-99.0.0-release.apk", 100)))
        val record = client.enqueueApkDownload(release, ReleaseAccelerators.DIRECT, "")
        val manager = context.getSystemService(DownloadManager::class.java)
        shadowOf(shadowOf(manager).getRequest(record.id)).setStatus(DownloadManager.STATUS_RUNNING)
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) { ReleaseUpdateScreen {} } }
        compose.waitUntil(5_000) { compose.onAllNodesWithText("正在下载").fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithText("取消下载").performScrollTo().performClick()
        compose.waitUntil(5_000) { client.savedDownload() == null }
        compose.onNodeWithText("取消下载").assertDoesNotExist()
        assertNull(client.queryDownload(record.id))
    }

    @Test fun downloadProgressAnimatesAndSupportsCancellationWithoutKnownSize() {
        var status by mutableStateOf(ReleaseDownloadSnapshot(DownloadManager.STATUS_RUNNING, 1024, 4096, 0))
        var cancelling by mutableStateOf(false)
        var cancels = 0
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            ReleaseApkActions(1, true, true, false, status, {}, {},
                onCancel = { cancels++; cancelling = true }, bytesPerSecond = 2048, busy = cancelling)
        } }
        compose.onNodeWithText("25%").assertExists()
        compose.onNodeWithText("2 KiB/s · 近 2 秒").assertExists()
        compose.onNodeWithTag("apk-progress").assertRangeInfoEquals(androidx.compose.ui.semantics.ProgressBarRangeInfo(.25f, 0f..1f))
        compose.mainClock.autoAdvance = false
        compose.runOnIdle { status = status.copy(downloadedBytes = 3072) }
        compose.mainClock.advanceTimeByFrame()
        compose.waitForIdle()
        compose.mainClock.advanceTimeByFrame()
        compose.mainClock.advanceTimeBy(160)
        val interim = compose.onNodeWithTag("apk-progress").fetchSemanticsNode().config[androidx.compose.ui.semantics.SemanticsProperties.ProgressBarRangeInfo].current
        assertTrue("Progress should interpolate rather than jump: $interim", interim > .25f && interim < .75f)
        compose.mainClock.advanceTimeBy(500)
        compose.mainClock.autoAdvance = true
        compose.onNodeWithTag("apk-progress").assertRangeInfoEquals(androidx.compose.ui.semantics.ProgressBarRangeInfo(.75f, 0f..1f))
        compose.runOnIdle { status = status.copy(totalBytes = -1) }
        compose.onNodeWithTag("apk-progress").assertRangeInfoEquals(androidx.compose.ui.semantics.ProgressBarRangeInfo.Indeterminate)
        compose.onNodeWithText("75%").assertDoesNotExist()
        compose.onNodeWithText("已下载 3 KiB").assertExists()
        compose.runOnIdle { status = status.copy(status = DownloadManager.STATUS_PAUSED, reason = DownloadManager.PAUSED_WAITING_FOR_NETWORK) }
        compose.onNodeWithText("等待网络连接").assertExists()
        compose.onNodeWithText("2 KiB/s · 近 2 秒").assertDoesNotExist()
        compose.onNodeWithText("取消下载").performClick()
        assertEquals(1, cancels)
        compose.onNodeWithText("正在取消…").assertIsNotEnabled()
        compose.onNodeWithText("下载 APK").assertDoesNotExist()
    }

    @Test fun latestVersionAllowsDownloadReinstallAndRetryWithoutDuplicateButtons() {
        var status by mutableStateOf<ReleaseDownloadSnapshot?>(null)
        var comparison by mutableStateOf<Int?>(0)
        var querying by mutableStateOf(false)
        var downloads = 0
        var installs = 0
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            Column { ReleaseApkActions(comparison, true, status != null, querying, status, { downloads++ }, { installs++ }) }
        } }
        compose.onNodeWithText("重新下载 APK").performClick()
        assertEquals(1, downloads)
        compose.runOnIdle { querying = true }
        compose.onNodeWithText("正在确认下载状态…").assertExists()
        compose.onNodeWithText("重新下载 APK").assertDoesNotExist()
        for (state in listOf(DownloadManager.STATUS_PENDING, DownloadManager.STATUS_RUNNING, DownloadManager.STATUS_PAUSED)) {
            compose.runOnIdle { querying = false; status = ReleaseDownloadSnapshot(state, 5, 10, 0) }
            compose.onNodeWithText("重新下载 APK").assertDoesNotExist()
        }
        compose.runOnIdle { status = ReleaseDownloadSnapshot(DownloadManager.STATUS_SUCCESSFUL, 10, 10, 0) }
        compose.onNodeWithText("重新安装").performClick()
        compose.onNodeWithText("重新下载 APK").performClick()
        assertEquals(1, installs)
        assertEquals(2, downloads)
        compose.runOnIdle { status = status!!.copy(status = DownloadManager.STATUS_FAILED) }
        compose.onNodeWithText("重新安装").assertDoesNotExist()
        compose.onNodeWithText("重新下载 APK").assertExists()
        compose.runOnIdle { comparison = 1; status = null }
        compose.onNodeWithText("下载 APK").assertExists()
        compose.runOnIdle { comparison = -1 }
        compose.onNodeWithText("下载 APK").assertDoesNotExist()
    }

    @Test fun releaseNotesRenderFormattingAndResolveWebLinks() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val base = "https://github.com/Haikure/hole/releases/tag/v1.0.6"
        val renderer = releaseNotesRenderer(context, base, Color.BLUE, Color.LTGRAY)
        val markdown = "# 更新日志\n\n- **改进连接**和 `NAT`\n- ~~旧行为~~\n\n> 提示\n\n```text\nhole --help\n```\n\n[完整变更](https://github.com/Haikure/hole/compare/v1...v2)"
        val text = renderer.toMarkdown(markdown)
        assertTrue(text.toString().contains("更新日志"))
        assertFalse(text.toString().contains("**"))
        assertFalse(text.toString().contains("```"))
        assertTrue(text.getSpans(0, text.length, HeadingSpan::class.java).isNotEmpty())
        val boldStart = text.toString().indexOf("改进连接")
        val paint = TextPaint()
        text.getSpans(boldStart, boldStart + 4, MetricAffectingSpan::class.java).forEach { it.updateMeasureState(paint) }
        assertTrue(paint.isFakeBoldText || paint.typeface?.isBold == true)
        assertTrue(text.getSpans(0, text.length, StrikethroughSpan::class.java).isNotEmpty())
        assertTrue(text.getSpans(0, text.length, LinkSpan::class.java).isNotEmpty())
        val view = TextView(context).apply { setTextIsSelectable(true) }
        renderer.setParsedMarkdown(view, text)
        assertTrue(view.isTextSelectable)
        assertNotNull(view.movementMethod)
        assertEquals("https://github.com/Haikure/hole/releases/tag/v1.0.6#notes", releaseNoteLink(base, "#notes"))
        assertNull(releaseNoteLink(base, "javascript:alert(1)"))
        assertNull(releaseNoteLink(base, "file:///private.txt"))
    }
}
