package dev.hole.app.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.Density
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onLast
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextReplacement
import dev.hole.app.ConfigUiState
import dev.hole.app.config.ConnectionSettings
import dev.hole.app.config.StoredConfig
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemeStyle
import dev.hole.app.config.usesDynamicColor
import dev.hole.app.config.withDynamicColor
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import org.robolectric.annotation.LooperMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
@LooperMode(LooperMode.Mode.PAUSED)
class ThemeSwitchTest {
    @get:Rule val compose = createComposeRule()

    private var config by mutableStateOf(
        StoredConfig(connection = ConnectionSettings(serverUrl = "wss://example.test/ws", deviceName = "saved-device")),
    )

    private fun showSettings() {
        compose.setContent {
            HoleTheme(ThemeMode.fromValue(config.themeMode), config.usesDynamicColor(), ThemeStyle.fromValue(config.themeStyle)) {
                SettingsScreen(
                    configState = ConfigUiState(loaded = true, config = config),
                    onSave = { _, _, _, _, _, _, _, _ -> null },
                    onThemeStyleChange = { config = config.copy(themeStyle = it.value) },
                    onThemeModeChange = { config = config.copy(themeMode = it.value) },
                    onDynamicColorChange = { config = config.withDynamicColor(ThemeStyle.fromValue(config.themeStyle), it) },
                    onBack = {},
                )
            }
        }
    }

    @Test
    fun styleChangesKeepUnsavedFormAndAllowSwitchingBack() {
        showSettings()
        val deviceField = hasSetTextAction() and hasText("设备名")
        compose.onNode(deviceField).performScrollTo().performTextReplacement("unsaved-device")
        compose.onNodeWithText("Miuix").performScrollTo().performClick().assertIsSelected()
        compose.onNode(deviceField).performScrollTo().assertTextContains("unsaved-device")
        compose.onNodeWithText("Material 3").performScrollTo().performClick().assertIsSelected()
        compose.onNode(deviceField).performScrollTo().assertTextContains("unsaved-device")
        assertEquals("material", config.themeStyle)
        assertEquals("saved-device", config.connection.deviceName)
    }

    @Test
    fun miuixSupportsAllModesAndFallsBackOnAndroidEight() {
        showSettings()
        compose.onNodeWithText("Miuix").performScrollTo().performClick()
        for (mode in listOf(ThemeMode.DARK, ThemeMode.LIGHT, ThemeMode.SYSTEM)) {
            compose.onNodeWithText("显示模式").performScrollTo().performClick()
            compose.onAllNodesWithText(mode.label).onLast().performClick()
            compose.runOnIdle { assertEquals(mode.value, config.themeMode) }
            compose.onNodeWithText("显示模式").assertTextContains(mode.label)
        }
        compose.onNodeWithContentDescription("动态配色").performScrollTo().assertIsNotEnabled()
    }

    @Test
    @Config(sdk = [31])
    fun bothThemesSupportDynamicColorOnAndroidTwelve() {
        showSettings()
        compose.onNodeWithContentDescription("动态配色").performScrollTo().assertIsEnabled().performClick()
        compose.runOnIdle { assertEquals(false, config.dynamicColor) }
        compose.onNodeWithText("Miuix").performScrollTo().performClick()
        compose.onNodeWithContentDescription("动态配色").performScrollTo().assertIsEnabled().performClick()
        compose.runOnIdle {
            assertEquals(true, config.miuixDynamicColor)
            assertEquals(false, config.dynamicColor)
        }
    }

    @Test
    @Config(sdk = [26, 35])
    @GraphicsMode(GraphicsMode.Mode.NATIVE)
    fun largeFontDoesNotEllipsizeMiuixModeLabels() {
        // 使用真实字体度量，而非 LEGACY graphics 中按字符数近似的字宽。
        var selected by mutableStateOf(ThemeMode.SYSTEM.value)
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = 2f)) {
                HoleTheme(ThemeMode.LIGHT, dynamic = false, style = ThemeStyle.MIUIX) {
                    HoleSingleChoice(
                        options = ThemeMode.entries.map { it.value to it.label },
                        selectedValue = selected,
                        onSelect = { selected = it },
                    )
                }
            }
        }
        for (mode in ThemeMode.entries) {
            val layouts = mutableListOf<TextLayoutResult>()
            compose.onNodeWithText(mode.label).performScrollTo().performClick().assertIsSelected()
                .performSemanticsAction(SemanticsActions.GetTextLayoutResult) { assertTrue(it(layouts)) }
            val layout = layouts.single()
            // BasicText 的语义结果按最大约束重建 paragraph，短标签的 paragraph.width
            // 可大于实际 Text 宽度；检查真实行边界，避免把空余段落宽度误报为溢出。
            assertEquals(1, layout.lineCount)
            assertFalse(layout.didOverflowHeight, "${mode.label} 应完整显示行高")
            assertFalse(layout.isLineEllipsized(0), "${mode.label} 应完整显示")
            assertEquals(mode.label.length, layout.getLineEnd(0, visibleEnd = true))
            assertTrue(
                layout.getLineRight(0) - layout.getLineLeft(0) <= layout.size.width + 1f,
                "${mode.label} 的文字宽度应在布局边界内",
            )
        }
    }
}
