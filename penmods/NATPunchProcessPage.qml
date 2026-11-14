import QtQuick 2.12
import QtQuick.Layouts 1.12
import "qrc:/qml/commons"
import "components"

YBackButtonPage {
    id: page
    objectName: "HoleNATPunchProcessPage"
    property var form: null
    property var hostKeyboard: null
    property string peerId: ""
    property string transportId: ""
    property string selectedGeneration: ""
    property bool initialized: false
    property bool closing: false
    property bool paused: false
    property bool playing: false
    property bool follow: false
    property bool freezeReady: false
    property int pauseToken: 0
    property bool modelsOpen: false
    property bool samplesOpen: false
    property bool networkOpen: false
    property bool planOpen: true
    property bool workerBusy: false
    property bool projectionDirty: true
    property bool jumpToRows: false
    property bool jumpToEnd: false
    property int epoch: 0
    property int revision: 0
    property string requestId: ""
    property string nextStream: "connection"
    property string pendingJson: ""
    property string readError: ""
    property real retryAt: 0
    property var currentRead: ({})
    property var pendingRead: null
    property var pendingCommand: null
    property var cursors: ({})
    property var rowData: ({})
    property var filters: freshFilters()
    property var frame: emptyFrame()
    readonly property bool keyboardBusy: !!hostKeyboard && (hostKeyboard.pageOpen || hostKeyboard.opening)
    readonly property bool available: !!holePlugin.natPunchProcessAvailable && typeof holePlugin.requestNATPunchProcess === "function"
    readonly property bool reading: requestId.length > 0 || workerBusy || pendingJson.length > 0
    readonly property var peerChoices: connectionChoices()
    readonly property string notice: holePlugin.reloading ? "正在重载核心…"
        : typeof holePlugin.requestNATPunchProcess !== "function" ? "当前插件未提供打洞过程，请更新插件包"
        : !holePlugin.coreReady ? "等待核心就绪，可点击刷新"
        : !available ? "当前核心未提供打洞过程，请更新 hole-core"
        : readError.length > 0 ? readError
        : frame.waiting ? "等待这台设备的连接过程记录" : ""

    ListModel { id: rows }
    Rectangle { anchors.fill: parent; z: -1; color: Theme.background }
    PageEntrance { target: list }

    function freshFilters() {
        return {tab: "steps", round: -1, trial: -1, socket: "", target: "", stageSequence: "", detail: "", expanded: {}, stageLimits: {},
            direction: "all", packetKind: "all", packetStream: "connection", query: "", packetPage: 0, matrixPortPage: 0, matrixSocketPage: 0, sampleRole: "all", sampleIp: ""}
    }
    function emptyFrame() {
        return {waiting: true, rows: [], attempts: [], rounds: [], trials: [], metrics: [], plans: [], summary: [], localModel: [], peerModel: [],
            network: [], sampleRows: [], sampleSteps: [], samplePoints: [], sampleIps: [], matrix: {sockets: [], targets: [], cells: []}}
    }
    function copy(value) {
        var next = {}
        Object.keys(value || {}).forEach(function(key) { next[key] = value[key] })
        return next
    }
    function connectionChoices() {
        var snapshot = form ? form.runtimeSnapshot : {}, peers = form ? form.orderedPeers(snapshot.peer_transports || []) : [], result = [], seen = {}
        for (var i = 0; i < peers.length; ++i) {
            var peer = peers[i], id = String(peer.transport_id || "")
            if (!id || seen[id] || form.isFixedPeer(peer)) continue
            seen[id] = true
            result.push({value: id, label: peer.peer_id || "设备"})
        }
        if (transportId && !seen[transportId]) result.unshift({value: transportId, label: peerId || "当前设备"})
        return result
    }
    function options() {
        var value = copy(filters)
        value.generation = selectedGeneration; value.paused = paused; value.freezeReady = freezeReady; value.pauseToken = pauseToken; value.follow = follow || playing
        return value
    }
    function invalidate() {
        revision += 1; projectionDirty = true
        dispatchTimer.restart()
    }
    function applyFilters(values) {
        var next = copy(filters)
        Object.keys(values).forEach(function(key) { next[key] = values[key] })
        filters = next
        invalidate()
    }
    function resetRecords() {
        epoch += 1; cursors = {}; pendingJson = ""; pendingRead = null; pendingCommand = null
        selectedGeneration = ""; paused = false; playing = false; follow = false; freezeReady = false
        readError = ""; retryAt = 0; nextStream = "connection"; jumpToRows = false; jumpToEnd = false
        var next = freshFilters()
        next.tab = filters.tab; filters = next
        frame = emptyFrame(); rows.clear(); rowData = {}
        invalidate()
    }
    function refreshRecords() {
        resetRecords()
        holePlugin.refresh()
        Qt.callLater(page.requestRead)
    }
    function choosePeer(value) {
        if (value === transportId) return
        for (var i = 0; i < peerChoices.length; ++i) if (peerChoices[i].value === value) peerId = peerChoices[i].label
        selectedGeneration = ""; transportId = value
        list.positionViewAtBeginning()
    }
    function chooseGeneration(value) {
        if (value === selectedGeneration) return
        selectedGeneration = value; paused = false; playing = false; follow = false; freezeReady = false
        readError = ""; retryAt = 0; nextStream = "connection"
        applyFilters({round: -1, trial: -1, socket: "", target: "", stageSequence: "", detail: "", expanded: {}, packetPage: 0, matrixPortPage: 0, matrixSocketPage: 0})
        list.positionViewAtBeginning()
    }
    function chooseTab(value) {
        follow = false
        applyFilters({tab: value, socket: "", target: "", stageSequence: "", detail: "", packetPage: 0})
    }
    function toggleRecord(record) {
        if (record.kind === "stage") {
            var expanded = {}
            expanded[record.key] = !record.expanded
            applyFilters({expanded: expanded, detail: ""})
        } else applyFilters({detail: filters.detail === record.key ? "" : record.key})
    }
    function moreSteps(key) {
        var limits = copy(filters.stageLimits)
        limits[key] = Math.max(24, Number(limits[key]) || 24) + 24
        applyFilters({stageLimits: limits})
    }
    function packetFilter(key, value) {
        var update = {packetPage: 0, detail: ""}
        update[key] = value; follow = false
        applyFilters(update)
    }
    function showPackets(values) {
        var update = {tab: "packets", packetPage: 0, direction: "all", packetKind: "all", query: "", socket: "", target: "", stageSequence: "", detail: ""}
        Object.keys(values).forEach(function(key) { update[key] = values[key] })
        follow = false; jumpToRows = true
        applyFilters(update)
    }
    function showSocket(socketId, sample) {
        showPackets({socket: socketId, packetStream: sample ? "discovery" : "connection"})
    }
    function showTransaction(transactionId, sample) {
        showPackets({query: transactionId, packetKind: "STUN", packetStream: sample ? "discovery" : "connection"})
    }
    function pauseView() {
        playing = false; paused = !paused; freezeReady = false
        if (paused) {
            pauseToken += 1; follow = false; nextStream = "connection"
            applyFilters({packetPage: Number(frame.pageIndex) || 0})
        } else {
            follow = true; jumpToEnd = true; retryAt = 0; nextStream = "connection"
            invalidate()
        }
        Qt.callLater(page.requestRead)
    }
    function command(name, value) {
        if (!frame.frozen) return
        if (name !== "tick") playing = false
        if (name === "step" && pendingCommand && pendingCommand.command === "step") pendingCommand.value += value
        else pendingCommand = {command: name, value: value}
        invalidate()
    }
    function playReplay() {
        if (!frame.frozen) return
        if (!playing && frame.atEnd) { pendingCommand = {command: "seek", value: 0}; invalidate() }
        playing = !playing
    }
    function dispatch() {
        if (!initialized || closing || !visible || keyboardBusy || workerBusy || !projectionDirty && !pendingJson && !pendingCommand) return
        var message = {epoch: epoch, revision: revision, options: options(), json: pendingJson, read: pendingRead}
        if (pendingCommand) { message.command = pendingCommand.command; message.value = pendingCommand.value }
        pendingJson = ""; pendingRead = null; pendingCommand = null; projectionDirty = false; workerBusy = true
        processWorker.sendMessage(message)
    }
    function requestRead() {
        if (!initialized || closing || !visible || keyboardBusy || !available || holePlugin.reloading || reading || projectionDirty || paused && frame.frozen || Date.now() < retryAt) return
        if (!transportId) return
        var stream = nextStream, key = selectedGeneration + "/" + stream, cursor = cursors[key] || {sequence: "0"}
        currentRead = {epoch: epoch, transport: transportId, generation: selectedGeneration, stream: stream, key: key}
        requestId = holePlugin.requestNATPunchProcess(transportId, cursor.sequence, selectedGeneration, stream)
    }
    function publish(view) {
        var entries = view.rows || [], next = {}, oldSequence = frame.hero ? frame.hero.sequence : "", oldTime = frame.time
        frame = view
        for (var i = 0; i < entries.length; ++i) {
            var key = entries[i].key, found = -1
            next[key] = entries[i]
            for (var j = i; j < rows.count; ++j) if (rows.get(j).rowKey === key) { found = j; break }
            if (found < 0) rows.insert(i, {rowKey: key})
            else if (found !== i) rows.move(found, i, 1)
        }
        if (rows.count > entries.length) rows.remove(entries.length, rows.count - entries.length)
        rowData = next
        var end = jumpToEnd || (follow || playing) && ((frame.hero ? frame.hero.sequence : "") !== oldSequence || frame.time !== oldTime), first = jumpToRows
        jumpToEnd = false; jumpToRows = false
        if (first || end) Qt.callLater(function() {
            if (page.closing || !page.visible || rows.count === 0) return
            if (first) list.positionViewAtIndex(0, ListView.Beginning)
            else list.positionViewAtEnd()
        })
        if (playing && frame.atEnd && !pendingCommand) playing = false
    }

    WorkerScript {
        id: processWorker
        source: "components/NATPunchProcessWorker.js"
        onMessage: {
            page.workerBusy = false
            if (messageObject.epoch !== page.epoch || page.closing) { dispatchTimer.restart(); return }
            var result = messageObject.readResult, read = messageObject.read, drain = false, freeze = false
            if (messageObject.error) { page.readError = messageObject.error; page.retryAt = Date.now() + 2000 }
            else {
                if (result && read) {
                    var next = page.copy(page.cursors)
                    next[read.key] = {sequence: result.sequence, generation: result.generation, more: result.more}
                    page.cursors = next
                    if (read.generation === page.selectedGeneration) {
                        page.nextStream = result.more ? result.stream : result.stream === "connection" && result.hasDiscovery ? "discovery" : "connection"
                        drain = result.more || result.stream === "connection" && result.hasDiscovery
                        freeze = page.paused && !page.freezeReady && !result.more && (result.stream === "discovery" || !result.hasDiscovery)
                    }
                    page.readError = ""
                }
                if (messageObject.revision === page.revision) page.publish(messageObject.view)
            }
            if (freeze) { page.freezeReady = true; page.invalidate() }
            if (page.projectionDirty || page.pendingJson || page.pendingCommand) dispatchTimer.restart()
            else if (drain) Qt.callLater(page.requestRead)
        }
    }
    Timer { id: dispatchTimer; interval: 0; onTriggered: page.dispatch() }
    Timer {
        interval: 500; repeat: true
        running: page.initialized && !page.closing && page.visible && !page.keyboardBusy && page.available && !holePlugin.reloading && !(page.paused && page.frame.frozen)
        onTriggered: page.requestRead()
    }
    Timer { interval: 100; repeat: true; running: page.playing && page.visible && !page.keyboardBusy && !page.closing; onTriggered: if (!page.workerBusy) page.command("tick", 0) }
    Connections {
        target: holePlugin
        ignoreUnknownSignals: true
        function onNatPunchProcessReady(id, json) {
            if (id !== page.requestId) return
            page.requestId = ""
            if (page.closing || page.currentRead.epoch !== page.epoch || page.currentRead.transport !== page.transportId) { Qt.callLater(page.requestRead); return }
            page.pendingJson = json; page.pendingRead = page.currentRead
            page.invalidate()
        }
        function onNatPunchProcessFailed(id, message) {
            if (id !== page.requestId) return
            page.requestId = ""
            if (page.closing || page.currentRead.epoch !== page.epoch) return
            page.readError = message; page.retryAt = Date.now() + 2000
        }
        function onCapabilitiesChanged() {
            if (!page.initialized || page.closing) return
            if (!holePlugin.coreReady) page.resetRecords()
            else { page.invalidate(); Qt.callLater(page.requestRead) }
        }
    }
    onTransportIdChanged: if (initialized) resetRecords()
    onKeyboardBusyChanged: if (initialized) { if (keyboardBusy) playing = false; else invalidate() }
    onVisibleChanged: if (initialized) { if (!visible) playing = false; else { invalidate(); Qt.callLater(page.requestRead) } }
    onBackButtonClickedCallback: { closing = true; playing = false }
    Component.onCompleted: { initialized = true; resetRecords(); holePlugin.refresh() }
    Component.onDestruction: closing = true

    ListView {
        id: list
        anchors.fill: parent
        anchors.leftMargin: 54; anchors.rightMargin: 8; anchors.topMargin: 8; anchors.bottomMargin: 8
        clip: true; spacing: 7; model: rows
        boundsBehavior: Flickable.StopAtBounds
        cacheBuffer: height
        onMovementStarted: if (page.follow) { page.follow = false; page.invalidate() }
        header: ColumnLayout {
            width: list.width; spacing: 7
            RowLayout {
                Layout.fillWidth: true; spacing: 5
                PageHeading { Layout.fillWidth: true; title: "打洞过程"; subtitle: page.peerId; iconName: "steps" }
                ActionButton { Layout.preferredWidth: 32; iconName: "refresh"; Accessible.name: "刷新过程记录"; enabled: !holePlugin.reloading; onClicked: page.refreshRecords() }
            }
            NATPunchChips { Layout.fillWidth: true; visible: page.peerChoices.length > 1; options: page.peerChoices; selectedValue: page.transportId; onChosen: page.choosePeer(value) }
            Rectangle {
                Layout.fillWidth: true; implicitHeight: noticeText.implicitHeight + 16; visible: page.notice.length > 0
                radius: Theme.radiusCard; color: page.readError.length ? Theme.surfaceError : Theme.surface
                Text { id: noticeText; x: 8; y: 8; width: parent.width - 16; text: page.notice; color: page.readError.length ? Theme.natRed : Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 11; wrapMode: Text.Wrap }
            }
            RowLayout {
                Layout.fillWidth: true; visible: (page.frame.attempts || []).length > 0; spacing: 5
                Text { text: "尝试"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10 }
                NATPunchChips { Layout.fillWidth: true; options: [{value: "", label: "最新连接"}].concat(page.frame.attempts || []); selectedValue: page.selectedGeneration; onChosen: page.chooseGeneration(value) }
            }
            NATPunchChips { Layout.fillWidth: true; options: [{value: "steps", label: "阶段"}, {value: "ports", label: "端口"}, {value: "packets", label: "报文"}]; selectedValue: page.filters.tab; onChosen: page.chooseTab(value) }
            NATPunchHero { Layout.fillWidth: true; visible: !!page.frame.hero; hero: page.frame.hero || ({}); paused: page.paused }
            GridLayout {
                Layout.fillWidth: true; columns: 4; columnSpacing: 5; visible: (page.frame.metrics || []).length > 0
                Repeater {
                    model: page.frame.metrics || []
                    Rectangle {
                        Layout.fillWidth: true; Layout.preferredWidth: 1; implicitHeight: 45; radius: 7; color: Theme.surfaceRaised
                        ColumnLayout {
                            anchors.fill: parent; anchors.margins: 5; spacing: 2
                            Text { Layout.fillWidth: true; text: modelData.label; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9; elide: Text.ElideRight }
                            Text { Layout.fillWidth: true; text: modelData.text; color: Theme.accent; font.family: "monospace"; font.pixelSize: 17; elide: Text.ElideRight }
                        }
                    }
                }
            }
            RowLayout {
                Layout.fillWidth: true; visible: !!page.frame.hero; spacing: 5
                ActionButton { Layout.fillWidth: true; Layout.preferredWidth: 1; text: page.paused ? "回到实时" : "暂停画面"; iconName: page.paused ? "play" : "pause"; onClicked: page.pauseView() }
                ActionButton { Layout.fillWidth: true; Layout.preferredWidth: 1; text: page.follow ? "跟随中" : "跟随当前"; iconName: "activity"; variant: page.follow ? "primary" : "secondary"; enabled: !page.paused; onClicked: { page.follow = !page.follow; page.jumpToEnd = page.follow; page.invalidate() } }
            }
            NATPunchReplay { Layout.fillWidth: true; visible: page.paused; frame: page.frame; playing: page.playing; onPlayRequested: page.playReplay(); onStepRequested: page.command("step", direction); onSeekRequested: page.command("seek", fraction) }
            RowLayout {
                Layout.fillWidth: true; visible: (page.frame.rounds || []).length > 1; spacing: 5
                Text { text: "轮次"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10 }
                NATPunchChips { Layout.fillWidth: true; options: page.frame.rounds || []; selectedValue: String(page.filters.round); onChosen: page.applyFilters({round: Number(value), trial: -1, detail: "", socket: "", target: "", stageSequence: "", matrixPortPage: 0, matrixSocketPage: 0, packetPage: 0}) }
            }
            RowLayout {
                Layout.fillWidth: true; visible: (page.frame.trials || []).length > 1; spacing: 5
                Text { text: "校准"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10 }
                NATPunchChips { Layout.fillWidth: true; options: page.frame.trials || []; selectedValue: String(page.filters.trial); onChosen: page.applyFilters({trial: Number(value), detail: "", packetPage: 0}) }
            }
            Section {
                Layout.fillWidth: true; visible: !!page.frame.hero; title: "双端 NAT 模型"; iconName: "shield"; collapsible: true; expanded: page.modelsOpen
                onHeaderTapped: page.modelsOpen = !page.modelsOpen
                Loader {
                    Layout.fillWidth: true; Layout.preferredHeight: item ? item.implicitHeight : 0; active: page.modelsOpen && width > 0
                    sourceComponent: GridLayout {
                        columns: width >= 218 ? 2 : 1; columnSpacing: 6; rowSpacing: 6
                        NATPunchFacts { Layout.fillWidth: true; Layout.preferredWidth: 1; Layout.alignment: Qt.AlignTop; title: "本机 NAT"; iconName: "device"; tone: "blue"; lines: page.frame.localModel || [] }
                        NATPunchFacts { Layout.fillWidth: true; Layout.preferredWidth: 1; Layout.alignment: Qt.AlignTop; title: "对端上报"; iconName: "device"; tone: "green"; lines: page.frame.peerModel || [] }
                    }
                }
            }
            Section {
                Layout.fillWidth: true; visible: (page.frame.plans || []).length > 0; title: "端口预测与本轮计划"; iconName: "trend"; collapsible: true; expanded: page.planOpen
                onHeaderTapped: page.planOpen = !page.planOpen
                Loader {
                    Layout.fillWidth: true; Layout.preferredHeight: item ? item.implicitHeight : 0; active: page.planOpen && width > 0
                    sourceComponent: ColumnLayout {
                        spacing: 7
                        Text { Layout.fillWidth: true; text: page.frame.formula || ""; visible: text.length > 0; color: Theme.accent; font.family: "monospace"; font.pixelSize: 17; wrapMode: Text.WrapAnywhere }
                        NATPunchFacts { Layout.fillWidth: true; lines: page.frame.plans || []; tone: "blue" }
                    }
                }
            }
            Section {
                Layout.fillWidth: true; visible: !!page.frame.hasDiscovery; title: "NAT 采样轨道"; subtitle: "STUN 映射与过滤"; iconName: "samples"; collapsible: true; expanded: page.samplesOpen
                onHeaderTapped: page.samplesOpen = !page.samplesOpen
                Loader {
                    Layout.fillWidth: true; Layout.preferredHeight: item ? item.implicitHeight : 0; active: page.samplesOpen && width > 0
                    sourceComponent: NATPunchSamples {
                        view: page.frame; sampleIp: page.filters.sampleIp; sampleRole: page.filters.sampleRole
                        onIpChosen: page.applyFilters({sampleIp: value, detail: ""})
                        onRoleChosen: page.applyFilters({sampleRole: value, sampleIp: "", detail: ""})
                        onToggleRequested: page.toggleRecord(value)
                        onSocketRequested: page.showSocket(socketId, sample)
                        onTransactionRequested: page.showTransaction(transactionId, sample)
                    }
                }
            }
            Section {
                Layout.fillWidth: true; visible: !!page.frame.hero; title: "当前网络"; iconName: "network"; collapsible: true; expanded: page.networkOpen
                onHeaderTapped: page.networkOpen = !page.networkOpen
                Loader { Layout.fillWidth: true; Layout.preferredHeight: item ? item.implicitHeight : 0; active: page.networkOpen && width > 0; sourceComponent: NATPunchFacts { lines: page.frame.network || [] } }
            }
            Loader {
                Layout.fillWidth: true; Layout.preferredHeight: item ? item.implicitHeight : 0; active: page.filters.tab === "ports" && !!page.frame.hero; visible: active
                sourceComponent: ColumnLayout {
                    spacing: 7
                    PageHeading { Layout.fillWidth: true; title: "端口 × socket"; subtitle: (page.frame.matrix.socketCount || 0) + " socket · " + (page.frame.matrix.targetCount || 0) + " 目标"; iconName: "mapping" }
                    GridLayout {
                        Layout.fillWidth: true; columns: 2; columnSpacing: 5
                        RowLayout {
                            Layout.fillWidth: true; Layout.preferredWidth: 1; spacing: 3
                            ActionButton { Layout.preferredWidth: 26; iconName: "previous"; Accessible.name: "上一组目标端口"; enabled: page.frame.matrix.portPage > 0; onClicked: page.applyFilters({matrixPortPage: page.frame.matrix.portPage - 1}) }
                            Text { Layout.fillWidth: true; text: "端口 " + ((page.frame.matrix.portPage || 0) + 1) + "/" + (page.frame.matrix.portPages || 1); horizontalAlignment: Text.AlignHCenter; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9 }
                            ActionButton { Layout.preferredWidth: 26; iconName: "next"; Accessible.name: "下一组目标端口"; enabled: page.frame.matrix.portPage + 1 < page.frame.matrix.portPages; onClicked: page.applyFilters({matrixPortPage: page.frame.matrix.portPage + 1}) }
                        }
                        RowLayout {
                            Layout.fillWidth: true; Layout.preferredWidth: 1; spacing: 3
                            ActionButton { Layout.preferredWidth: 26; iconName: "previous"; Accessible.name: "上一组业务 socket"; enabled: page.frame.matrix.socketPage > 0; onClicked: page.applyFilters({matrixSocketPage: page.frame.matrix.socketPage - 1}) }
                            Text { Layout.fillWidth: true; text: "socket " + ((page.frame.matrix.socketPage || 0) + 1) + "/" + (page.frame.matrix.socketPages || 1); horizontalAlignment: Text.AlignHCenter; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9 }
                            ActionButton { Layout.preferredWidth: 26; iconName: "next"; Accessible.name: "下一组业务 socket"; enabled: page.frame.matrix.socketPage + 1 < page.frame.matrix.socketPages; onClicked: page.applyFilters({matrixSocketPage: page.frame.matrix.socketPage + 1}) }
                        }
                    }
                    NATPunchMatrix { Layout.fillWidth: true; matrix: page.frame.matrix; onCellChosen: page.showPackets({socket: socketId, target: target, packetStream: "connection"}) }
                    Text { Layout.fillWidth: true; text: "点按格子查看对应报文 · 横滑查看 socket"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.Wrap }
                    PageHeading { Layout.fillWidth: true; title: "socket 明细"; iconName: "core" }
                }
            }
            Loader {
                Layout.fillWidth: true; Layout.preferredHeight: item ? item.implicitHeight : 0; active: page.filters.tab === "packets" && !!page.frame.hero; visible: active
                sourceComponent: ColumnLayout {
                    spacing: 6
                    NATPunchChips { Layout.fillWidth: true; options: [{value: "connection", label: "连接收发"}, {value: "discovery", label: "NAT 采样"}]; selectedValue: page.filters.packetStream; onChosen: { page.follow = false; page.applyFilters({packetStream: value, socket: "", target: "", stageSequence: "", packetPage: 0, detail: ""}) } }
                    NATPunchChips { Layout.fillWidth: true; options: [{value: "all", label: "全部方向"}, {value: "out", label: "发送 ↑"}, {value: "in", label: "接收 ↓"}]; selectedValue: page.filters.direction; onChosen: page.packetFilter("direction", value) }
                    NATPunchChips { Layout.fillWidth: true; options: [{value: "all", label: "全部报文"}, {value: "STUN", label: "STUN"}, {value: "PROBE", label: "PROBE"}, {value: "ACK", label: "ACK"}, {value: "signal", label: "信令"}, {value: "rejected", label: "未通过"}]; selectedValue: page.filters.packetKind; onChosen: page.packetFilter("packetKind", value) }
                    Field { Layout.fillWidth: true; label: "搜索 IP、端口、记录或 STUN 事务"; placeholder: "输入筛选内容"; value: page.filters.query; hostKeyboard: page.hostKeyboard; onEdited: page.packetFilter("query", nextValue) }
                    Text { Layout.fillWidth: true; text: [page.filters.socket ? "socket " + page.filters.socket : "", page.filters.target ? "目标 " + page.filters.target : "", page.filters.stageSequence ? "阶段 #" + page.filters.stageSequence : ""].filter(function(value) { return !!value }).join(" · "); visible: text.length > 0; color: Theme.accent; font.family: Theme.fontFamily; font.pixelSize: 10; wrapMode: Text.WrapAnywhere }
                    ActionButton { Layout.fillWidth: true; visible: !!page.filters.socket || !!page.filters.target || !!page.filters.stageSequence || !!page.filters.query; text: "清除定位筛选"; iconName: "close"; variant: "link"; onClicked: page.applyFilters({socket: "", target: "", stageSequence: "", query: "", detail: "", packetPage: 0}) }
                    RowLayout {
                        Layout.fillWidth: true; spacing: 5
                        ActionButton { Layout.preferredWidth: 32; iconName: "previous"; Accessible.name: "上一页报文"; enabled: page.frame.pageIndex > 0; onClicked: { page.follow = false; page.applyFilters({packetPage: page.frame.pageIndex - 1, detail: ""}) } }
                        Text { Layout.fillWidth: true; text: (page.frame.packetCount || 0) + " 条 · " + ((page.frame.pageIndex || 0) + 1) + "/" + (page.frame.pageCount || 1); horizontalAlignment: Text.AlignHCenter; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10 }
                        ActionButton { Layout.preferredWidth: 32; iconName: "next"; Accessible.name: "下一页报文"; enabled: page.frame.pageIndex + 1 < page.frame.pageCount; onClicked: { page.follow = false; page.applyFilters({packetPage: page.frame.pageIndex + 1, detail: ""}) } }
                    }
                }
            }
            NATPunchFacts { Layout.fillWidth: true; visible: (page.frame.summary || []).length > 0; title: "本轮收发汇总"; iconName: "activity"; lines: page.frame.summary || [] }
            Text { Layout.fillWidth: true; visible: !!page.frame.loss; text: page.frame.loss || ""; color: Theme.natYellow; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.Wrap }
            Item { Layout.fillWidth: true; implicitHeight: 2 }
        }
        delegate: NATPunchRow {
            width: list.width
            record: page.rowData[model.rowKey] || ({})
            onToggleRequested: page.toggleRecord(value)
            onSocketRequested: page.showSocket(socketId, sample)
            onTransactionRequested: page.showTransaction(transactionId, sample)
            onStagePacketsRequested: page.showPackets({stageSequence: sequence, round: round > 0 ? round : -1, packetStream: "connection"})
            onMoreStepsRequested: page.moreSteps(key)
        }
        footer: Item {
            width: list.width; height: message.visible ? message.implicitHeight + 40 : 36
            Text { id: message; width: parent.width; visible: rows.count === 0 && !page.frame.waiting; text: page.filters.tab === "packets" ? "暂无符合筛选的收发记录" : "暂无对应过程记录"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10; wrapMode: Text.Wrap }
        }
    }
    ActionButton { anchors.right: parent.right; anchors.rightMargin: 8; anchors.bottom: parent.bottom; anchors.bottomMargin: 8; z: 5; width: 32; visible: list.contentY > list.height; iconName: "upload"; Accessible.name: "回到过程概览与控制"; onClicked: list.positionViewAtBeginning() }
}
