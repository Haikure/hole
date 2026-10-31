package dev.hole.app

import dev.hole.corebridge.CoreSnapshot
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Process-local handoff between the forwarding service and the overlay service.
 *
 * The snapshot contains only the public runtime summary. Credentials and
 * configuration secrets never enter this store.
 */
object FloatingBallStateStore {
    private val mutableSnapshot = MutableStateFlow(CoreSnapshot())
    private val mutablePanelExpanded = MutableStateFlow(false)

    val snapshot: StateFlow<CoreSnapshot> = mutableSnapshot.asStateFlow()
    val panelExpanded: StateFlow<Boolean> = mutablePanelExpanded.asStateFlow()

    fun publish(snapshot: CoreSnapshot) {
        mutableSnapshot.value = snapshot
    }

    fun setPanelExpanded(expanded: Boolean) {
        mutablePanelExpanded.value = expanded
    }
}
