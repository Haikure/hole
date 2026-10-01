package dev.hole.app.config

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlinx.serialization.json.Json

class ThemePreferencesTest {
    @Test
    fun legacyConfigKeepsMaterialAndExistingAppearance() {
        val config = Json.decodeFromString<StoredConfig>("""{"themeMode":"dark","dynamicColor":false}""")
        assertEquals(ThemeStyle.MATERIAL, ThemeStyle.fromValue(config.themeStyle))
        assertEquals(ThemeMode.DARK, ThemeMode.fromValue(config.themeMode))
        assertEquals(false, config.dynamicColor)
    }

    @Test
    fun unknownThemeValuesFallBackWithoutDiscardingConfig() {
        val config = Json.decodeFromString<StoredConfig>("""{"themeStyle":"future-theme","themeMode":"future-mode"}""")
        assertEquals(ThemeStyle.MATERIAL, ThemeStyle.fromValue(config.themeStyle))
        assertEquals(ThemeMode.SYSTEM, ThemeMode.fromValue(config.themeMode))
    }

    @Test
    fun themeValuesAreStableAndCaseInsensitive() {
        ThemePalette.entries.forEach { palette ->
            val config = StoredConfig(materialPalette = palette.value, dynamicColor = false)
            val restored = Json.decodeFromString<StoredConfig>(Json.encodeToString(StoredConfig.serializer(), config))
            assertEquals(palette, ThemePalette.fromValue(restored.materialPalette))
            assertEquals(false, restored.dynamicColor)
        }
        assertEquals(ThemePalette.BLUE, ThemePalette.fromValue("future-palette"))
        assertEquals(ThemePalette.BLUE, Json.decodeFromString<StoredConfig>("{}").let { ThemePalette.fromValue(it.materialPalette) })
        ThemeStyle.entries.forEach { assertEquals(it, ThemeStyle.fromValue(it.value)) }
        ThemeMode.entries.forEach { assertEquals(it, ThemeMode.fromValue(it.value)) }
        assertEquals(ThemeStyle.MIUIX, ThemeStyle.fromValue("MIUIX"))
        assertEquals(ThemeMode.LIGHT, ThemeMode.fromValue("LIGHT"))
    }

    @Test
    fun miuixUsesOfficialColorsByDefaultAndKeepsItsOwnDynamicPreference() {
        val legacy = Json.decodeFromString<StoredConfig>("""{"themeStyle":"miuix","dynamicColor":true}""")
        assertEquals(false, legacy.usesDynamicColor())
        assertEquals(true, legacy.usesDynamicColor(ThemeStyle.MATERIAL))
        val changed = legacy.withDynamicColor(ThemeStyle.MIUIX, true).withDynamicColor(ThemeStyle.MATERIAL, false)
        assertEquals(true, changed.usesDynamicColor(ThemeStyle.MIUIX))
        assertEquals(false, changed.usesDynamicColor(ThemeStyle.MATERIAL))
    }
}
