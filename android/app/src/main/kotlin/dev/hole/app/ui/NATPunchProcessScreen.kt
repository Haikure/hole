@file:OptIn(androidx.compose.foundation.layout.ExperimentalLayoutApi::class)

package dev.hole.app.ui

import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.DragInteraction
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowForward
import androidx.compose.material.icons.automirrored.filled.ShowChart
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import dev.hole.corebridge.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import java.util.Locale

@Composable
fun NATPunchProcessEntry(onClick: () -> Unit) {
    RaisedPanel(Modifier.fillMaxWidth().clickable(onClick = onClick), color = MaterialTheme.colorScheme.primaryContainer) {
        Row(Modifier.padding(screenSpacing(18.dp)), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            ProcessIcon(Icons.Default.Route)
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("打洞过程", style = MaterialTheme.typography.titleMedium)
                Text("实时步骤、端口矩阵与逐条报文", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Icon(Icons.AutoMirrored.Filled.ArrowForward, "查看打洞过程", Modifier.size(20.dp))
        }
    }
}

@Composable
fun NATPunchProcessScreen(
    snapshot: CoreSnapshot, transportId: String,
    readProcess: suspend (String, String) -> NATPunchProcess, onBack: () -> Unit,
    onExport: (String) -> Unit, exporting: Boolean,
) {
    var live by remember(transportId) { mutableStateOf(NATPunchProcess(transportId = transportId)) }
    var frozen by remember(transportId) { mutableStateOf<NATPunchProcess?>(null) }
    var error by remember(transportId) { mutableStateOf<String?>(null) }
    var loaded by remember(transportId) { mutableStateOf(false) }
    var tab by rememberSaveable(transportId) { mutableStateOf("timeline") }
    var selectedGeneration by rememberSaveable(transportId) { mutableStateOf("") }
    var selectedRound by rememberSaveable(transportId) { mutableIntStateOf(-1) }
    var selectedTrial by rememberSaveable(transportId) { mutableIntStateOf(-1) }
    var selectedSocket by rememberSaveable(transportId) { mutableStateOf("") }
    var selectedTarget by rememberSaveable(transportId) { mutableStateOf("") }
    var replayFraction by remember(transportId) { mutableFloatStateOf(1f) }
    var replayCursor by remember(transportId) { mutableStateOf<Long?>(null) }
    var replaySequence by remember(transportId) { mutableStateOf<Long?>(null) }
    var playing by remember(transportId) { mutableStateOf(false) }
    var follow by remember(transportId) { mutableStateOf(false) }
    var samplePackets by rememberSaveable(transportId) { mutableStateOf(false) }
    var packetKind by rememberSaveable(transportId) { mutableStateOf("all") }
    var packetDirection by rememberSaveable(transportId) { mutableStateOf("all") }
    var packetQuery by rememberSaveable(transportId) { mutableStateOf("") }
    var detail by remember { mutableStateOf<NATPunchEvent?>(null) }
    var sampleDetail by remember { mutableStateOf(false) }
    var socketDetail by remember { mutableStateOf<NATPunchSocket?>(null) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val listState = rememberLazyListState()
    val reader by rememberUpdatedState(readProcess)
    val peer = snapshot.peers.firstOrNull { it.transportId == transportId }

    LaunchedEffect(transportId, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (isActive) {
                try {
                    var delta = reader(transportId, live.sequence)
                    if (live.sequence != "0" && delta.attempts.isNotEmpty() && live.restartsWith(delta)) {
                        delta = reader(transportId, "0")
                    }
                    live = live.merge(delta)
                    loaded = true
                    error = null
                } catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { error = failure.message ?: "过程记录读取失败" }
                val latest = live.attempts.lastOrNull()
                delay(if (latest == null || latest.state == "running" || latest.discovery?.attempts?.lastOrNull()?.state == "running") 250 else 1500)
            }
        }
    }
    LaunchedEffect(listState) {
        listState.interactionSource.interactions.collect { if (it is DragInteraction.Start) follow = false }
    }
    val source = frozen ?: live
    val recordedRun = source.attempts.firstOrNull { it.generation == selectedGeneration } ?: source.attempts.lastOrNull()
    val generation = recordedRun?.generation.orEmpty()
    val allEvents = remember(source.events, generation) { source.events.filter { it.generation == generation } }
    val duration = maxOf(recordedRun?.elapsedMs ?: 0L, allEvents.lastOrNull()?.elapsedMs ?: 0L, 1L)
    val cursor = if (frozen == null) duration else replayCursor ?: (duration * replayFraction).toLong()
    val through = if (frozen == null) Long.MAX_VALUE else replaySequence ?: Long.MAX_VALUE
    val history = remember(allEvents, cursor, through) {
        allEvents.filter { it.elapsedMs < cursor || it.elapsedMs == cursor && it.number <= through }
    }
    val run = remember(recordedRun, history, cursor, frozen) {
        if (frozen == null) recordedRun else recordedRun?.at(cursor, history)
    }
    val discovery = run?.discovery
    val visibleEvents = remember(history, selectedRound, selectedTrial, selectedSocket, selectedTarget) {
        history.filter { (selectedRound < 0 || it.round == selectedRound) && (selectedTrial < 0 || it.trial == selectedTrial) &&
            (selectedSocket.isBlank() || it.socketId == selectedSocket || it.kind == "stage") &&
            (selectedTarget.isBlank() || it.remoteAddress == selectedTarget || it.kind == "stage") }
    }
    val latestRound = run?.rounds?.lastOrNull { selectedRound < 0 || it.round == selectedRound }
    val round = if (selectedTrial < 0) latestRound else history.lastOrNull {
        it.round == latestRound?.round && it.trial == selectedTrial && it.roundSnapshot != null
    }?.roundSnapshot
    val sockets = remember(run?.sockets, selectedRound) {
        run?.sockets.orEmpty().filter { selectedRound < 0 || it.round == selectedRound }
    }
    val packetSource = if (samplePackets) discovery?.events.orEmpty() else visibleEvents
    val packets = remember(packetSource, packetKind, packetDirection, packetQuery) {
        packetSource.filter { event -> event.isPacket && (packetDirection == "all" || event.direction == packetDirection) &&
            when (packetKind) {
                "PROBE", "ACK" -> event.packet == packetKind
                "STUN" -> event.isSTUN
                "signal" -> !event.isDatagram
                "rejected" -> event.validation.endsWith("rejected") || event.kind.endsWith("error")
                else -> true
            } && (packetQuery.isBlank() || listOf(event.title, event.packet, event.localAddress, event.boundAddress,
                event.remoteAddress, event.mappedAddress, event.transactionId, event.sequence).any { it.contains(packetQuery, ignoreCase = true) }) }
    }
    val activity = visibleEvents.lastOrNull { it.isDatagram || it.kind in setOf("socket_selected", "ice_selected") }
    val groups = remember(visibleEvents) { processGroups(visibleEvents) }
    val model = round?.localModel ?: discovery?.attempts?.lastOrNull()?.rounds?.lastOrNull()?.localModel ?:
        NATPunchModel(nat = if (frozen == null) peer?.localNAT ?: NATSnapshot() else NATSnapshot())

    LaunchedEffect(playing, generation, duration) {
        if (playing && frozen != null) {
            while (isActive && replayFraction < 1f) {
                delay(32)
                replayFraction = (replayFraction + 32f / duration).coerceAtMost(1f)
            }
            playing = false
        }
    }
    LaunchedEffect(source.sequence, tab, follow) {
        if (follow && frozen == null && !listState.isScrollInProgress) {
            val last = listState.layoutInfo.totalItemsCount - 1
            if (last >= 0) listState.scrollToItem(last)
        }
    }
    fun resumeLive() { frozen = null; playing = false; replayFraction = 1f; replayCursor = null; replaySequence = null; follow = true }
    fun pause() { frozen = live; playing = false; replayFraction = 1f; replayCursor = null; replaySequence = null; follow = false }
    fun step(next: Boolean) {
        if (frozen == null) pause()
        playing = false
        val event = if (next) allEvents.firstOrNull { it.elapsedMs > cursor || it.elapsedMs == cursor && it.number > through }
            else allEvents.lastOrNull { it.elapsedMs < cursor || it.elapsedMs == cursor && it.number < through }
        replayCursor = event?.elapsedMs ?: if (next) duration else 0L
        replaySequence = event?.number ?: if (next) Long.MAX_VALUE else 0L
        replayFraction = replayCursor!!.toFloat() / duration
    }

    HoleScaffold(title = "打洞过程", navigationIcon = { HoleBackButton(onBack) }, actions = {
        HoleIconButton({ if (frozen == null) pause() else resumeLive() }) {
            Icon(if (frozen == null) Icons.Default.Pause else Icons.Default.Sensors, if (frozen == null) "暂停浏览" else "回到实时")
        }
    }) { insets ->
        BoxWithConstraints(Modifier.fillMaxSize().padding(insets)) {
            val wide = maxWidth.value >= 720 * LocalDensity.current.fontScale && !isWearUi()
            HoleLazyColumn(
                Modifier.fillMaxSize(), state = listState,
                contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 12.dp, bottom = LocalFloatingInset.current + 24.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                item {
                    if (wide) Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        ActivityCard(peer?.peerId ?: source.peerId, run, round, activity, frozen != null, Modifier.weight(1.7f))
                        ProcessNetworkCard(run?.network ?: snapshot.network, Modifier.weight(1f))
                    } else ActivityCard(peer?.peerId ?: source.peerId, run, round, activity, frozen != null)
                }
                if (error != null) item { ProcessNotice(Icons.Default.CloudOff, error.orEmpty(), error = true) }
                if (run == null) item {
                    ProcessCard("等待过程记录", Icons.Default.HourglassEmpty) {
                        Text(if (!loaded) "正在读取这台设备的连接过程…" else "当前连接尚无过程记录。", style = MaterialTheme.typography.bodyMedium)
                        Text("新的连接尝试会在此显示实时端点、算法步骤和报文。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                if (source.attempts.isNotEmpty()) item {
                    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        source.attempts.forEach { attempt ->
                            FilterChip(selected = generation == attempt.generation, onClick = {
                                selectedGeneration = attempt.generation; selectedRound = -1; selectedTrial = -1; selectedSocket = ""; selectedTarget = ""
                                playing = false; replayFraction = 1f; replayCursor = null; replaySequence = null; follow = false
                            }, label = { Text(processPhase(attempt.phase) + " · " + attempt.generation) },
                                leadingIcon = { Icon(processStageIcon(attempt.phase), null, Modifier.size(16.dp)) }, shape = CircleShape)
                        }
                    }
                }
                if (run != null) item {
                    ProcessMetrics(listOf(
                        Triple(Icons.Default.NorthEast, "PROBE 已发", sockets.sumOf { it.probesSent }.toString()),
                        Triple(Icons.Default.DoneAll, "ACK 收到", sockets.sumOf { it.acksReceived }.toString()),
                        Triple(Icons.Default.Hub, "参与 socket", sockets.size.toString()),
                        Triple(Icons.Default.FilterCenterFocus, "目标端口", round?.targets?.size?.toString() ?: "—"),
                    ))
                }
                item {
                    ProcessCard(if (frozen == null) "实时过程" else "过程回放", Icons.Default.Timeline) {
                        FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            ProcessPill(if (frozen == null) "实时" else "已暂停", if (frozen == null) Icons.Default.Sensors else Icons.Default.Pause)
                            Text(processTime(cursor), style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
                            if (replaySequence != null) Text("#" + replaySequence, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace)
                        }
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
                            HoleIconButton({ step(false) }) { Icon(Icons.Default.SkipPrevious, "上一条记录") }
                            HoleIconButton({
                                if (frozen == null) { frozen = live; replayFraction = 0f; follow = false }
                                if (replayFraction >= 1f) replayFraction = 0f
                                replayCursor = null; replaySequence = null
                                playing = !playing
                            }) { Icon(if (playing) Icons.Default.Pause else Icons.Default.PlayArrow, if (playing) "暂停回放" else "播放回放") }
                            HoleIconButton({ step(true) }) { Icon(Icons.Default.SkipNext, "下一条记录") }
                        }
                        Slider(value = if (frozen == null) 1f else replayFraction, onValueChange = {
                            if (frozen == null) frozen = live
                            follow = false; playing = false; replayCursor = null; replaySequence = null; replayFraction = it
                        })
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                            Text("0 ms", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            Text(processTime(duration), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        HoleTextButton(if (frozen != null) "回到实时" else if (follow) "正在跟随" else "跟随最新步骤", { resumeLive() }, Modifier.fillMaxWidth())
                        HoleButton(if (exporting) "正在导出…" else "导出完整记录", { onExport(transportId) }, Modifier.fillMaxWidth(),
                            enabled = loaded && snapshot.nativeReady && source.attempts.isNotEmpty() && !exporting)
                        Text("保存当前保留的全部连接尝试与 NAT 采样为 JSON，不受页面筛选影响。",
                            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                item { HoleSingleChoice(listOf("timeline" to "过程", "ports" to "端口", "packets" to "报文"), tab, { tab = it; samplePackets = false; follow = false }) }
                if (run?.rounds?.any { it.round > 0 } == true && !(tab == "packets" && samplePackets)) item {
                    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        FilterChip(selectedRound < 0, { selectedRound = -1; selectedTrial = -1; selectedSocket = ""; selectedTarget = "" }, label = { Text("全过程") }, shape = CircleShape)
                        run.rounds.filter { it.round > 0 }.forEach { r ->
                            FilterChip(selectedRound == r.round, { selectedRound = r.round; selectedTrial = -1; selectedSocket = ""; selectedTarget = "" },
                                label = { Text("第 " + r.round + " 轮") }, leadingIcon = { Icon(Icons.Default.Refresh, null, Modifier.size(16.dp)) }, shape = CircleShape)
                        }
                    }
                }
                val trials = history.filter { selectedRound < 0 || it.round == selectedRound }.map { it.trial }.filter { it > 0 }.distinct().sorted()
                if (trials.size > 1 && !(tab == "packets" && samplePackets)) item {
                    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        FilterChip(selectedTrial < 0, { selectedTrial = -1 }, label = { Text("全部校准") }, shape = CircleShape)
                        trials.forEach { trial -> FilterChip(selectedTrial == trial, { selectedTrial = trial }, label = { Text("校准 " + trial) }, shape = CircleShape) }
                    }
                }
                if (selectedSocket.isNotBlank()) item {
                    InputChip(true, { selectedSocket = "" }, label = { Text("正在跟踪 " + processSocketLabel(selectedSocket)) },
                        trailingIcon = { Icon(Icons.Default.Close, "清除 socket 筛选", Modifier.size(16.dp)) })
                }
                if (selectedTarget.isNotBlank()) item {
                    InputChip(true, { selectedTarget = "" }, label = { Text(selectedTarget, fontFamily = FontFamily.Monospace) },
                        leadingIcon = { Icon(Icons.Default.FilterCenterFocus, null, Modifier.size(16.dp)) }, trailingIcon = { Icon(Icons.Default.Close, "清除目标筛选", Modifier.size(16.dp)) })
                }
                when (tab) {
                    "timeline" -> {
                        item {
                            ProcessPair(
                                { ProcessModelCard("本机 NAT", model, Icons.Default.PhoneAndroid) },
                                { ProcessModelCard("对端 NAT", round?.peerModel ?: NATPunchModel(nat = if (frozen == null) peer?.peerNAT ?: NATSnapshot() else NATSnapshot()), Icons.Default.DevicesOther) },
                            )
                        }
                        if (round?.peerPlan != null) item { ProcessPlanCard(round) }
                        if (discovery != null) item {
                            ProcessDiscoveryCard(discovery, onEvent = { detail = it; sampleDetail = true }, onPackets = {
                                samplePackets = true; selectedTrial = -1; selectedSocket = ""; selectedTarget = ""; packetKind = "all"; tab = "packets"; follow = false
                            })
                        }
                        itemsIndexed(groups, key = { _, group -> group.first().sequence }) { index, group ->
                            ProcessTimelineCard(group, index + 1, current = group === groups.lastOrNull() && run?.state == "running" && frozen == null, onEvent = { detail = it; sampleDetail = false },
                                onPackets = { samplePackets = false; selectedRound = group.first().round.takeIf { it > 0 } ?: -1; selectedTrial = -1; tab = "packets"; follow = false })
                        }
                        round?.diagnostic?.let { stats -> item { ProcessRoundSummary(stats) } }
                    }
                    "ports" -> {
                        if (!wide) item { ProcessNetworkCard(run?.network ?: snapshot.network) }
                        if (round?.peerPlan != null) item { ProcessPlanCard(round) }
                        if (sockets.isEmpty()) item { ProcessNotice(Icons.Default.Hub, "业务 socket 尚未创建。") }
                        if (round != null && round.targets.isNotEmpty()) item {
                            ProcessPortMatrix(sockets.filter { it.role == "business" && it.round == round.round }, round.targets, history.filter { it.round == round.round }) { socket, target ->
                                selectedSocket = socket; selectedTarget = target; selectedTrial = -1; samplePackets = false; follow = false; tab = "packets"
                            }
                        }
                        items(sockets.chunked(if (wide) 3 else 2), key = { it.first().id }) { row ->
                            ProcessSocketRow(row, selectedSocket, onSelect = { selectedSocket = it.id; selectedTarget = ""; socketDetail = it; follow = false })
                        }
                    }
                    "packets" -> {
                        if (discovery != null) item {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                FilterChip(!samplePackets, { samplePackets = false }, label = { Text("连接收发") }, shape = CircleShape)
                                FilterChip(samplePackets, { samplePackets = true; selectedTrial = -1; selectedSocket = ""; selectedTarget = ""; packetKind = "all" }, label = { Text("模型采样") },
                                    leadingIcon = { Icon(Icons.Default.Dns, null, Modifier.size(16.dp)) }, shape = CircleShape)
                            }
                        }
                        item {
                            ProcessCard("报文筛选", Icons.Default.FilterList) {
                                HoleTextField(packetQuery, { packetQuery = it }, "搜索端点、事务或记录编号", Modifier.fillMaxWidth(), singleLine = true)
                                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                    listOf("all" to "全部", "PROBE" to "PROBE", "ACK" to "ACK", "STUN" to "STUN", "signal" to "信令", "rejected" to "未通过").forEach { (value, label) ->
                                        FilterChip(packetKind == value, { packetKind = value }, label = { Text(label) }, shape = CircleShape)
                                    }
                                }
                                HoleSingleChoice(listOf("all" to "双向", "out" to "发送", "in" to "接收"), packetDirection, { packetDirection = it })
                            }
                        }
                        item { ProcessNotice(Icons.Default.SwapHoriz, "保留 " + packets.size + " 条收发记录 · 点击报文查看完整端点与认证状态") }
                        items(packets.chunked(if (wide) 2 else 1), key = { (if (samplePackets) "sample/" else "live/") + it.first().sequence }) { row ->
                            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                                row.forEach { event -> ProcessPacketCard(event, Modifier.weight(1f)) { detail = event; sampleDetail = samplePackets } }
                                if (wide && row.size == 1) Spacer(Modifier.weight(1f))
                            }
                        }
                        if (packets.isEmpty()) item { ProcessNotice(Icons.Default.HourglassEmpty, "当前筛选下暂无收发记录。") }
                    }
                }
                if (source.eventsDropped > 0 || source.eventsEvicted > 0) item {
                    ProcessNotice(Icons.Default.History, "较早事件已移出保留窗口 " + source.eventsEvicted + " 条 · 未记录事件 " + source.eventsDropped + " 条")
                }
            }
        }
    }
    detail?.let { event ->
        InspectionSheet(processEventTitle(event), { detail = null }) {
            ProcessEventDetails(event)
            if (event.transactionId.isNotBlank()) HoleButton("查看此事务的请求与响应", {
                packetQuery = event.transactionId; packetKind = "all"; packetDirection = "all"
                samplePackets = sampleDetail; selectedRound = -1; selectedTrial = -1; selectedSocket = ""; selectedTarget = ""
                tab = "packets"; follow = false; detail = null
            }, Modifier.fillMaxWidth())
        }
    }
    socketDetail?.let { socket ->
        val current = run?.sockets?.firstOrNull { it.id == socket.id } ?: socket
        InspectionSheet(current.label + " · " + processRole(current.role), { socketDetail = null }) {
            ProcessSocketDetails(current, history.filter { it.socketId == current.id })
            HoleButton("只查看这个 socket 的报文", { selectedSocket = socket.id; selectedTarget = ""; samplePackets = false; socketDetail = null; tab = "packets" }, Modifier.fillMaxWidth())
        }
    }
}

