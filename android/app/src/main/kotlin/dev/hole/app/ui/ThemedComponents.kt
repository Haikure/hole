package dev.hole.app.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.*
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Settings

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HoleScaffold(
    title: String, modifier: Modifier = Modifier, largeTitle: Boolean = false,
    navigationIcon: @Composable () -> Unit = {}, actions: @Composable RowScope.() -> Unit = {},
    bottomBar: @Composable () -> Unit = {}, snackbarHost: @Composable () -> Unit = {},
    content: @Composable (PaddingValues) -> Unit,
) {
    val scrollBehavior = if (largeTitle) TopAppBarDefaults.exitUntilCollapsedScrollBehavior() else null
    Scaffold(
        modifier = modifier.imePadding().then(if (scrollBehavior != null) Modifier.nestedScroll(scrollBehavior.nestedScrollConnection) else Modifier),
        topBar = {
            if (largeTitle && scrollBehavior != null) LargeTopAppBar({ Text(title) }, navigationIcon, actions, scrollBehavior = scrollBehavior)
            else TopAppBar({ Text(title) }, navigationIcon, actions)
        },
        bottomBar = bottomBar, snackbarHost = snackbarHost, content = content,
    )
}

@Composable fun HoleBackButton(onClick: () -> Unit) = HoleIconButton(onClick) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "返回") }
@Composable fun HoleSettingsButton(onClick: () -> Unit) = HoleIconButton(onClick) { Icon(Icons.Filled.Settings, "设置") }
@Composable fun HoleAddButton(label: String, onClick: () -> Unit) = HoleIconButton(onClick, tonal = true) { Icon(Icons.Filled.Add, label) }
@Composable fun HoleSectionTitle(text: String) = Text(text, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(top = 8.dp).semantics { heading() })

@Composable
fun HoleSettingsGroup(title: String, content: @Composable ColumnScope.() -> Unit) {
    Column { HoleSectionTitle(title); Column(Modifier.padding(top = 4.dp), verticalArrangement = Arrangement.spacedBy(12.dp), content = content) }
}

@Composable
fun HoleSwitchPreference(title: String, summary: String, label: String = title, checked: Boolean, enabled: Boolean = true,
    insideMargin: PaddingValues = PaddingValues(16.dp), onCheckedChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().padding(insideMargin), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).padding(end = 12.dp)) { Text(title); Text(summary, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        HoleSwitch(checked, onCheckedChange, label, enabled = enabled)
    }
}

@Composable
fun HoleCard(modifier: Modifier = Modifier, outlined: Boolean = false,
    containerColor: Color = MaterialTheme.colorScheme.surfaceContainerLow, content: @Composable ColumnScope.() -> Unit) {
    if (outlined) OutlinedCard(modifier = modifier, content = content)
    else Card(modifier = modifier, colors = CardDefaults.cardColors(containerColor = containerColor), content = content)
}

@Composable
fun HoleButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true, secondary: Boolean = false) {
    if (secondary) OutlinedButton(onClick, modifier, enabled) { Text(text) } else Button(onClick, modifier, enabled) { Text(text) }
}
@Composable fun HoleTextButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) = TextButton(onClick, modifier) { Text(text) }
@Composable fun HoleIconButton(onClick: () -> Unit, modifier: Modifier = Modifier, tonal: Boolean = false, content: @Composable () -> Unit) {
    if (tonal) FilledTonalIconButton(onClick, modifier, content = content) else IconButton(onClick, modifier, content = content)
}
@Composable fun HoleSwitch(checked: Boolean, onCheckedChange: (Boolean) -> Unit, label: String, modifier: Modifier = Modifier, enabled: Boolean = true) {
    Switch(checked, onCheckedChange, modifier.semantics { contentDescription = label; toggleableState = ToggleableState(checked) }, enabled)
}

@Composable
fun HoleTextField(value: String, onValueChange: (String) -> Unit, label: String, modifier: Modifier = Modifier,
    supportingText: @Composable (() -> Unit)? = null, isError: Boolean = false, singleLine: Boolean = false,
    visualTransformation: VisualTransformation = VisualTransformation.None, keyboardOptions: KeyboardOptions = KeyboardOptions.Default,
    trailingIcon: @Composable (() -> Unit)? = null) {
    OutlinedTextField(value, onValueChange, modifier, label = { Text(label) }, supportingText = supportingText,
        isError = isError, singleLine = singleLine, visualTransformation = visualTransformation,
        keyboardOptions = keyboardOptions, trailingIcon = trailingIcon, shape = MaterialTheme.shapes.medium)
}

@Composable
fun HoleSingleChoice(options: List<Pair<String, String>>, selectedValue: String, onSelect: (String) -> Unit, modifier: Modifier = Modifier) {
    SingleChoiceSegmentedButtonRow(modifier.fillMaxWidth()) {
        options.forEachIndexed { index, (value, label) ->
            SegmentedButton(selectedValue == value, { onSelect(value) }, SegmentedButtonDefaults.itemShape(index, options.size)) { Text(label) }
        }
    }
}

@Composable fun HoleSnackbarHost(state: SnackbarHostState) { SnackbarHost(state) }
