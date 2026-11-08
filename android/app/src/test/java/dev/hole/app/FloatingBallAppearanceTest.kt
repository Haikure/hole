package dev.hole.app

import android.graphics.Bitmap
import android.graphics.HardwareRenderer
import android.graphics.RenderNode
import android.graphics.drawable.GradientDrawable
import android.os.Looper
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.widget.ImageButton
import android.widget.TextView
import androidx.compose.ui.graphics.toArgb
import dev.hole.app.config.ConfigRepository
import dev.hole.app.config.ThemePalette
import dev.hole.app.ui.materialPalette
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.PeerSnapshot
import java.io.File
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import org.robolectric.annotation.LooperMode
import org.robolectric.shadow.api.Shadow
import org.robolectric.shadows.ShadowSettings
import org.robolectric.shadows.ShadowWindowManagerImpl
import org.robolectric.util.ReflectionHelpers
import org.robolectric.util.ReflectionHelpers.ClassParameter

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w393dp-h851dp-night-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
class FloatingBallAppearanceTest {
    @Test fun expandedPanelFollowsSavedAppearanceAndLiveChanges() = runBlocking {
        val context = RuntimeEnvironment.getApplication()
        val repository = ConfigRepository(context)
        val saved = repository.load()
        repository.update { it.copy(themeMode = "dark", themeStyle = "material", dynamicColor = false, materialPalette = "green") }
        FloatingBallPreferences(context).setEnabled(true)
        ShadowSettings.setCanDrawOverlays(true)
        FloatingBallStateStore.publish(CoreSnapshot(runRequested = true, engineState = "running", signalState = "joined",
            peers = listOf(PeerSnapshot(peerId = "phone", state = "active", pathType = "direct", addressFamily = "IPv6", rttMs = "12"))))
        val controller = Robolectric.buildService(FloatingBallService::class.java).create()
        try {
            val service = controller.get()
            service.onStartCommand(null, 0, 1)
            val manager = Shadow.extract<ShadowWindowManagerImpl>(service.getSystemService(WindowManager::class.java))
            val root = manager.views.single() as ViewGroup
            root.performClick()
            for ((mode, palette) in listOf("dark" to ThemePalette.GREEN, "light" to ThemePalette.ROSE)) {
                // Let the paused main looper finish the service's initial load
                // before starting a second repository transaction.
                if (mode == "light") repository.update { it.copy(themeMode = mode, materialPalette = palette.value) }
                val scheme = materialPalette(palette, mode == "dark")
                val deadline = System.nanoTime() + 5_000_000_000L
                fun buttonColor(): Int? = (root.findViewWithTag<ImageButton>("floating-run-action").background as GradientDrawable).color?.defaultColor
                while (buttonColor() != scheme.primary.toArgb() && System.nanoTime() < deadline) {
                    shadowOf(Looper.getMainLooper()).idle()
                    Thread.sleep(5)
                }
                assertEquals(scheme.primary.toArgb(), buttonColor())
                assertEquals(scheme.onSurface.toArgb(), findTitle(root)?.currentTextColor)
                assertTrue(FloatingBallStateStore.panelExpanded.value)
                capture(root, scheme.background.toArgb(), "floating-$mode.png")
                val panel = (0 until root.childCount).map(root::getChildAt).filterIsInstance<FloatingBallSummaryPanel>().single()
                assertTrue(panel.left > 0 && panel.top > 0)
                assertTrue(panel.right < root.width && panel.bottom < root.height)
                assertTrue(root.width <= context.resources.displayMetrics.widthPixels)
                assertTrue(root.height <= context.resources.displayMetrics.heightPixels)
            }
        } finally {
            controller.destroy()
            repository.update { saved }
            FloatingBallPreferences(context).setEnabled(false)
        }
    }

    private fun findTitle(view: View): TextView? {
        if (view is TextView && view.text.toString() == "hole") return view
        if (view is ViewGroup) for (i in 0 until view.childCount) findTitle(view.getChildAt(i))?.let { return it }
        return null
    }

    private fun capture(root: View, backdrop: Int, name: String) {
        val params = root.layoutParams as WindowManager.LayoutParams
        root.measure(View.MeasureSpec.makeMeasureSpec(params.width, View.MeasureSpec.EXACTLY),
            View.MeasureSpec.makeMeasureSpec(params.height, View.MeasureSpec.EXACTLY))
        root.layout(0, 0, params.width, params.height)
        val node = RenderNode("floating-preview").apply { setPosition(0, 0, root.width, root.height) }
        node.beginRecording(root.width, root.height).also { it.drawColor(backdrop); root.draw(it) }
        node.endRecording()
        val hardware = ReflectionHelpers.callStaticMethod<Bitmap>(HardwareRenderer::class.java, "createHardwareBitmap",
            ClassParameter.from(RenderNode::class.java, node), ClassParameter.from(Int::class.javaPrimitiveType, root.width), ClassParameter.from(Int::class.javaPrimitiveType, root.height))
        val bitmap = requireNotNull(hardware.copy(Bitmap.Config.ARGB_8888, false))
        val directory = File("build/reports/nat-preview").apply { mkdirs() }
        File(directory, name).outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        bitmap.recycle(); hardware.recycle(); node.discardDisplayList()
    }
}
