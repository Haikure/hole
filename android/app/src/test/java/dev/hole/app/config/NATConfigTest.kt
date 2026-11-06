package dev.hole.app.config

import kotlinx.serialization.encodeToString
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Test

class NATConfigTest {
    @Test fun settingsRetainDefaultsAndExplicitEmptyServices() {
        val json = Json { encodeDefaults = true }
        assertEquals("auto", json.decodeFromString<IceSettings>("{}").portMapping)
        for (urls in listOf(null, emptyList(), listOf("stun:127.0.0.1:3478"))) {
            val config = IceSettings(portMapping = "off", natStunUrls = urls)
            val decoded = json.decodeFromString<IceSettings>(json.encodeToString(config))
            assertEquals(config, decoded)
        }
    }
}
