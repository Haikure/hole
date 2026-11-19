package dev.hole.app

import dev.hole.app.config.StoredConfig

/** Shared presentation state; credentials are never included in core snapshots. */
data class ConfigUiState(
    val loaded: Boolean = false,
    val config: StoredConfig = StoredConfig(),
    val password: String = "",
    val token: String = "",
    val lastError: String? = null,
    val turnCredential: String = "",
)
