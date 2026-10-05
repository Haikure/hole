package dev.hole.app.ui

import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag

internal suspend fun SnackbarHostState.showUndoSnackbar(message: String): SnackbarResult =
    showSnackbar(
        message = message,
        actionLabel = "撤销",
        withDismissAction = true,
        duration = SnackbarDuration.Short,
    )

@Composable
fun HoleSnackbarHost(state: SnackbarHostState, modifier: Modifier = Modifier) {
    SnackbarHost(state, modifier.padding(bottom = LocalFloatingInset.current)) { data ->
        key(data) {
            SwipeToDismissBox(
                state = rememberSwipeToDismissBoxState(),
                modifier = Modifier.testTag("snackbar-swipe"),
                backgroundContent = {},
                onDismiss = { data.dismiss() },
            ) {
                Snackbar(data, shape = MaterialTheme.shapes.large)
            }
        }
    }
}
