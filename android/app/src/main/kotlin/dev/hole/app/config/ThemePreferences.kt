package dev.hole.app.config

/** 持久化使用稳定字符串；未知值只回退外观，不影响连接配置的读取。 */
enum class ThemeStyle(val value: String, val label: String, val description: String) {
    MATERIAL("material", "Material 3", "经典 Material 设计"),
    MIUIX("miuix", "Miuix", "简洁圆角与灵动交互"),
    ;

    companion object {
        fun fromValue(value: String): ThemeStyle =
            entries.firstOrNull { it.value.equals(value, ignoreCase = true) } ?: MATERIAL
    }
}

enum class ThemeMode(val value: String, val label: String) {
    SYSTEM("system", "跟随系统"),
    LIGHT("light", "浅色"),
    DARK("dark", "深色"),
    ;

    companion object {
        fun fromValue(value: String): ThemeMode =
            entries.firstOrNull { it.value.equals(value, ignoreCase = true) } ?: SYSTEM
    }
}

fun StoredConfig.usesDynamicColor(style: ThemeStyle = ThemeStyle.fromValue(themeStyle)): Boolean =
    if (style == ThemeStyle.MIUIX) miuixDynamicColor else dynamicColor

fun StoredConfig.withDynamicColor(style: ThemeStyle, enabled: Boolean): StoredConfig =
    if (style == ThemeStyle.MIUIX) copy(miuixDynamicColor = enabled) else copy(dynamicColor = enabled)
