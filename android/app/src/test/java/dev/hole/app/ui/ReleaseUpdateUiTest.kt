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
import io.noties.markwon.core.spans.HeadingSpan
import io.noties.markwon.core.spans.LinkSpan
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ReleaseUpdateUiTest {
    @get:Rule val compose = createComposeRule()

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
