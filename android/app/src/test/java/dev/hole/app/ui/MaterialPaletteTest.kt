package dev.hole.app.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import dev.hole.app.config.ThemePalette
import kotlin.test.Test
import kotlin.test.assertTrue

class MaterialPaletteTest {
    @Test
    fun presetTextMaintainsReadableContrastInBothModes() {
        for (palette in ThemePalette.entries) {
            for (dark in listOf(false, true)) {
                val colors = materialPalette(palette, dark)
                val pairs = listOf(
                    colors.primary to colors.onPrimary,
                    colors.primaryContainer to colors.onPrimaryContainer,
                    colors.secondaryContainer to colors.onSecondaryContainer,
                    colors.tertiaryContainer to colors.onTertiaryContainer,
                    colors.surface to colors.onSurface,
                    colors.surfaceContainerLow to colors.onSurfaceVariant,
                )
                pairs.forEach { (background, foreground) ->
                    assertTrue(contrast(background, foreground) >= 4.5f, "$palette dark=$dark: text contrast")
                }
            }
        }
    }

    private fun contrast(a: Color, b: Color): Float {
        val x = a.luminance()
        val y = b.luminance()
        return (maxOf(x, y) + 0.05f) / (minOf(x, y) + 0.05f)
    }
}
