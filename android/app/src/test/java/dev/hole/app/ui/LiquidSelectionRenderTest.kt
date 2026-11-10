package dev.hole.app.ui

import android.graphics.Bitmap
import android.graphics.HardwareRenderer
import android.graphics.RenderNode
import android.os.Build
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.dp
import dev.hole.app.config.ThemeMode
import java.io.File
import kotlin.math.abs
import kotlin.math.roundToInt
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import org.robolectric.annotation.LooperMode
import org.robolectric.util.ReflectionHelpers
import org.robolectric.util.ReflectionHelpers.ClassParameter

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [31, 35, 36], qualifiers = "w393dp-h851dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
class LiquidSelectionRenderTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    @Test fun glassHasNoInteriorRectangleAfterSelectionAndDragging() {
        var mode by mutableStateOf(ThemeMode.LIGHT)
        var selected by mutableStateOf("tcp")
        compose.setContent {
            HoleTheme(mode, dynamic = false) {
                Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background).padding(16.dp)) {
                    HoleSingleChoice(listOf("tcp" to "TCP", "udp" to "UDP"), selected, { selected = it }, Modifier.width(320.dp))
                }
            }
        }
        for (appearance in listOf(ThemeMode.LIGHT, ThemeMode.DARK)) {
            compose.runOnIdle { mode = appearance; selected = "tcp" }
            checkLens("${appearance.value}-tcp")
            compose.onNodeWithText("UDP").performClick().assertIsSelected()
            checkLens("${appearance.value}-udp")
            compose.onNodeWithTag("segmented-control").performTouchInput {
                down(Offset(width * .75f, centerY))
                moveTo(Offset(width * .25f, centerY), 300)
                up()
            }
            compose.onNodeWithText("TCP").assertIsSelected()
            checkLens("${appearance.value}-dragged")
        }
    }

    private fun checkLens(name: String) {
        compose.mainClock.advanceTimeBy(600)
        compose.waitForIdle()
        val bounds = compose.onNodeWithTag("selection-lens").fetchSemanticsNode().boundsInWindow
        compose.runOnIdle { compose.activity.window.decorView.invalidate() }
        compose.mainClock.advanceTimeByFrame()
        compose.waitForIdle()
        compose.runOnIdle {
            val view = compose.activity.window.decorView
            val node = RenderNode("selection-preview").apply { setPosition(0, 0, view.width, view.height) }
            view.draw(node.beginRecording(view.width, view.height))
            node.endRecording()
            val hardware = ReflectionHelpers.callStaticMethod<Bitmap>(
                HardwareRenderer::class.java, "createHardwareBitmap",
                ClassParameter.from(RenderNode::class.java, node),
                ClassParameter.from(Int::class.javaPrimitiveType, view.width),
                ClassParameter.from(Int::class.javaPrimitiveType, view.height),
            )
            val bitmap = requireNotNull(hardware.copy(Bitmap.Config.ARGB_8888, false))
            try {
                val directory = File("build/outputs/selection-previews").apply { mkdirs() }
                File(directory, "api-${Build.VERSION.SDK_INT}-$name.png").outputStream().use {
                    bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)
                }
                // Stay inside the rounded corners, and above the text. The glass should be
                // uniform across each row and change smoothly downwards, without a white band.
                val left = (bounds.left + bounds.width * .12f).roundToInt()
                val right = (bounds.right - bounds.width * .12f).roundToInt()
                val top = (bounds.top + bounds.height * .20f).roundToInt()
                val bottom = (bounds.top + bounds.height * .30f).roundToInt()
                val colors = (top..bottom).map { y ->
                    val first = bitmap.getPixel(left, y)
                    for (x in left..right) assertSimilar(first, bitmap.getPixel(x, y), "$name 横向出现色块 ($x, $y)")
                    first
                }
                colors.zipWithNext().forEach { (a, b) -> assertSimilar(a, b, "$name 渐变出现突变") }
                assertTrue(colors.distinct().size > 1, "$name 应绘制渐变滑块")
            } finally {
                bitmap.recycle()
                hardware.recycle()
                node.discardDisplayList()
            }
        }
    }

    private fun assertSimilar(a: Int, b: Int, message: String) {
        for (shift in listOf(16, 8, 0)) {
            assertTrue(abs(((a shr shift) and 255) - ((b shr shift) and 255)) <= 3, message)
        }
    }
}
