package dev.hole.app

object BuildConfig {
    val VERSION_NAME: String = System.getProperty("hole.desktop.version") ?: runCatching {
        java.io.File(System.getProperty("compose.application.resources.dir"), "desktop-version.txt").readText().trim()
    }.getOrDefault("development")
}
