package dev.hole.app.ui

import android.graphics.Bitmap
import android.graphics.HardwareRenderer
import android.graphics.RenderNode
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.dropShadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.shadow.Shadow
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.unit.DpOffset
import androidx.compose.ui.unit.dp
import dev.hole.app.config.ThemeMode
import kotlin.test.assertContentEquals
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
@Config(sdk = [35], qualifiers = "w393dp-h851dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
class RaisedPanelRenderTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    @Test fun cachedPanelMatchesOriginalPixelsIncludingShadowAndThemeChanges() {
        var cached by mutableStateOf(false)
        var mode by mutableStateOf(ThemeMode.LIGHT)
        compose.setContent {
            HoleTheme(mode, dynamic = false) {
                Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background).padding(32.dp)) {
                    val content: @Composable ColumnScope.() -> Unit = {
                        Text("NAT 检测", Modifier.padding(18.dp))
                    }
                    if (cached) RaisedPanel(Modifier.width(280.dp), content = content) else {
                        // Original renderer, before adding the display-list boundary.
                        val color = MaterialTheme.colorScheme.surfaceContainerLow
                        val dark = mode == ThemeMode.DARK
                        val shape = MaterialTheme.shapes.extraLarge
                        Column(Modifier.width(280.dp)
                            .dropShadow(shape, Shadow(radius = 10.dp,
                                color = Color(0xFF071B3C).copy(alpha = if (dark) .28f else .08f),
                                offset = DpOffset(0.dp, 4.dp)))
                            .clip(shape).background(Brush.linearGradient(listOf(
                                if (dark) lerp(color, Color.White, .045f) else lerp(color, Color.White, .70f), color,
                            ))), content = content)
                    }
                }
            }
        }
        for (appearance in listOf(ThemeMode.LIGHT, ThemeMode.DARK, ThemeMode.LIGHT)) {
            compose.runOnIdle { mode = appearance; cached = false }
            val original = pixels()
            compose.runOnIdle { cached = true }
            assertContentEquals(original, pixels(), "$appearance 的渐变、圆角、阴影应与原版完全一致")
        }
    }

    private fun pixels(): IntArray {
        compose.waitForIdle()
        compose.runOnIdle { compose.activity.window.decorView.invalidate() }
        compose.mainClock.advanceTimeByFrame()
        compose.waitForIdle()
        return compose.runOnIdle {
            val view = compose.activity.window.decorView
            val node = RenderNode("panel-comparison").apply { setPosition(0, 0, view.width, view.height) }
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
                IntArray(bitmap.width * bitmap.height).also {
                    bitmap.getPixels(it, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
                }
            } finally {
                bitmap.recycle()
                hardware.recycle()
                node.discardDisplayList()
            }
        }
    }
}
