package dev.hole.app.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalIconButton
import androidx.compose.material3.IconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.ProvideTextStyle
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.contentColorFor
import androidx.compose.material3.minimumInteractiveComponentSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.error
import androidx.compose.ui.semantics.disabled
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.toggleableState
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.dp
import dev.hole.app.config.ThemeStyle
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Settings
import top.yukonga.miuix.kmp.icon.MiuixIcons
import top.yukonga.miuix.kmp.icon.extended.Back
import top.yukonga.miuix.kmp.icon.extended.Add
import top.yukonga.miuix.kmp.icon.extended.Settings
import top.yukonga.miuix.kmp.preference.SwitchPreference
import top.yukonga.miuix.kmp.basic.Scaffold as MiuixScaffold
import top.yukonga.miuix.kmp.basic.SmallTitle as MiuixSmallTitle
import top.yukonga.miuix.kmp.basic.ButtonDefaults as MiuixButtonDefaults
import top.yukonga.miuix.kmp.basic.Card as MiuixCard
import top.yukonga.miuix.kmp.basic.CardDefaults as MiuixCardDefaults
import top.yukonga.miuix.kmp.basic.IconButton as MiuixIconButton
import top.yukonga.miuix.kmp.basic.MiuixScrollBehavior
import top.yukonga.miuix.kmp.basic.SmallTopAppBar as MiuixSmallTopAppBar
import top.yukonga.miuix.kmp.basic.Snackbar as MiuixSnackbar
import top.yukonga.miuix.kmp.basic.SnackbarData as MiuixSnackbarData
import top.yukonga.miuix.kmp.basic.SnackbarDuration as MiuixSnackbarDuration
import top.yukonga.miuix.kmp.basic.SnackbarVisuals as MiuixSnackbarVisuals
import top.yukonga.miuix.kmp.basic.Switch as MiuixSwitch
import top.yukonga.miuix.kmp.basic.TabRowWithContour as MiuixTabRow
import top.yukonga.miuix.kmp.basic.TextButton as MiuixTextButton
import top.yukonga.miuix.kmp.basic.TextField as MiuixTextField
import top.yukonga.miuix.kmp.basic.TextFieldDefaults as MiuixTextFieldDefaults
import top.yukonga.miuix.kmp.basic.TopAppBar as MiuixTopAppBar
import top.yukonga.miuix.kmp.theme.MiuixTheme

