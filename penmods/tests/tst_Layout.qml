import QtQuick 2.12
import QtQuick.Layouts 1.12
import QtTest 1.2
import "../components"

TestCase {
    id: test
    name: "PenLayout"
    width: 320; height: 170; visible: true
    when: windowShown
    Rectangle {
        id: viewport
        anchors.fill: parent; color: Theme.background
        // Reserve the same left rail as the host YVerticalTitleBar.
        NATIcon { x: 15; y: 73; width: 24; height: 24; name: "chevron"; rotation: 180; tint: Theme.textSecondary }
        Flickable {
            id: scroll
            x: 54; y: 8; width: viewport.width - 62; height: viewport.height - 16
            contentHeight: loader.height
            contentWidth: width; clip: true; boundsBehavior: Flickable.StopAtBounds
            Loader { id: loader; width: scroll.width }
        }
    }
    QtObject {
        id: testForm
        function humanSnapshot(snapshot) { return [{label: "核心版本", text: "1.0.7"}] }
        property string statusRaw: ""
        function orderedPeers(peers) { return peers }
        function stateText(state) { return state === "active" ? "已连接" : "正在连接" }
        function peerPathText(peer) { return peer.path_type === "direct" ? "直连 · IPv4" : "中继 · UDP" }
        function bytesText(value) { return value === "2097152" ? "2 MB" : "8 MB" }
        function sendLossText(loss) { return loss.rate_percent === null ? "暂无样本" : loss.rate_percent.toFixed(2) + "%" }
        function peerDetailLines(peer) { return [{label: "本机连接", text: "直连 · 192.168.1.20:40000"}, {label: "对端连接", text: "直连 · 198.51.100.8:51000"}] }
    }
    QtObject {
        id: keyboard
        property int calls: 0
        function open(value, placeholder, accepted) { calls++; accepted("8080") }
    }
    Component { id: homeComponent; HomePanel {} }
    Component { id: runtimeComponent; RuntimePanel { form: testForm } }
    Component { id: mappingComponent; MappingCard { entryId: "ssh"; entryPort: "22"; hostKeyboard: keyboard } }
    SignalSpy { id: spy }

    function load(component) {
        loader.sourceComponent = component
        tryVerify(function() { return loader.status === Loader.Ready })
        scroll.contentY = 0
        wait(80)
        return loader.item
    }
    function init() { test.width = 320; keyboard.calls = 0; spy.clear() }
    function cleanup() { spy.target = null; loader.sourceComponent = undefined }

    function test_homeFitsAndActions() {
        var home = load(homeComponent)
        home.stateTitle = "连接成功"; home.connected = true; home.requested = true; home.identity = "词典笔 · my-room"
        home.snapshot = {peer_transports: [{state: "active"}, {state: "connecting"}], mappings: [{state: "active"}, {state: "paused"}]}
        compare(home.onlinePeers, 1); compare(home.activeMappings, 1)
        verify(home.implicitHeight <= scroll.height, "Home actions must fit one 320×170 screen")
        spy.signalName = "navigate"; spy.target = home
        var nav = findChild(home, "nav-devices")
        mouseClick(nav, nav.width / 2, nav.height / 2)
        compare(spy.count, 1); compare(spy.signalArguments[0][0], "devices")
        var last = findChild(home, "nav-settings").mapToItem(viewport, 0, 44)
        verify(last.y <= viewport.height)
        spy.clear(); spy.signalName = "toggleRequested"
        var power = findChild(home, "homePower")
        mouseClick(power, 20, 20); compare(spy.count, 1)
        home.busy = true
        mouseClick(power, 20, 20); compare(spy.count, 1)
        home.busy = false
        spy.clear(); spy.signalName = "reloadRequested"
        var reload = findChild(home, "homeReload")
        mouseClick(reload, reload.width / 2, reload.height / 2); compare(spy.count, 1)
        home.busy = true
        mouseClick(reload, reload.width / 2, reload.height / 2); compare(spy.count, 1)
        home.busy = false
        home.connected = false; home.requested = false; home.stateTitle = "尚未连接"; home.snapshot = ({})
        compare(home.onlinePeers, 0)
        home.errorText = "信令暂时不可用，请检查网络后重试。"
        verify(home.implicitHeight > scroll.height)
    }
    function test_peersAndQuic() {
        var runtime = load(runtimeComponent)
        runtime.connected = true
        runtime.snapshot = {peer_transports: [{peer_id: "家里的电脑", state: "active", path_type: "direct", rtt_ms: "18", bytes_sent: "2097152", bytes_received: "8388608",
            quic_loss: {rate_percent: 1.25}, send_loss: {rate_percent: 1.5}, receive_loss: {rate_percent: 1.0},
            bandwidth: {total: {peak_bps: 16e6, average_bps: 6e6}, send: {peak_bps: 8e6, average_bps: 2e6}, receive: {peak_bps: 12e6, average_bps: 4e6}}}]}
        wait(100)
        var expand = findChild(runtime, "peerExpand")
        verify(expand !== null)
        compare(findChild(runtime, "quicGrid"), null)
        compare(findChild(runtime, "runtimeDiagnostics"), null)
        var card = findChild(runtime, "peerCard")
        expand.clicked()
        var grid = findChild(runtime, "quicGrid")
        tryVerify(function() { return grid.visible })
        var peakCell = findChild(grid, "quicCell-9")
        compare(peakCell.text, "16")
        // The poll replaces snapshot objects without recreating any controls.
        var next = JSON.parse(JSON.stringify(runtime.snapshot))
        next.peer_transports[0].rtt_ms = "22"
        next.peer_transports[0].bandwidth.total.peak_bps = 20.5e6
        runtime.snapshot = next
        wait(180)
        compare(card, findChild(runtime, "peerCard"))
        compare(expand, findChild(runtime, "peerExpand"))
        compare(grid, findChild(runtime, "quicGrid"))
        compare(card.peer.rtt_ms, "22")
        compare(peakCell, findChild(grid, "quicCell-9"))
        compare(peakCell.text, "20.5")
        verify(grid.visible)
        scroll.contentY = grid.mapToItem(runtime, 0, 0).y - 26
        wait(60)
        for (var i = 0; i < grid.children.length; ++i) {
            var cell = grid.children[i]
            if (cell.hasOwnProperty("text")) verify(cell.width > 20 && cell.x + cell.width <= grid.width + 1)
        }
        test.width = 252
        wait(60)
        verify(grid.width <= scroll.width)
        next.peer_transports.unshift({peer_id: "另一个设备", state: "active"})
        runtime.snapshot = JSON.parse(JSON.stringify(next))
        wait(30)
        verify(card.expanded); compare(card.peer.peer_id, "家里的电脑")
        next.peer_transports.shift()
        runtime.snapshot = JSON.parse(JSON.stringify(next))
        wait(30)
        compare(card, findChild(runtime, "peerCard"))
        runtime.diagnosticsOpen = true
        tryVerify(function() { return findChild(runtime, "runtimeDiagnostics") !== null })
        runtime.diagnosticsOpen = false
        tryCompare(runtime, "diagnosticsOpen", false)
        tryVerify(function() { return findChild(runtime, "runtimeDiagnostics") === null })
        expand.clicked()
        wait(200)
        compare(findChild(runtime, "quicGrid"), null)
        runtime.snapshot = ({})
        tryCompare(runtime, "peers", [])
    }
    function test_mappingEditingAndCollapse() {
        var card = load(mappingComponent)
        verify(!card.expanded)
        spy.signalName = "portEdited"; spy.target = card
        var collapsedHeight = card.implicitHeight
        var expand = findChild(card, "mappingExpand")
        mouseClick(expand, 60, 20)
        tryCompare(card, "expanded", true)
        tryVerify(function() { return card.implicitHeight > collapsedHeight })
        var fields = []
        function visit(item) {
            if (item.hasOwnProperty("numeric") && item.numeric) fields.push(item)
            for (var i = 0; i < item.children.length; ++i) visit(item.children[i])
        }
        visit(card); compare(fields.length, 1)
        fields[0].requestInput()
        compare(keyboard.calls, 1); compare(spy.count, 1); compare(spy.signalArguments[0][0], "8080")
        scroll.contentY = fields[0].mapToItem(card, 0, 0).y - 50
        card.expanded = false
        tryCompare(card, "implicitHeight", collapsedHeight)
    }
}
