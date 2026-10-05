package dev.hole.app.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.runtime.remember
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.*
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Info

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HoleScaffold(
    title: String, modifier: Modifier = Modifier, largeTitle: Boolean = false,
    navigationIcon: @Composable () -> Unit = {}, actions: @Composable RowScope.() -> Unit = {},
    floatingActions: @Composable RowScope.() -> Unit = {}, snackbarHost: @Composable () -> Unit = {},
    content: @Composable (PaddingValues) -> Unit,
) {
    val scrollBehavior = TopAppBarDefaults.pinnedScrollBehavior()
    Scaffold(
        modifier = modifier.imePadding().nestedScroll(scrollBehavior.nestedScrollConnection),
        topBar = {
            TopAppBar(
                title = { Text(title, style = MaterialTheme.typography.titleLarge) },
                navigationIcon = navigationIcon, actions = actions, scrollBehavior = scrollBehavior,
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
        contentWindowInsets = if (LocalFloatingInset.current > 0.dp) WindowInsets.systemBars.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal) else ScaffoldDefaults.contentWindowInsets,
        containerColor = MaterialTheme.colorScheme.background,
        floatingActionButton = { Row(horizontalArrangement = Arrangement.spacedBy(12.dp), content = floatingActions) },
        snackbarHost = snackbarHost, content = content,
    )
}

@Composable fun HoleBackButton(onClick: () -> Unit) = HoleIconButton(onClick) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "返回") }
@Composable fun HoleSettingsButton(onClick: () -> Unit) = HoleIconButton(onClick) { Icon(Icons.Filled.Settings, "设置") }
@Composable fun HoleAddButton(label: String, onClick: () -> Unit) = HoleIconButton(onClick, tonal = true) { Icon(Icons.Filled.Add, label) }
@Composable fun HoleSectionTitle(text: String) = Text(
    text,
    style = MaterialTheme.typography.titleMedium,
    color = MaterialTheme.colorScheme.primary,
    modifier = Modifier.padding(start = 8.dp, top = 8.dp, bottom = 4.dp).semantics { heading() },
)

@Composable
fun HoleSettingsGroup(title: String, content: @Composable ColumnScope.() -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        HoleSectionTitle(title)
        RaisedPanel(Modifier.fillMaxWidth()) {
            Column(
                Modifier.padding(12.dp),
                verticalArrangement = Arrangement.spacedBy(14.dp),
                content = content,
            )
        }
    }
}

@Composable
fun HoleSwitchPreference(title: String, summary: String, label: String = title, checked: Boolean, enabled: Boolean = true,
    insideMargin: PaddingValues = PaddingValues(16.dp), onCheckedChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().heightIn(min = 56.dp).padding(insideMargin), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).padding(end = 12.dp), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            if (summary.length <= 18) Text(summary, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (summary.length > 18) HoleInfoButton(title) { Text(summary) }
        HoleSwitch(checked, onCheckedChange, label, enabled = enabled)
    }
}

@Composable
fun HoleCard(modifier: Modifier = Modifier, outlined: Boolean = false,
    containerColor: Color = MaterialTheme.colorScheme.surfaceContainerLow, content: @Composable ColumnScope.() -> Unit) {
    RaisedPanel(modifier, containerColor, content)
}

@Composable
fun HoleButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true, secondary: Boolean = false) {
    val interaction = remember { MutableInteractionSource() }
    val scale = pressScale(interaction)
    val animated = modifier.graphicsLayer { scaleX = scale; scaleY = scale }
    if (secondary) OutlinedButton(onClick, animated, enabled, interactionSource = interaction) { Text(text) }
    else Button(onClick, animated, enabled, interactionSource = interaction) { Text(text) }
}
@Composable fun HoleTextButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) = TextButton(onClick, modifier) { Text(text) }
@Composable fun HoleIconButton(onClick: () -> Unit, modifier: Modifier = Modifier, tonal: Boolean = false, content: @Composable () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    val scale = pressScale(interaction)
    val animated = modifier.graphicsLayer { scaleX = scale; scaleY = scale }
    if (tonal) FilledTonalIconButton(onClick, animated, interactionSource = interaction, content = content)
    else IconButton(onClick, animated, interactionSource = interaction, content = content)
}
@Composable fun HoleSwitch(checked: Boolean, onCheckedChange: (Boolean) -> Unit, label: String, modifier: Modifier = Modifier, enabled: Boolean = true) {
    Switch(
        checked = checked,
        onCheckedChange = onCheckedChange,
        modifier = modifier.semantics { contentDescription = label; toggleableState = ToggleableState(checked) },
        enabled = enabled,
    )
}

@Composable
fun HoleTextField(value: String, onValueChange: (String) -> Unit, label: String, modifier: Modifier = Modifier,
    supportingText: @Composable (() -> Unit)? = null, isError: Boolean = false, singleLine: Boolean = false,
    visualTransformation: VisualTransformation = VisualTransformation.None, keyboardOptions: KeyboardOptions = KeyboardOptions.Default,
    trailingIcon: @Composable (() -> Unit)? = null) {
    OutlinedTextField(value, onValueChange, modifier, label = { Text(label) }, supportingText = if (isError) supportingText else null,
        isError = isError, singleLine = singleLine, visualTransformation = visualTransformation,
        keyboardOptions = keyboardOptions, trailingIcon = if (trailingIcon != null || (supportingText != null && !isError)) ({
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (supportingText != null && !isError) HoleInfoButton(label, supportingText)
                trailingIcon?.invoke()
            }
        }) else null, shape = MaterialTheme.shapes.medium)
}

@Composable
fun HoleInfoButton(title: String, content: @Composable () -> Unit) {
    var open by rememberSaveable { mutableStateOf(false) }
    IconButton(onClick = { open = true }) {
        Icon(Icons.Default.Info, contentDescription = "$title 说明", modifier = Modifier.size(20.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
    if (open) AlertDialog(
        onDismissRequest = { open = false },
        title = { Text(title) },
        text = { Column(Modifier.verticalScroll(rememberScrollState())) { content() } },
        confirmButton = { TextButton(onClick = { open = false }) { Text("知道了") } },
    )
}

@Composable
fun HoleHelp(title: String, text: String) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(title, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        HoleInfoButton(title) { Text(text) }
    }
}

@Composable
fun HoleSingleChoice(options: List<Pair<String, String>>, selectedValue: String, onSelect: (String) -> Unit, modifier: Modifier = Modifier) {
    val track = MaterialTheme.colorScheme.surfaceContainerHigh
    Box(modifier.fillMaxWidth().clip(androidx.compose.foundation.shape.RoundedCornerShape(10.dp)).background(track).padding(horizontal = 3.dp, vertical = 2.dp)) {
        LiquidSelection(options.map { it.second }, options.indexOfFirst { it.first == selectedValue }.coerceAtLeast(0), { onSelect(options[it].first) })
    }
}

@Composable
fun FloatingIconAction(icon: androidx.compose.ui.graphics.vector.ImageVector, label: String, onClick: () -> Unit, enabled: Boolean = true) {
    val interaction = remember { MutableInteractionSource() }
    val scale = pressScale(interaction, 1.25f)
    FloatingSurface(Modifier.graphicsLayer { scaleX = scale; scaleY = scale }) {
        Box(Modifier.size(56.dp).clickable(interactionSource = interaction, indication = null, enabled = enabled, role = Role.Button, onClick = onClick), contentAlignment = Alignment.Center) {
            Icon(icon, label, Modifier.size(24.dp), tint = MaterialTheme.colorScheme.primary.copy(alpha = if (enabled) 1f else .38f))
        }
    }
}