@Composable
private fun ProcessCard(title: String, icon: ImageVector, modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    RaisedPanel(modifier.fillMaxWidth()) {
        Column(Modifier.padding(screenSpacing(18.dp)).animateContentSize(), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                ProcessIcon(icon, compact = true)
                Text(title, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
            }
            content()
        }
    }
}

@Composable
private fun ProcessIcon(icon: ImageVector, compact: Boolean = false, tone: Color = MaterialTheme.colorScheme.primary) {
    Surface(shape = if (compact) RoundedCornerShape(10.dp) else CircleShape, color = tone.copy(alpha = .12f)) {
        Icon(icon, null, Modifier.padding(if (compact) 8.dp else 12.dp).size(if (compact) 18.dp else 24.dp), tint = tone)
    }
}

@Composable
private fun ProcessPill(text: String, icon: ImageVector? = null, tone: Color = MaterialTheme.colorScheme.primary) {
    Surface(shape = CircleShape, color = tone.copy(alpha = .09f)) {
        Row(Modifier.padding(horizontal = 10.dp, vertical = 5.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(5.dp)) {
            if (icon != null) Icon(icon, null, Modifier.size(13.dp), tint = tone)
            Text(text, style = MaterialTheme.typography.labelSmall, color = tone)
        }
    }
}

@Composable
private fun ProcessPair(left: @Composable () -> Unit, right: @Composable () -> Unit) {
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        if (maxWidth.value >= 300 * LocalDensity.current.fontScale && !isWearUi()) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                Box(Modifier.weight(1f)) { left() }
                Box(Modifier.weight(1f)) { right() }
            }
        } else Column(verticalArrangement = Arrangement.spacedBy(12.dp)) { left(); right() }
    }
}

