package dev.hole.app.ui

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds

/** One directional transition for toolbar, system Back and successful saves.
 * Compose honors the system animation-duration scale (including animations off).
 */
@Composable
fun PageMotion(route: String, back: Boolean, content: @Composable (String) -> Unit) {
    AnimatedContent(
        targetState = route,
        modifier = Modifier.fillMaxSize().clipToBounds(),
        transitionSpec = {
            val enter = slideInHorizontally(tween(320, easing = FastOutSlowInEasing)) { if (back) -it / 4 else it } + fadeIn(tween(240))
            val exit = slideOutHorizontally(tween(320, easing = FastOutSlowInEasing)) { if (back) it else -it / 4 } + fadeOut(tween(200))
            (enter togetherWith exit).using(SizeTransform(clip = false)).apply { targetContentZIndex = if (back) -1f else 1f }
        },
        label = "page navigation",
    ) { page -> content(page) }
}
