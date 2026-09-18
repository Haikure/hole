package dev.hole.app.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import dev.hole.app.config.ThemeStyle
import top.yukonga.miuix.kmp.basic.BasicComponent

/**
 * 支持从右向左滑动删除的列表行：背景为删除色，达到阈值后触发 [onDelete]。
 * 行上同时暴露自定义无障碍删除操作，读屏用户不需要滑动也能删除。
 */
@Composable
fun SwipeDismissRow(
    onDelete: () -> Unit,
    modifier: Modifier = Modifier,
    deleteLabel: String = "删除",
    content: @Composable () -> Unit,
) {
    val dismissState = rememberSwipeToDismissBoxState(
        confirmValueChange = { value ->
            if (value == SwipeToDismissBoxValue.EndToStart) {
                onDelete()
                true
            } else {
                false
            }
        },
    )
    SwipeToDismissBox(
        state = dismissState,
        modifier = modifier,
        enableDismissFromStartToEnd = false,
        backgroundContent = {
            Row(
                Modifier
                    .fillMaxSize()
                    .clip(MaterialTheme.shapes.medium)
                    .background(MaterialTheme.colorScheme.errorContainer)
                    .padding(end = 24.dp),
                horizontalArrangement = Arrangement.End,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(
                    Icons.Filled.Delete,
                    contentDescription = deleteLabel,
                    tint = MaterialTheme.colorScheme.onErrorContainer,
                )
            }
        },
    ) {
        content()
    }
}

/**
 * 列表条目：标题、说明、可选状态行、独立开关；整行点击进入编辑，
 * 开关只切换启用状态，不触发行编辑。
 */
@Composable
fun MappingRow(
    title: String,
    subtitle: String,
    status: String?,
    enabled: Boolean,
    onToggle: (Boolean) -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
    modifier: Modifier = Modifier,
) {
    SwipeDismissRow(onDelete = onDelete, modifier = modifier) {
        HoleCard(
            modifier = Modifier
                .fillMaxWidth()
                .semantics {
                    customActions = listOf(CustomAccessibilityAction("删除") { onDelete(); true })
                },
            containerColor = if (enabled) MaterialTheme.colorScheme.surfaceContainerLow else MaterialTheme.colorScheme.surfaceContainer,
        ) {
            if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
                BasicComponent(
                    title = title,
                    summary = listOfNotNull(subtitle, status).joinToString("\n"),
                    onClick = onEdit,
                    endActions = { HoleSwitch(checked = enabled, onCheckedChange = onToggle, label = "启用 $title") },
                )
            } else {
                Row(
                    Modifier
                        .fillMaxWidth()
                        .heightIn(min = 56.dp)
                        .clickable(onClick = onEdit)
                        .padding(start = 16.dp, top = 12.dp, bottom = 12.dp, end = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text(title, style = MaterialTheme.typography.titleMedium)
                        Text(
                            subtitle,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        if (status != null) {
                            Text(
                                status,
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    HoleSwitch(checked = enabled, onCheckedChange = onToggle, label = "启用 $title")
                }
            }
        }
    }
}

/** 区域标题 + 右侧"新增"按钮，两个区域各自独立，不共用一个全局按钮。 */
@Composable
fun SectionHeader(
    title: String,
    subtitle: String?,
    addLabel: String,
    onAdd: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            if (LocalThemeStyle.current == ThemeStyle.MIUIX) HoleSectionTitle(title)
            else Text(title, style = MaterialTheme.typography.titleMedium)
            if (subtitle != null) {
                Text(
                    subtitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        HoleAddButton(addLabel, onClick = onAdd)
    }
}