@Composable
private fun ActivityCard(peerName: String, run: NATPunchRun?, round: NATPunchRound?, event: NATPunchEvent?, replay: Boolean, modifier: Modifier = Modifier) {
    val colors = natColors(when (run?.state) { "connected" -> "green"; "failed" -> "red"; "cancelled" -> "neutral"; else -> "blue" })
    RaisedPanel(modifier.fillMaxWidth(), color = lerp(MaterialTheme.colorScheme.surfaceContainerLow, colors.surface, .65f)) {
        Column(Modifier.padding(screenSpacing(18.dp)), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                ProcessIcon(Icons.Default.Route, tone = colors.ink)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(peerName.ifBlank { "设备打洞过程" }, style = MaterialTheme.typography.titleLarge, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Text(if (round?.mode.isNullOrBlank()) processPhase(run?.phase.orEmpty()) else processMode(round?.mode.orEmpty()), style = MaterialTheme.typography.bodyMedium, color = colors.ink)
                }
                Column(horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(processTime(run?.elapsedMs ?: 0), style = MaterialTheme.typography.titleMedium, fontFamily = FontFamily.Monospace, color = colors.ink)
                    Text(if (replay) "回放节点" else processRunState(run?.state.orEmpty()), style = MaterialTheme.typography.labelSmall, color = colors.ink)
                }
            }
            val local = event?.let { it.localAddress.ifBlank { it.boundAddress } }.orEmpty()
            val remote = event?.remoteAddress.orEmpty().ifBlank { round?.targets?.firstOrNull().orEmpty() }
            ProcessPair(
                { ProcessEndpoint(if (event?.kind == "ice_selected") "本机候选" else if (event?.localAddress.isNullOrBlank()) "绑定 UDP" else "本机 UDP", local,
                    processSource(if (event?.kind == "ice_selected") "ice_candidate" else event?.localAddressSource?.ifBlank { event.addressSource }.orEmpty()), Icons.Default.PhoneAndroid) },
                { ProcessEndpoint(if (event?.direction == "in") "实际回包来源" else if (event?.isSTUN == true) "STUN 服务" else "打洞目标", remote,
                    if (event == null) "算法预测" else if (event.isDatagram) "实际收发" else "选中线路", Icons.Default.FilterCenterFocus) },
            )
            ProcessPacketFlow(event, !replay && event?.isDatagram == true, colors.ink)
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Icon(processStageIcon(round?.stage?.ifBlank { run?.stage.orEmpty() } ?: run?.stage.orEmpty()), null, Modifier.size(18.dp), tint = colors.ink)
                Text(processStage(round?.stage?.ifBlank { run?.stage.orEmpty() } ?: run?.stage.orEmpty()), Modifier.weight(1f), style = MaterialTheme.typography.labelLarge)
            }
            if (event != null) Text(event.title + " · +" + processTime(event.elapsedMs), style = MaterialTheme.typography.labelSmall, color = colors.ink)
            FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                if (round != null && round.round > 0) ProcessPill("第 " + round.round + " 轮", Icons.Default.Refresh)
                if (event?.socketId?.isNotBlank() == true) ProcessPill("S" + event.socketId.substringAfterLast('/').padStart(2, '0'), Icons.Default.Hub)
                if ((event?.packetSequence ?: 0) > 0) ProcessPill("序号 " + event?.packetSequence)
                if (round != null && round.targets.isNotEmpty()) ProcessPill("窗口 " + endpointPort(round.targets.first()) + "–" + endpointPort(round.targets.last()), Icons.Default.LinearScale)
            }
        }
    }
}

