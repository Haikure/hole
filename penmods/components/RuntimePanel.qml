import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

ColumnLayout {
    id: panel
    property var form: null
    property var snapshot: ({})
    property bool busy: false
    property bool connected: false
    property bool diagnosticsOpen: false
    property var peerData: ({})
    readonly property var peers: form ? form.orderedPeers(snapshot.peer_transports || []) : []
    signal refreshRequested()
    signal reconnectRequested()
    signal punchProcessRequested(string peerId, string transportId)
    spacing: 6
    // Only membership/order changes touch the model. A poll updates existing
    // cards, preserving their Canvas icons, controls and expansion state.
    ListModel { id: peerModel }
    onPeersChanged: syncPeers()
    Component.onCompleted: syncPeers()
    function syncPeers() {
        var nextData = {}
        for (var i = 0; i < peers.length; ++i) {
            var key = JSON.stringify([peers[i].peer_id || "", peers[i].transport_id || ""])
            nextData[key] = peers[i]
            var found = -1
            for (var j = i; j < peerModel.count; ++j)
                if (peerModel.get(j).peerKey === key) { found = j; break }
            if (found < 0) peerModel.insert(i, {peerKey: key})
            else if (found !== i) peerModel.move(found, i, 1)
        }
        if (peerModel.count > peers.length) peerModel.remove(peers.length, peerModel.count - peers.length)
        peerData = nextData
    }
    RowLayout {
        Layout.fillWidth: true; spacing: 5
        PageHeading { Layout.fillWidth: true; title: "设备"; subtitle: panel.peers.length + " 台"; iconName: "device" }
        ActionButton { Layout.preferredWidth: 32; iconName: "refresh"; Accessible.name: "刷新"; enabled: !panel.busy; onClicked: panel.refreshRequested() }
        ActionButton { Layout.preferredWidth: 32; iconName: "mapping"; Accessible.name: "重选路径"; enabled: panel.connected && !panel.busy; onClicked: panel.reconnectRequested() }
    }
    Rectangle {
        Layout.fillWidth: true; Layout.preferredHeight: 76; visible: panel.peers.length === 0
        color: Theme.surface; radius: 10
        NATIcon { x: 12; anchors.verticalCenter: parent.verticalCenter; width: 28; height: 28; name: "device"; tint: Theme.textMuted }
        Column {
            x: 52; width: parent.width - 62; anchors.verticalCenter: parent.verticalCenter; spacing: 5
            Text { text: "暂无连接设备"; color: Theme.textPrimary; font.family: Theme.fontFamily; font.pixelSize: 13 }
            Text { width: parent.width; text: "加入同一房间，启用匹配的服务。"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10; wrapMode: Text.Wrap }
        }
    }
    Repeater {
        model: peerModel
        delegate: PeerCard {
            objectName: "peerCard"
            Layout.fillWidth: true
            peer: panel.peerData[model.peerKey] || ({})
            form: panel.form
            onExpansionRequested: expanded = value
            onPunchProcessRequested: panel.punchProcessRequested(peerId, transportId)
        }
    }
    Section {
        Layout.fillWidth: true; title: "运行诊断"; iconName: "samples"; collapsible: true; expanded: panel.diagnosticsOpen
        onHeaderTapped: panel.diagnosticsOpen = !panel.diagnosticsOpen
        Loader {
            Layout.fillWidth: true
            active: panel.diagnosticsOpen
            sourceComponent: ColumnLayout {
                DetailList { objectName: "runtimeDiagnostics"; Layout.fillWidth: true; lines: panel.form ? panel.form.humanSnapshot(panel.snapshot) : [] }
                Text { Layout.fillWidth: true; text: panel.form ? panel.form.statusRaw : ""; visible: text.length > 0; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 11; wrapMode: Text.WrapAnywhere }
            }
        }
    }
}
