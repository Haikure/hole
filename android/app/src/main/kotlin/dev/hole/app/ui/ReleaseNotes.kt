package dev.hole.app.ui

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.util.TypedValue
import android.widget.TextView
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import io.noties.markwon.AbstractMarkwonPlugin
import io.noties.markwon.Markwon
import io.noties.markwon.MarkwonConfiguration
import io.noties.markwon.core.MarkwonTheme
import io.noties.markwon.ext.strikethrough.StrikethroughPlugin
import io.noties.markwon.ext.tables.TablePlugin
import java.net.URI

internal fun releaseNoteLink(baseUrl: String, destination: String): String? = runCatching {
    URI(baseUrl).resolve(destination).takeIf {
        it.scheme in setOf("https", "http") && !it.host.isNullOrBlank()
    }?.toString()
}.getOrNull()

internal fun releaseNotesRenderer(context: Context, baseUrl: String, linkColor: Int, codeBackground: Int): Markwon =
    Markwon.builder(context)
        .usePlugin(TablePlugin.create(context))
        .usePlugin(StrikethroughPlugin.create())
        .usePlugin(object : AbstractMarkwonPlugin() {
            override fun configureTheme(builder: MarkwonTheme.Builder) {
                builder.linkColor(linkColor).codeBackgroundColor(codeBackground)
                    .blockQuoteColor(linkColor).headingBreakHeight(0)
            }

            override fun configureConfiguration(builder: MarkwonConfiguration.Builder) {
                builder.linkResolver { view, destination ->
                    releaseNoteLink(baseUrl, destination)?.let { url ->
                        runCatching { view.context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) }
                    }
                }
            }
        })
        .build()

@Composable
internal fun ReleaseNotes(markdown: String, pageUrl: String) {
    val context = LocalContext.current
    val colors = MaterialTheme.colorScheme
    val renderer = remember(context, pageUrl, colors.primary, colors.surfaceContainerHighest) {
        releaseNotesRenderer(context, pageUrl, colors.primary.toArgb(), colors.surfaceContainerHighest.toArgb())
    }
    val rendered = remember(renderer, markdown) { renderer.toMarkdown(markdown) }
    val textSize = MaterialTheme.typography.bodyMedium.fontSize.value
    AndroidView(
        modifier = Modifier.fillMaxWidth(),
        factory = { TextView(it).apply {
            setTextIsSelectable(true)
            setPadding(0, 0, 0, 0)
            setLineSpacing(0f, 1.15f)
        } },
        update = { view ->
            view.setTextSize(TypedValue.COMPLEX_UNIT_SP, textSize)
            view.setTextColor(colors.onSurface.toArgb())
            view.setLinkTextColor(colors.primary.toArgb())
            renderer.setParsedMarkdown(view, rendered)
        },
    )
}