@Composable
private fun ProcessPacketFlow(event: NATPunchEvent?, animate: Boolean, ink: Color) {
    val progress = remember { Animatable(1f) }
    LaunchedEffect(event?.sequence, animate) {
        if (animate) { progress.snapTo(0f); progress.animateTo(1f, tween(550, easing = LinearEasing)) }
        else progress.snapTo(1f)
    }
    Canvas(Modifier.fillMaxWidth().height(14.dp)) {
        val y = size.height / 2
        val start = 5.dp.toPx()
        val end = size.width - start
        drawLine(ink.copy(alpha = .18f), Offset(start, y), Offset(end, y), 2.dp.toPx())
        drawCircle(ink.copy(alpha = .35f), 3.dp.toPx(), Offset(start, y))
        drawCircle(ink.copy(alpha = .35f), 3.dp.toPx(), Offset(end, y))
        if (event?.isDatagram == true) {
            val fraction = if (event.direction == "in") 1f - progress.value else progress.value
            val point = Offset(start + (end - start) * fraction, y)
            drawCircle(ink.copy(alpha = .12f), 7.dp.toPx(), point)
            drawCircle(ink, 3.dp.toPx(), point)
        }
    }
}

@Suppress("DEPRECATION")
@Composable
private fun ProcessEndpoint(title: String, endpoint: String, source: String, icon: ImageVector) {
    val clipboard = LocalClipboardManager.current
    RaisedPanel(color = MaterialTheme.colorScheme.surfaceContainer) {
        Column(Modifier.fillMaxWidth().padding(screenSpacing(14.dp)), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                Icon(icon, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.primary)
                Text(title, Modifier.weight(1f), style = MaterialTheme.typography.labelMedium)
                if (endpoint.isNotBlank()) HoleIconButton({ clipboard.setText(AnnotatedString(endpoint)) }) {
                    Icon(Icons.Default.ContentCopy, "复制端点", Modifier.size(14.dp))
                }
            }
            SelectionContainer {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(endpointHost(endpoint).ifBlank { "待观测" }, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Text(endpointPort(endpoint).ifBlank { "—" }, style = MaterialTheme.typography.headlineSmall, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.primary)
                }
            }
            Text(source.ifBlank { "待观测" }, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun ProcessMetrics(values: List<Triple<ImageVector, String, String>>) {
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        val columns = (maxWidth.value / (140 * LocalDensity.current.fontScale)).toInt().coerceIn(1, 4)
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            values.chunked(columns).forEach { row ->
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    row.forEach { (icon, label, value) ->
                        RaisedPanel(Modifier.weight(1f)) {
                            Column(Modifier.padding(screenSpacing(14.dp)), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                    Icon(icon, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.primary)
                                    Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                                Text(value, style = MaterialTheme.typography.titleLarge, fontFamily = FontFamily.Monospace)
                            }
                        }
                    }
                    repeat(columns - row.size) { Spacer(Modifier.weight(1f)) }
                }
            }
        }
    }
}

@Composable
private fun ProcessNetworkCard(network: NetworkSnapshot, modifier: Modifier = Modifier) {
    ProcessCard("使用的网络", Icons.Default.Lan, modifier) {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            ProcessPill(network.transport.ifBlank { "系统选择" }, Icons.Default.Wifi)
            if (network.interfaceName.isNotBlank()) ProcessPill(network.interfaceName)
        }
        ProcessField("网络编号", network.handle)
        network.addresses.forEach { ProcessField(if (it.contains(':')) "IPv6" else "IPv4", it) }
        if (network.dns.isNotEmpty()) ProcessField("DNS", network.dns.joinToString(" · "))
    }
}

@Composable
private fun ProcessModelCard(title: String, model: NATPunchModel, icon: ImageVector) {
    ProcessCard(title, icon) {
        Text(model.nat.description(local = title.startsWith("本机")), style = MaterialTheme.typography.bodyMedium)
        ProcessField("映射", processBehavior(model.mapping))
        ProcessField("过滤", processBehavior(model.filtering))
        ProcessField("分配范围", processScope(model.allocationScope))
        ProcessField("端口保留", processPreservation(model.preservation))
        ProcessField("No-PAT", processNoPat(model.noPat))
        ProcessField("端口步长", model.portStep.takeIf { it > 0 }?.toString() ?: "待观测")
        if (model.publicIp.isNotBlank()) ProcessField("公网 IP", model.publicIp)
        if (model.lastMappingPort > 0) ProcessField("基准端口", model.lastMappingPort.toString())
        ProcessField("有效样本", model.nat.samples.toString())
        if (model.filteringServer.isNotBlank()) ProcessField("过滤服务", model.filteringServer)
        model.ipEvidence.forEach { evidence ->
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = .35f))
            ProcessField("地址池 IP", evidence.ip)
            Text("目标 " + evidence.targets + " · 端口 " + evidence.ports + " · " + processTrend(evidence.trend),
                style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (evidence.step > 0) ProcessField("此 IP 的步长", evidence.step.toString())
        }
    }
}