/** 共用页面结构与状态，只在组件层切换设计系统，避免维护两套表单和业务逻辑。 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HoleScaffold(
    title: String,
    modifier: Modifier = Modifier,
    largeTitle: Boolean = false,
    navigationIcon: @Composable () -> Unit = {},
    actions: @Composable RowScope.() -> Unit = {},
    bottomBar: @Composable () -> Unit = {},
    snackbarHost: @Composable () -> Unit = {},
    content: @Composable (PaddingValues) -> Unit,
) {
    val miuix = LocalThemeStyle.current == ThemeStyle.MIUIX
    val scrollBehavior = MiuixScrollBehavior()
    // 表单、路由与滚动状态由调用方持有；Miuix 使用自己的布局和弹层宿主。
    if (miuix) {
        MiuixScaffold(
            modifier = modifier.imePadding().then(
                if (largeTitle) Modifier.nestedScroll(scrollBehavior.nestedScrollConnection) else Modifier,
            ),
            topBar = {
                if (largeTitle) {
                    MiuixTopAppBar(title = title, navigationIcon = navigationIcon, actions = actions, scrollBehavior = scrollBehavior)
                } else {
                    MiuixSmallTopAppBar(title = title, navigationIcon = navigationIcon, actions = actions)
                }
            },
            bottomBar = bottomBar,
            snackbarHost = snackbarHost,
            content = content,
        )
    } else {
        Scaffold(
            modifier = modifier.imePadding(),
            topBar = { TopAppBar(title = { Text(title) }, navigationIcon = navigationIcon, actions = actions) },
            bottomBar = bottomBar,
            snackbarHost = snackbarHost,
            content = content,
        )
    }
}

@Composable
fun HoleBackButton(onClick: () -> Unit) {
    HoleIconButton(onClick) {
        Icon(
            if (LocalThemeStyle.current == ThemeStyle.MIUIX) MiuixIcons.Regular.Back else Icons.AutoMirrored.Filled.ArrowBack,
            contentDescription = "返回",
        )
    }
}

@Composable
fun HoleSettingsButton(onClick: () -> Unit) {
    HoleIconButton(onClick) {
        Icon(if (LocalThemeStyle.current == ThemeStyle.MIUIX) MiuixIcons.Regular.Settings else Icons.Filled.Settings, "设置")
    }
}

@Composable
fun HoleAddButton(label: String, onClick: () -> Unit) {
    HoleIconButton(onClick, tonal = LocalThemeStyle.current != ThemeStyle.MIUIX) {
        Icon(if (LocalThemeStyle.current == ThemeStyle.MIUIX) MiuixIcons.Regular.Add else Icons.Filled.Add, label)
    }
}

@Composable
fun HoleSectionTitle(text: String) {
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        MiuixSmallTitle(text, modifier = Modifier.semantics { heading() }, insideMargin = PaddingValues(start = 12.dp, top = 8.dp, bottom = 4.dp))
    } else {
        Text(text, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(top = 8.dp).semantics { heading() })
    }
}

@Composable
fun HoleSettingsGroup(title: String, content: @Composable ColumnScope.() -> Unit) {
    Column {
        HoleSectionTitle(title)
        if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
            MiuixCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = androidx.compose.foundation.layout.Arrangement.spacedBy(12.dp), content = content)
            }
        } else {
            Column(verticalArrangement = androidx.compose.foundation.layout.Arrangement.spacedBy(12.dp), content = content)
        }
    }
}

@Composable
fun HoleSwitchPreference(
    title: String,
    summary: String,
    label: String = title,
    checked: Boolean,
    enabled: Boolean = true,
    insideMargin: PaddingValues = PaddingValues(16.dp),
    onCheckedChange: (Boolean) -> Unit,
) {
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        SwitchPreference(
            title = title, summary = summary, checked = checked, enabled = enabled, onCheckedChange = onCheckedChange,
            insideMargin = insideMargin,
            modifier = Modifier.semantics {
                contentDescription = label
                toggleableState = ToggleableState(checked)
                if (!enabled) disabled()
            },
        )
    } else {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f).padding(end = 12.dp)) {
                Text(title)
                Text(summary, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            HoleSwitch(checked, onCheckedChange, label, enabled = enabled)
        }
    }
}

@Composable
fun HoleCard(
    modifier: Modifier = Modifier,
    outlined: Boolean = false,
    containerColor: Color = if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        MiuixTheme.colorScheme.surfaceContainer
    } else {
        MaterialTheme.colorScheme.surfaceContainerHighest
    },
    content: @Composable ColumnScope.() -> Unit,
) {
    val contentColor = contentColorFor(containerColor)
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        MiuixCard(
            modifier = modifier,
            colors = MiuixCardDefaults.defaultColors(color = containerColor, contentColor = contentColor),
        ) {
            CompositionLocalProvider(LocalContentColor provides contentColor) { content() }
        }
    } else if (outlined) {
        OutlinedCard(modifier = modifier, content = content)
    } else {
        Card(modifier = modifier, colors = CardDefaults.cardColors(containerColor = containerColor), content = content)
    }
}

@Composable
fun HoleButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    secondary: Boolean = false,
) {
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        MiuixTextButton(
            text = text,
            onClick = onClick,
            modifier = modifier,
            enabled = enabled,
            colors = if (secondary) MiuixButtonDefaults.textButtonColors() else MiuixButtonDefaults.textButtonColorsPrimary(),
        )
    } else if (secondary) {
        OutlinedButton(onClick = onClick, modifier = modifier, enabled = enabled) { Text(text) }
    } else {
        Button(onClick = onClick, modifier = modifier, enabled = enabled) { Text(text) }
    }
}

@Composable
fun HoleTextButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        MiuixTextButton(
            text = text,
            onClick = onClick,
            modifier = modifier,
            minWidth = 48.dp,
            minHeight = 48.dp,
            insideMargin = PaddingValues(horizontal = 12.dp, vertical = 6.dp),
            colors = MiuixButtonDefaults.textButtonColors(color = Color.Transparent, textColor = MiuixTheme.colorScheme.primary),
        )
    } else {
        TextButton(onClick = onClick, modifier = modifier) { Text(text) }
    }
}

@Composable
fun HoleIconButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    tonal: Boolean = false,
    content: @Composable () -> Unit,
) {
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        MiuixIconButton(
            onClick = onClick,
            modifier = modifier,
            minWidth = 48.dp,
            minHeight = 48.dp,
            backgroundColor = if (tonal) MiuixTheme.colorScheme.secondaryVariant else Color.Transparent,
            content = content,
        )
    } else if (tonal) {
        FilledTonalIconButton(onClick = onClick, modifier = modifier, content = content)
    } else {
        IconButton(onClick = onClick, modifier = modifier, content = content)
    }
}

@Composable
fun HoleSwitch(
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    val accessibleModifier = modifier.semantics { contentDescription = label }
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        MiuixSwitch(
            checked = checked,
            onCheckedChange = onCheckedChange,
            enabled = enabled,
            modifier = accessibleModifier.minimumInteractiveComponentSize(),
        )
    } else {
        Switch(checked = checked, onCheckedChange = onCheckedChange, enabled = enabled, modifier = accessibleModifier)
    }
}

@Composable
fun HoleTextField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    supportingText: @Composable (() -> Unit)? = null,
    isError: Boolean = false,
    singleLine: Boolean = false,
    visualTransformation: VisualTransformation = VisualTransformation.None,
    keyboardOptions: KeyboardOptions = KeyboardOptions.Default,
    trailingIcon: @Composable (() -> Unit)? = null,
) {
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        val colors = MiuixTheme.colorScheme
        Column(modifier) {
            MiuixTextField(
                value = value,
                onValueChange = onValueChange,
                label = label,
                modifier = Modifier.fillMaxWidth().semantics {
                    if (isError) error("输入内容有误")
                },
                colors = MiuixTextFieldDefaults.textFieldColors(
                    backgroundColor = if (isError) colors.errorContainer else colors.secondaryContainer,
                    labelColor = if (isError) colors.error else colors.onSurfaceVariantSummary,
                    borderColor = if (isError) colors.error else colors.primary,
                ),
                textStyle = MiuixTheme.textStyles.main.copy(color = colors.onSurface),
                singleLine = singleLine,
                visualTransformation = visualTransformation,
                keyboardOptions = keyboardOptions,
                trailingIcon = trailingIcon,
            )
            if (supportingText != null) {
                CompositionLocalProvider(LocalContentColor provides if (isError) colors.error else MaterialTheme.colorScheme.onSurfaceVariant) {
                    ProvideTextStyle(MaterialTheme.typography.bodySmall) {
                        Column(Modifier.padding(start = 16.dp, end = 16.dp, top = 4.dp)) { supportingText() }
                    }
                }
            }
        }
    } else {
        OutlinedTextField(
            value = value, onValueChange = onValueChange, label = { Text(label) },
            modifier = modifier, supportingText = supportingText, isError = isError,
            singleLine = singleLine, visualTransformation = visualTransformation,
            keyboardOptions = keyboardOptions, trailingIcon = trailingIcon,
        )
    }
}

@Composable
fun HoleSingleChoice(
    options: List<Pair<String, String>>,
    selectedValue: String,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        val density = LocalDensity.current
        val measurer = rememberTextMeasurer()
        // TabRowWithContour 使用 main 字体、body2 字号和选中时的粗体。
        val labelStyle = MiuixTheme.textStyles.main.copy(
            fontSize = MiuixTheme.textStyles.body2.fontSize,
            fontWeight = FontWeight.Bold,
        )
        // 以真实字宽决定最小宽度；大字体时允许横向滚动，不把“跟随系统”等标签截成省略号。
        val labelWidth = options.maxOf { measurer.measure(it.second, style = labelStyle, softWrap = false).size.width }
        val minWidth = with(density) { (labelWidth + 12.dp.roundToPx() * 2).toDp() }
        MiuixTabRow(
            tabs = options.map { it.second },
            selectedTabIndex = options.indexOfFirst { it.first == selectedValue }.coerceAtLeast(0),
            onTabSelected = { onSelect(options[it].first) },
            modifier = modifier,
            height = 48.dp * density.fontScale.coerceAtLeast(1f),
            minWidth = minWidth.coerceAtLeast(76.dp),
            maxWidth = minWidth.coerceAtLeast(98.dp),
        )
    } else {
        SingleChoiceSegmentedButtonRow(modifier) {
            options.forEachIndexed { index, (value, label) ->
                SegmentedButton(
                    selected = selectedValue == value,
                    onClick = { onSelect(value) },
                    shape = SegmentedButtonDefaults.itemShape(index, options.size),
                ) { Text(label) }
            }
        }
    }
}

/** 保留原有排队、超时与撤销回调，只适配 Snackbar 的渲染，不另建消息队列。 */
@Composable
fun HoleSnackbarHost(state: SnackbarHostState) {
    SnackbarHost(state) { data ->
        if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
            val adapted = remember(data) {
                object : MiuixSnackbarData {
                    override val visuals = MiuixSnackbarVisuals(
                        message = data.visuals.message,
                        actionLabel = data.visuals.actionLabel,
                        withDismissAction = data.visuals.withDismissAction,
                        duration = when (data.visuals.duration) {
                            SnackbarDuration.Short -> MiuixSnackbarDuration.Short
                            SnackbarDuration.Long -> MiuixSnackbarDuration.Long
                            SnackbarDuration.Indefinite -> MiuixSnackbarDuration.Indefinite
                        },
                    )

                    override suspend fun dismiss() = data.dismiss()
                    override suspend fun performAction() = data.performAction()
                }
            }
            MiuixSnackbar(data = adapted)
        } else {
            Snackbar(snackbarData = data)
        }
    }
}
