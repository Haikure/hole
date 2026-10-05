package dev.hole.app.ui

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier

@Composable
fun pressScale(source: MutableInteractionSource, pressedScale: Float = .95f): Float {
    val pressed by source.collectIsPressedAsState()
    val scale by animateFloatAsState(if (pressed) pressedScale else 1f, spring(dampingRatio = .68f, stiffness = 503.551f), label = "press feedback")
    return scale
}

@Composable
fun AnimatedMetric(value: String, modifier: Modifier = Modifier) {
    AnimatedContent(value, modifier, transitionSpec = {
        (fadeIn(tween(180)) + slideInVertically(tween(220)) { it / 3 }) togetherWith
            (fadeOut(tween(100)) + slideOutVertically(tween(180)) { -it / 3 })
    }, label = "metric update") { text ->
        Text(text, style = MaterialTheme.typography.headlineSmall.copy(fontFeatureSettings = "tnum"))
    }
}