@Composable
private fun ProcessField(label: String, value: String, modifier: Modifier = Modifier) {
    Row(modifier.fillMaxWidth(), verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(label, Modifier.weight(.8f), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        SelectionContainer(Modifier.weight(1.3f)) { Text(value.ifBlank { "待观测" }, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace) }
    }
}

@Composable
private fun ProcessNotice(icon: ImageVector, text: String, error: Boolean = false) {
    Row(Modifier.padding(horizontal = 6.dp, vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.Top) {
        Icon(icon, null, Modifier.size(18.dp), tint = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant)
        Text(text, style = MaterialTheme.typography.bodySmall, color = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun ProcessPlanCard(round: NATPunchRound) {
    val plan = round.peerPlan ?: return
    val step = round.peerModel.portStep
    val offset = if (round.mode == "fixed") maxOf(1, plan.sockets / 2) else 1
    val calculation = when {
        round.mode in setOf("fixed", "next") && step > 0 -> plan.port.toString() + " + " + step + " × " + offset + " = " + plan.target
        plan.target >= plan.port -> plan.port.toString() + " + " + (plan.target - plan.port) + " = " + plan.target
        else -> plan.port.toString() + " − " + (plan.port - plan.target) + " = " + plan.target
    }
    ProcessCard("本轮端口计划", Icons.Default.Calculate) {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            ProcessPill(processMode(round.mode), Icons.Default.Route)
            ProcessPill(if (round.dialer) "本机拨号" else "本机监听", Icons.Default.SwapHoriz)
            if (round.trial > 0) ProcessPill("校准 " + round.trial)
        }
        SelectionContainer { Text(calculation, style = MaterialTheme.typography.titleLarge, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.primary) }
        ProcessPair(
            {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    ProcessField("对端校准 IP", plan.ip)
                    ProcessField("校准端口", plan.port.toString())
                    ProcessField("观测步长", if (step > 0) step.toString() else "按本轮趋势试探")
                }
            },
            {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    ProcessField("目标起点", plan.target.toString())
                    ProcessField("扫描宽度", plan.width.toString())
                    ProcessField("对端 socket", plan.sockets.toString())
                }
            },
        )
        if (round.targets.isNotEmpty()) {
            ProcessField("候选端口顺序", endpointPort(round.targets.first()) + " → " + endpointPort(round.targets.last()))
            ProcessField("目标端点", round.targets.first() + if (round.targets.size > 1) " … " + round.targets.last() else "")
        }
        round.localPlan?.let { local ->
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = .35f))
            ProcessField("本机校准端点", local.ip + ":" + local.port)
            ProcessField("本机公布窗口", local.target.toString() + "–" + (local.target + local.width - 1))
            ProcessField("本机业务 socket", local.sockets.toString())
        }
    }
}

@Composable
private fun ProcessDiscoveryCard(discovery: NATPunchProcess, onEvent: (NATPunchEvent) -> Unit, onPackets: () -> Unit) {
    var sampleMode by rememberSaveable { mutableStateOf("base") }
    var showSteps by rememberSaveable { mutableStateOf(false) }
    val allMapped = discovery.events.filter { it.kind == "mapped" && it.mappedAddress.isNotBlank() }
    val mapped = allMapped.filter {
        when (sampleMode) { "base" -> it.socketRole in setOf("nat_base", "nat_probe", ""); "validation" -> it.socketRole == "nat_validation"; "filtering" -> it.socketRole == "nat_filtering"; else -> true }
    }
    val ips = mapped.map { endpointHost(it.mappedAddress) }.distinct()
    var selectedIp by rememberSaveable { mutableStateOf("") }
    val ip = selectedIp.takeIf { it in ips } ?: ips.firstOrNull().orEmpty()
    val samples = mapped.filter { endpointHost(it.mappedAddress) == ip }
    val ports = samples.mapNotNull { endpointPort(it.mappedAddress).toIntOrNull() }
    val ink = MaterialTheme.colorScheme.primary
    val grid = MaterialTheme.colorScheme.outlineVariant.copy(alpha = .4f)
    ProcessCard("模型生成时的 NAT 采样", Icons.AutoMirrored.Filled.ShowChart) {
        val probe = discovery.attempts.lastOrNull()
        FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            ProcessPill(processRunState(probe?.state.orEmpty()), Icons.Default.NetworkCheck)
            Text(allMapped.size.toString() + " 个映射响应", style = MaterialTheme.typography.labelSmall)
            Text(processTime(probe?.elapsedMs ?: 0), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace)
        }
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf("base" to "基础映射", "validation" to "跨 socket", "filtering" to "过滤", "all" to "全部").forEach { (value, label) ->
                FilterChip(sampleMode == value, { sampleMode = value; selectedIp = "" }, label = { Text(label) }, shape = CircleShape)
            }
        }
        if (ips.isNotEmpty()) Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            ips.forEach { value -> FilterChip(ip == value, { selectedIp = value }, label = { Text(value, fontFamily = FontFamily.Monospace) }, shape = CircleShape) }
        }
        if (ports.isNotEmpty()) {
            Canvas(Modifier.fillMaxWidth().height(if (isWearUi()) 80.dp else 120.dp)) {
                val left = 8.dp.toPx()
                val height = size.height - 16.dp.toPx()
                val low = ports.min().toFloat()
                val span = (ports.max() - ports.min()).coerceAtLeast(1).toFloat()
                repeat(3) { row -> drawLine(grid, Offset(left, 8.dp.toPx() + height * row / 2), Offset(size.width - left, 8.dp.toPx() + height * row / 2), 1.dp.toPx()) }
                val points = ports.mapIndexed { index, port ->
                    Offset(left + (size.width - left * 2) * index / (ports.size - 1).coerceAtLeast(1),
                        8.dp.toPx() + height * (1f - (port - low) / span))
                }
                val path = Path()
                points.forEachIndexed { index, point -> if (index == 0) path.moveTo(point.x, point.y) else path.lineTo(point.x, point.y) }
                drawPath(path, ink, style = Stroke(2.dp.toPx()))
                points.forEach { drawCircle(ink, 3.dp.toPx(), it) }
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text("端口 " + ports.min(), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace)
                Text("端口 " + ports.max(), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace)
            }
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                samples.forEachIndexed { index, event ->
                    Surface(onClick = { onEvent(event) }, shape = RoundedCornerShape(12.dp), color = ink.copy(alpha = .08f)) {
                        Column(Modifier.padding(10.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text("样本 " + (index + 1), style = MaterialTheme.typography.labelSmall)
                            Text(endpointPort(event.mappedAddress), style = MaterialTheme.typography.titleMedium, fontFamily = FontFamily.Monospace, color = ink)
                        }
                    }
                }
            }
        }
        discovery.events.filter { it.kind == "dns_result" }.distinctBy { it.remoteAddress }.forEach { event ->
            ProcessField(event.detail, event.remoteAddress)
        }
        val steps = discovery.events.filter { it.kind == "stage" }
        Row(Modifier.fillMaxWidth().clickable { showSteps = !showSteps }, verticalAlignment = Alignment.CenterVertically) {
            Text("采样步骤 · " + steps.size, Modifier.weight(1f), style = MaterialTheme.typography.labelLarge)
            Icon(if (showSteps) Icons.Default.ExpandLess else Icons.Default.ExpandMore, if (showSteps) "收起采样步骤" else "展开采样步骤")
        }
        if (showSteps) steps.forEachIndexed { index, event ->
            Row(Modifier.fillMaxWidth().clickable { onEvent(event) }, horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.Top) {
                Text((index + 1).toString().padStart(2, '0'), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = ink)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(event.title, style = MaterialTheme.typography.bodySmall)
                    Text("+" + processTime(event.elapsedMs) + if (event.socketId.isNotBlank()) " · " + processSocketLabel(event.socketId) else "",
                        style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
        if (discovery.eventsDropped > 0 || discovery.eventsEvicted > 0) ProcessNotice(Icons.Default.History,
            "采样记录已移出 " + discovery.eventsEvicted + " 条 · 未记录 " + discovery.eventsDropped + " 条")
        HoleTextButton("查看采样请求、响应与过滤探测", onPackets, Modifier.fillMaxWidth())
    }
}

private fun processGroups(events: List<NATPunchEvent>): List<List<NATPunchEvent>> {
    val groups = mutableListOf<MutableList<NATPunchEvent>>()
    events.forEach { event ->
        if (groups.isEmpty() || event.kind == "stage") groups.add(mutableListOf())
        groups.last().add(event)
    }
    return groups
}

@Composable
private fun ProcessTimelineCard(events: List<NATPunchEvent>, step: Int, current: Boolean, onEvent: (NATPunchEvent) -> Unit, onPackets: () -> Unit) {
    val first = events.first()
    var expanded by rememberSaveable(first.sequence) { mutableStateOf(current) }
    val packets = events.filter { it.isPacket }
    val colors = natColors(if (current) "blue" else if (events.any { it.kind == "round_failed" }) "red" else "neutral")
    RaisedPanel(color = if (current) colors.surface else MaterialTheme.colorScheme.surfaceContainerLow) {
        Column(Modifier.animateContentSize()) {
            Row(Modifier.fillMaxWidth().clickable { expanded = !expanded }.padding(screenSpacing(16.dp)),
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Surface(shape = CircleShape, color = colors.ink.copy(alpha = .1f)) {
                    Box(Modifier.size(34.dp), contentAlignment = Alignment.Center) {
                        Text(step.toString().padStart(2, '0'), style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace, color = colors.ink)
                    }
                }
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(5.dp)) {
                    Text(first.title, style = MaterialTheme.typography.titleSmall)
                    Text("+" + processTime(first.elapsedMs) + " → " + processTime(events.last().elapsedMs) + " · " + events.size + " 条记录" +
                        if (first.round > 0) " · 第 " + first.round + " 轮" else "",
                        style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Icon(if (expanded) Icons.Default.ExpandLess else Icons.Default.ExpandMore, if (expanded) "收起步骤" else "展开步骤", Modifier.size(20.dp))
            }
            if (expanded) Column(Modifier.padding(start = screenSpacing(16.dp), end = screenSpacing(16.dp), bottom = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = .35f))
                events.filterNot { it.isPacket }.forEach { event ->
                    Row(Modifier.fillMaxWidth().clickable { onEvent(event) }.padding(vertical = 4.dp),
                        verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Icon(processEventIcon(event), null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.primary)
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text(event.title, style = MaterialTheme.typography.bodySmall)
                            val endpoint = event.mappedAddress.ifBlank { event.remoteAddress.ifBlank { event.localAddress } }
                            if (endpoint.isNotBlank()) Text(endpoint, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace)
                            if (event.detail.isNotBlank()) Text(event.detail, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        }
                        Text(processTime(event.elapsedMs), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace)
                    }
                }
                if (packets.isNotEmpty()) {
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        ProcessPill("发送 " + packets.count { it.direction == "out" }, Icons.Default.NorthEast)
                        ProcessPill("接收 " + packets.count { it.direction == "in" }, Icons.Default.SouthWest)
                        ProcessPill("socket " + packets.map { it.socketId }.filter { it.isNotBlank() }.distinct().size, Icons.Default.Hub)
                    }
                    packets.takeLast(3).forEach { event -> ProcessPacketCard(event) { onEvent(event) } }
                    HoleTextButton("查看全部收发记录", onPackets, Modifier.fillMaxWidth())
                }
            }
        }
    }
}

