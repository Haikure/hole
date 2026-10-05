package dev.hole.app.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.Icon
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
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
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp

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
                    .clip(MaterialTheme.shapes.extraLarge)
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
    direction: String = "",
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
            containerColor = if (enabled) MaterialTheme.colorScheme.surface else MaterialTheme.colorScheme.surfaceContainerLow,
        ) {
            Row(
                Modifier.fillMaxWidth().heightIn(min = 88.dp).padding(horizontal = 14.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Row(
                    Modifier
                        .weight(1f)
                        .clickable(onClickLabel = "编辑 $title", onClick = onEdit),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Surface(
                        modifier = Modifier.size(46.dp),
                        shape = RoundedCornerShape(12.dp),
                        color = if (enabled) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceVariant,
                        contentColor = if (enabled) MaterialTheme.colorScheme.onPrimaryContainer else MaterialTheme.colorScheme.onSurfaceVariant,
                    ) {
                        Box(contentAlignment = Alignment.Center) {
                            Icon(
                                when (direction) {
                                    "提供" -> Icons.Default.Share
                                    "使用" -> Icons.Default.Terminal
                                    else -> Icons.Default.Build
                                },
                                contentDescription = when (direction) { "提供" -> "提供服务"; "使用" -> "使用服务"; else -> "服务映射" },
                                modifier = Modifier.size(24.dp),
                            )
                        }
                    }
                    Column(Modifier.padding(start = 13.dp, end = 8.dp), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                        Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.clickable(onClickLabel = "编辑 $title", onClick = onEdit))
                        Text(
                            subtitle,
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        if (status != null) {
                            Text(
                                status,
                                style = MaterialTheme.typography.labelSmall,
                                color = if (enabled) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
                HoleSwitch(checked = enabled, onCheckedChange = onToggle, label = "启用 $title")
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
    Row(modifier.fillMaxWidth().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() })

        }
        if (subtitle != null) HoleInfoButton(title) { Text(subtitle) }
        FilledTonalButton(
            modifier = Modifier.semantics { contentDescription = addLabel },
            onClick = onAdd,
            shape = CircleShape,
            contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 16.dp, vertical = 10.dp),
        ) {
            Icon(Icons.Filled.Add, contentDescription = null)
            Text("添加", style = MaterialTheme.typography.labelLarge)
        }
    }
}

@Composable
fun HoleEmptyCard(title: String) {
    HoleCard(Modifier.fillMaxWidth(), outlined = true) {
        Column(Modifier.padding(16.dp)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
        }
    }
}