@Composable
private fun ProcessRoundSummary(stats: NATPunchRoundStats) {
    ProcessCard("本轮收发汇总", Icons.Default.Assessment) {
        ProcessPill(if (stats.quicEstablished) "QUIC 已建立" else "本轮已结束", if (stats.quicEstablished) Icons.Default.CheckCircle else Icons.Default.History)
        ProcessField("PROBE 发送 / 接收", stats.probesSent.toString() + " / " + stats.probesReceived)
        ProcessField("ACK 发送 / 接收", stats.acksSent.toString() + " / " + stats.acksReceived)
        ProcessField("挑战回显匹配", stats.acksVerified.toString())
        ProcessField("来源 / 认证未通过", stats.sourceRejected.toString() + " / " + stats.packetRejected)
        ProcessField("接收错误", stats.readErrors.toString())
        if (stats.error.isNotBlank()) Text(stats.error, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
    }
}

@Composable
private fun ProcessPacketCard(event: NATPunchEvent, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val colors = natColors(when { event.validation.endsWith("rejected") -> "red"; event.direction == "in" -> "green"; else -> "blue" })
    RaisedPanel(modifier.fillMaxWidth().clickable(onClick = onClick)) {
        Column(Modifier.padding(screenSpacing(14.dp)), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                ProcessIcon(if (event.direction == "in") Icons.Default.SouthWest else Icons.Default.NorthEast, compact = true, tone = colors.ink)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                    Text(processEventTitle(event), style = MaterialTheme.typography.labelLarge)
                    Text(event.packet + if (event.packetSequence > 0) " #" + event.packetSequence else "",
                        style = MaterialTheme.typography.labelSmall, color = colors.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                Text("+" + processTime(event.elapsedMs), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace)
            }
            if (event.isDatagram) {
                val local = event.localAddress.ifBlank { event.boundAddress }
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                        Text(endpointHost(local).ifBlank { "本机" }, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        Text(endpointPort(local).ifBlank { "—" }, style = MaterialTheme.typography.titleLarge, fontFamily = FontFamily.Monospace)
                    }
                    Icon(if (event.direction == "in") Icons.Default.SouthWest else Icons.Default.NorthEast, null, Modifier.size(18.dp), tint = colors.ink)
                    Column(Modifier.weight(1f), horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(3.dp)) {
                        Text(endpointHost(event.remoteAddress).ifBlank { "对端" }, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        Text(endpointPort(event.remoteAddress).ifBlank { "—" }, style = MaterialTheme.typography.titleLarge, fontFamily = FontFamily.Monospace, color = colors.ink)
                    }
                }
            }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                if (event.socketId.isNotBlank()) ProcessPill("S" + event.socketId.substringAfterLast('/').padStart(2, '0'), Icons.Default.Hub)
                if (event.round > 0) ProcessPill("第 " + event.round + " 轮")
                if (event.trial > 0) ProcessPill("校准 " + event.trial)
                if (event.bytes > 0) ProcessPill(event.bytes.toString() + " B")
                if (event.validation.isNotBlank()) ProcessPill(processValidation(event.validation), Icons.Default.Shield)
            }
            if (event.transactionId.isNotBlank()) Text("TX " + event.transactionId, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun ProcessPortMatrix(sockets: List<NATPunchSocket>, targets: List<String>, events: List<NATPunchEvent>, onSelect: (String, String) -> Unit) {
    val indexed = remember(events) { events.groupBy { it.socketId to it.remoteAddress } }
    val latest = events.lastOrNull { it.isDatagram }
    val scale = LocalDensity.current.fontScale
    val columnWidth = (96 * scale).dp
    val rowHeight = (72 * scale).dp
    val headerHeight = (40 * scale).dp
    ProcessCard("socket × 目标端口", Icons.Default.GridView) {
        ProcessField("目标 IP", targets.firstOrNull()?.let(::endpointHost).orEmpty())
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            listOf("未发送" to "neutral", "已探测" to "blue", "已回应" to "yellow", "未通过" to "red", "已验证" to "green", "已命中" to "green").forEach { (label, tone) ->
                ProcessPill(label, tone = natColors(tone).ink)
            }
        }
        Text("横向滑动查看端口 · 点击格子追踪收发", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Column(Modifier.width((76 * scale).dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Box(Modifier.fillMaxWidth().height(headerHeight), contentAlignment = Alignment.CenterStart) {
                    Text("socket", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                sockets.forEach { socket ->
                    Surface(onClick = { onSelect(socket.id, "") }, modifier = Modifier.fillMaxWidth().height(rowHeight), shape = RoundedCornerShape(12.dp), color = MaterialTheme.colorScheme.surfaceContainer) {
                        Column(Modifier.padding(8.dp), verticalArrangement = Arrangement.Center) {
                            Text(socket.label, style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
                            Text(endpointPort(socket.boundAddress), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
            }
            LazyRow(Modifier.weight(1f), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                items(targets, key = { it }) { target ->
                    Column(Modifier.width(columnWidth), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Surface(onClick = { onSelect("", target) }, modifier = Modifier.fillMaxWidth().height(headerHeight), shape = RoundedCornerShape(10.dp), color = MaterialTheme.colorScheme.primaryContainer) {
                            Box(contentAlignment = Alignment.Center) { Text(endpointPort(target), style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace) }
                        }
                        sockets.forEach { socket ->
                            val related = indexed[socket.id to target].orEmpty()
                            val status = when {
                                related.any { it.kind == "socket_selected" } -> "已命中"
                                related.any { it.kind == "ack_verified" } -> "已验证"
                                related.any { it.kind == "ack_received" || it.kind == "probe_received" } -> "已回应"
                                related.any { it.validation.endsWith("rejected") } -> "未通过"
                                related.any { it.kind == "probe_sent" } -> "已探测"
                                else -> "未发送"
                            }
                            val colors = natColors(when (status) { "已命中", "已验证" -> "green"; "已回应" -> "yellow"; "未通过" -> "red"; "已探测" -> "blue"; else -> "neutral" })
                            val active = latest?.let { it.socketId == socket.id && it.remoteAddress == target } == true
                            Surface(onClick = { onSelect(socket.id, target) }, modifier = Modifier.fillMaxWidth().height(rowHeight), shape = RoundedCornerShape(12.dp), color = colors.surface,
                                border = BorderStroke(if (active) 2.dp else 1.dp, colors.ink.copy(alpha = if (active) .7f else .12f))) {
                                Column(Modifier.padding(8.dp), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                                    Text(status, style = MaterialTheme.typography.labelSmall, color = colors.ink)
                                    Text("↑" + related.count { it.isDatagram && it.direction == "out" } + "  ↓" + related.count { it.isDatagram && it.direction == "in" },
                                        style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = colors.ink)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun ProcessSocketRow(row: List<NATPunchSocket>, selected: String, onSelect: (NATPunchSocket) -> Unit) {
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        val horizontal = maxWidth.value >= 280 * LocalDensity.current.fontScale && !isWearUi()
        if (horizontal) Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            row.forEach { ProcessSocketCard(it, it.id == selected, Modifier.weight(1f)) { onSelect(it) } }
            if (row.size == 1) Spacer(Modifier.weight(1f))
        } else Column(verticalArrangement = Arrangement.spacedBy(12.dp)) { row.forEach { ProcessSocketCard(it, it.id == selected) { onSelect(it) } } }
    }
}

@Composable
private fun ProcessSocketCard(socket: NATPunchSocket, selected: Boolean, modifier: Modifier = Modifier, onClick: () -> Unit) {
    RaisedPanel(modifier.fillMaxWidth().clickable(onClick = onClick), color = if (selected) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerLow) {
        Column(Modifier.padding(screenSpacing(14.dp)), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                ProcessIcon(if (socket.role == "calibration") Icons.Default.Dns else Icons.Default.Hub, compact = true)
                Column(Modifier.weight(1f)) {
                    Text(socket.label, style = MaterialTheme.typography.titleSmall)
                    Text(processRole(socket.role), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            Text(endpointPort(socket.boundAddress), style = MaterialTheme.typography.headlineSmall, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.primary)
            Text(endpointHost(socket.localAddress.ifBlank { socket.boundAddress }), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, maxLines = 2, overflow = TextOverflow.Ellipsis)
            ProcessPill(processSocketState(socket.state))
            Text("PROBE ↑" + socket.probesSent + " ↓" + socket.probesReceived + " · ACK ↓" + socket.acksReceived,
                style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text("公网 " + socket.mappedAddress.ifBlank { "待观测" }, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
private fun ProcessSocketDetails(socket: NATPunchSocket, events: List<NATPunchEvent>) {
    ProcessField("socket", socket.label)
    ProcessField("用途", processRole(socket.role))
    ProcessField("状态", processSocketState(socket.state))
    ProcessField("绑定端点", socket.boundAddress)
    ProcessField("本机端点", socket.localAddress)
    ProcessField("本机 IP 来源", processSource(socket.localAddressSource))
    ProcessField("公网端点", socket.mappedAddress)
    ProcessField("映射来源", processSource(socket.mappingSource))
    ProcessField("最近目标", socket.target)
    ProcessField("创建时间", "+" + processTime(socket.createdMs))
    ProcessField("最近活动", "+" + processTime(socket.lastActivityMs))
    ProcessField("PROBE 发送 / 接收", socket.probesSent.toString() + " / " + socket.probesReceived)
    ProcessField("ACK 发送 / 接收", socket.acksSent.toString() + " / " + socket.acksReceived)
    events.filter { it.isDatagram && it.remoteAddress.isNotBlank() }.map { it.remoteAddress }.distinct().forEach { ProcessField("使用过的目标", it) }
    events.filter { it.kind == "probe_sent" }.groupBy { it.remoteAddress }.forEach { (target, sends) ->
        val last = sends.last()
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = .35f))
        ProcessField("探测目标", target)
        ProcessField("发送序号 / 上限", last.packetSequence.toString() + " / " + last.probeLimit)
        ProcessField("重传间隔", "至少 " + last.retryAfterMs + " ms")
        if (last.packetSequence < last.probeLimit && socket.state !in setOf("closed", "retained", "selected")) {
            ProcessField("最早可重传", "+" + processTime(last.elapsedMs + last.retryAfterMs))
        }
    }
}

@Composable
private fun ProcessEventDetails(event: NATPunchEvent) {
    ProcessField("记录编号", event.sequence)
    ProcessField("代次 / 轮次 / 校准", event.generation + " / " + event.round + " / " + event.trial)
    ProcessField("绝对时间", event.time)
    ProcessField("相对时间", "+" + processTime(event.elapsedMs))
    if (event.direction.isNotBlank()) ProcessField("方向", if (event.direction == "in") "接收" else "发送")
    if (event.socketId.isNotBlank()) ProcessField("本机 socket", "S" + event.socketId.substringAfterLast('/').padStart(2, '0'))
    if (event.localAddress.isNotBlank()) ProcessField("本机端点", event.localAddress)
    if (event.localAddressSource.isNotBlank()) ProcessField("本机 IP 来源", processSource(event.localAddressSource))
    if (event.socketRole.isNotBlank()) ProcessField("socket 用途", processRole(event.socketRole))
    if (event.boundAddress.isNotBlank()) ProcessField("绑定端点", event.boundAddress)
    if (event.remoteAddress.isNotBlank()) ProcessField(if (event.direction == "in") "实际来源" else "发送目标", event.remoteAddress)
    if (event.mappedAddress.isNotBlank()) ProcessField("观测映射", event.mappedAddress)
    if (event.addressSource.isNotBlank()) ProcessField("地址来源", processSource(event.addressSource))
    if (event.packet.isNotBlank()) ProcessField("报文类型", event.packet)
    if (event.transactionId.isNotBlank()) ProcessField("STUN 事务", event.transactionId)
    if (event.isSTUN) ProcessField("CHANGE-REQUEST", when (event.changeRequest) { 6 -> "6 · 变更 IP 与端口"; 2 -> "2 · 变更端口"; else -> "0 · 默认来源" })
    if (event.serverAddress.isNotBlank()) ProcessField("请求的 STUN 服务", event.serverAddress)
    if (event.otherAddress.isNotBlank()) ProcessField("OTHER-ADDRESS", event.otherAddress)
    if (event.mode.isNotBlank()) ProcessField("策略", processMode(event.mode))
    if (event.result.isNotBlank()) ProcessField("结果", processResult(event.result))
    if (event.packetSequence > 0) ProcessField("报文序号", event.packetSequence.toString())
    if (event.kind == "probe_sent") {
        ProcessField("探测次数上限", event.probeLimit.toString())
        ProcessField("候选重传间隔", "至少 " + event.retryAfterMs + " ms")
    }
    if (event.peerSocket > 0) ProcessField("对端 socket 编号", event.peerSocket.toString())
    if (event.bytes > 0) ProcessField("报文长度", event.bytes.toString() + " B")
    if (event.validation.isNotBlank()) ProcessField("验证状态", processValidation(event.validation))
    event.plan?.let { ProcessEventPlan("信令中的校准计划", it) }
    event.model?.let { ProcessModelCard("信令中的 NAT 模型", it, Icons.Default.AccountTree) }
    event.roundSnapshot?.let { round ->
        if (event.kind in setOf("model", "plan", "targets", "round_summary")) {
            ProcessField("本轮策略", processMode(round.mode))
            round.localPlan?.let { ProcessEventPlan("本机计划", it) }
            round.peerPlan?.let { ProcessEventPlan("对端计划", it) }
            if (round.targets.isNotEmpty()) ProcessField("扫描端点", round.targets.joinToString("\n"))
            round.diagnostic?.let { ProcessRoundSummary(it) }
        }
    }
    if (event.detail.isNotBlank()) SelectionContainer { Text(event.detail, style = MaterialTheme.typography.bodyMedium) }
}

@Composable
private fun ProcessEventPlan(title: String, plan: NATPunchPlan) {
    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = .35f))
    Text(title, style = MaterialTheme.typography.titleSmall)
    ProcessField("校准映射", plan.ip + ":" + plan.port)
    ProcessField("预测端口", plan.target.toString())
    ProcessField("扫描宽度 / socket", plan.width.toString() + " / " + plan.sockets)
}

private fun endpointHost(endpoint: String): String = when {
    endpoint.startsWith("[") -> endpoint.substringAfter('[').substringBefore(']')
    endpoint.substringAfterLast(':', "").toIntOrNull() != null -> endpoint.substringBeforeLast(':')
    else -> endpoint
}
private fun endpointPort(endpoint: String) = endpoint.substringAfterLast(':', "").takeIf { it.toIntOrNull() != null }.orEmpty()
private fun processTime(ms: Long) = if (ms < 1000) "$ms ms" else String.format(Locale.ROOT, "%.3f s", ms / 1000.0)
private fun processSocketLabel(id: String) = "S" + id.substringAfterLast('/').padStart(2, '0')
private fun processEventTitle(event: NATPunchEvent): String {
    if (!event.kind.startsWith("signal_")) return event.title
    val name = when (event.packet) {
        "offer" -> "打洞参数提议"; "reserve" -> "打洞时段预约"; "reserved" -> "预约确认"
        "model" -> "NAT 分配模型"; "calibrate" -> "校准准备"; "calibrated" -> "校准结果"
        "ready" -> "端口准备"; "go" -> "同步开始"; "failed" -> "本轮结束"; "abort" -> "停止打洞"; "ice_start" -> "ICE 开始"
        else -> event.packet
    }
    return (if (event.kind == "signal_error") "提交未完成 · " else if (event.direction == "in") "收到 · " else "提交 · ") + name
}
private fun processPhase(value: String) = when (value) { "ice", "direct" -> "ICE"; "punch" -> "预测打洞"; "hole" -> "Hole 中继"; "relay", "relay_udp" -> "UDP 中继"; "relay_tcp" -> "TCP 中继"; "nat" -> "NAT 采样"; else -> value.ifBlank { "连接" } }
private fun processMode(value: String) = when (value) {
    "single" -> "单侧对称 · 多 socket 探测"; "fixed" -> "双侧对称 · 固定目标预测"; "next" -> "双侧对称 · 下一端口预测"
    "pool" -> "地址池 · 有限端口扫描"; "cone" -> "双端锥形 · ICE 检查"; "ice", "ordinary" -> "普通 ICE"; else -> "连接准备"
}
private fun processRunState(value: String) = when (value) { "running" -> "进行中"; "connected" -> "已连接"; "complete" -> "采样完成"; "failed" -> "已结束"; "cancelled" -> "已取消"; "insufficient" -> "信息不足"; else -> "准备中" }
private fun processStage(value: String) = when (value) {
    "preparing" -> "准备连接"; "waiting_nat" -> "等待 NAT 采样"; "nat_sampling" -> "映射与过滤采样"; "strategy" -> "选定打洞策略"
    "reservation" -> "预约打洞时段"; "model", "model_refresh" -> "交换分配模型"; "dns" -> "解析 STUN"; "binding" -> "绑定业务端口"
    "calibration_wait" -> "等待同步校准"; "calibration" -> "校准公网映射"; "coordination" -> "校验双方计划"; "udp" -> "UDP 端口探测"
    "quic" -> "QUIC 握手"; "mux" -> "身份与通道协商"; "connected" -> "业务通道就绪"; "ice" -> "ICE 候选检查"
    "nat_base" -> "同 socket 映射采样"; "nat_extra" -> "新 socket 分配验证"; "nat_classify" -> "汇总 NAT 分类证据"
    "nat_filter_base" -> "建立过滤探测基准"; "nat_filter_ip_port" -> "变更响应 IP 与端口"; "nat_filter_port" -> "变更响应端口"
    "nat_filter_alternate" -> "确认备用 IP 连通性"; "nat_validate" -> "跨 socket 验证步长"; "nat_reuse" -> "确认同目标映射稳定"
    else -> "等待实际收发"
}
private fun processStageIcon(value: String): ImageVector = when (value) {
    "dns", "calibration", "nat", "nat_sampling", "nat_base" -> Icons.Default.Dns
    "nat_filter_base", "nat_filter_ip_port", "nat_filter_port", "nat_filter_alternate" -> Icons.Default.Shield
    "nat_extra", "nat_validate", "nat_reuse" -> Icons.Default.Hub
    "nat_classify" -> Icons.Default.AccountTree
    "binding" -> Icons.Default.Hub
    "reservation", "calibration_wait", "waiting_nat" -> Icons.Default.HourglassEmpty
    "model", "model_refresh", "strategy" -> Icons.Default.AccountTree
    "udp" -> Icons.Default.SwapHoriz
    "quic", "mux" -> Icons.Default.Shield
    "connected" -> Icons.Default.CheckCircle
    "ice" -> Icons.Default.Route
    else -> Icons.Default.Timeline
}
private fun processEventIcon(event: NATPunchEvent) = when (event.kind) {
    "mapped" -> Icons.Default.Public; "socket_bound", "socket_selected" -> Icons.Default.Hub
    "socket_closed" -> Icons.Default.Close; "ack_verified" -> Icons.Default.VerifiedUser
    "dns_start", "dns_result", "stun_server" -> Icons.Default.Dns
    "candidate_local", "candidate_remote", "ice_selected" -> Icons.Default.Route
    "round_failed", "write_error", "socket_error" -> Icons.Default.ErrorOutline
    else -> processStageIcon(event.stage)
}
private fun processBehavior(value: String) = when (value) {
    "endpoint_independent" -> "端点无关"; "endpoint_dependent" -> "端点相关"; "address_dependent" -> "地址相关"
    "address_port_dependent", "address_and_port_dependent" -> "地址与端口相关"; else -> "未知"
}
private fun processScope(value: String) = when (value) { "same_socket" -> "同 socket"; "cross_socket" -> "跨 socket"; else -> "未知" }
private fun processTrend(value: String) = when (value) { "incremental", "predictable_incremental" -> "递增"; "decremental", "predictable_decremental" -> "递减"; "stable" -> "稳定"; "irregular" -> "无规律"; else -> "待验证" }
private fun processRole(value: String) = when (value) { "business" -> "打洞业务"; "calibration" -> "STUN 校准"; "nat_probe" -> "NAT 采样"; "nat_base" -> "基础映射采样"; "nat_validation" -> "跨 socket 验证"; "nat_filtering" -> "过滤探测"; "ice" -> "ICE 候选"; else -> value }
private fun processSocketState(value: String) = when (value) { "bound" -> "已绑定"; "probing" -> "探测中"; "verified" -> "ACK 已验证"; "quic" -> "QUIC 握手"; "selected" -> "已选中"; "retained" -> "保留占用"; "closed" -> "已关闭"; else -> value }
private fun processSource(value: String) = when (value) { "socket" -> "socket 绑定"; "selected_network" -> "所选网络 IP"; "selected_route" -> "实际选中路由"; "stun" -> "STUN 返回"; "peer_observed" -> "对端实际观测"; "peer_reported" -> "对端上报"; "ice_candidate" -> "ICE 候选"; else -> "待观测" }
private fun processValidation(value: String) = when (value) { "authenticated" -> "报文认证通过"; "transaction_matched" -> "STUN 事务匹配"; "echo_verified" -> "挑战回显匹配"; "source_rejected" -> "来源不在范围内"; "packet_rejected" -> "报文认证未通过"; "sent" -> "发送成功"; "queued" -> "信令已提交"; "send_failed" -> "提交未完成"; else -> value }
private fun processPreservation(value: String) = when (value) { "preserved" -> "保留本机端口"; "translated" -> "端口发生转换"; "mixed" -> "部分保留"; else -> "待观测" }
private fun processNoPat(value: String) = when (value) { "suspected" -> "疑似 No-PAT"; "not_detected" -> "未观测到"; "not_applicable" -> "不适用"; else -> "待观测" }
private fun processResult(value: String) = when (value) { "ok" -> "完成"; "retry" -> "重新校准"; "refresh" -> "刷新模型"; "exhausted" -> "本轮机会耗尽"; "connected", "complete", "failed", "cancelled", "insufficient" -> processRunState(value); else -> value }
