import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

Rectangle {
    id: card
    property var peer: ({})
    property var form: null
    property bool expanded: false
    signal expansionRequested(bool value)
    readonly property bool active: peer.state === "active" || peer.state === "switching"
    implicitHeight: body.height + 16
    radius: 10; color: Theme.surface

    function loss(value, missing) { return value ? form.sendLossText(value) : missing }
    function rate(direction, peak) {
        var value = peer.bandwidth && peer.bandwidth[direction]
        var bps = value && value[peak ? "peak_bps" : "average_bps"]
        return typeof bps === "number" && isFinite(bps) && bps >= 0 ? (bps / 1000000).toFixed(2).replace(/\.?0+$/, "") : "—"
    }
    function metricCell(index) {
        var column = index % 4
        var row = Math.floor(index / 4)
        if (row === 0) return ["", "合计", "上行", "下行"][column]
        if (column === 0) return ["", "丢包率", "峰值", "平均"][row]
        if (row === 1) return loss(peer[["", "quic_loss", "send_loss", "receive_loss"][column]], column === 2 ? "—" : "暂无反馈")
        return rate(["", "total", "send", "receive"][column], row === 2)
    }
    ColumnLayout {
        id: body
        x: 8; y: 8; width: parent.width - 16; spacing: 7
        RowLayout {
            Layout.fillWidth: true; spacing: 6
            NATIcon { name: "device"; Layout.preferredWidth: 19; Layout.preferredHeight: 19 }
            Text { Layout.fillWidth: true; text: card.peer.peer_id || "未命名设备"; color: Theme.textPrimary; font.family: Theme.fontFamily; font.pixelSize: 14; font.bold: true; elide: Text.ElideRight }
            Rectangle { width: 5; height: 5; radius: 3; color: card.active ? Theme.natGreen : Theme.warning }
            Text { text: card.form ? card.form.stateText(card.peer.state) : "—"; color: card.active ? Theme.natGreen : Theme.warning; font.family: Theme.fontFamily; font.pixelSize: 10 }
        }
        Text { Layout.fillWidth: true; text: card.form ? card.form.peerPathText(card.peer) : "—"; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 11; wrapMode: Text.Wrap }
        RowLayout {
            Layout.fillWidth: true; spacing: 5
            MetricTile { Layout.fillWidth: true; Layout.preferredWidth: 1; label: "延迟"; iconName: "latency"; value: Number(card.peer.rtt_ms) > 0 ? card.peer.rtt_ms + " ms" : "—" }
            MetricTile { Layout.fillWidth: true; Layout.preferredWidth: 1; label: "上行"; iconName: "upload"; value: card.form ? card.form.bytesText(card.peer.bytes_sent) : "—" }
            MetricTile { Layout.fillWidth: true; Layout.preferredWidth: 1; label: "下行"; iconName: "download"; value: card.form ? card.form.bytesText(card.peer.bytes_received) : "—" }
        }
        ActionButton {
            objectName: "peerExpand"
            Layout.fillWidth: true
            text: card.expanded ? "收起连接详情" : "连接详情"; iconName: "activity"; variant: "link"
            onClicked: card.expansionRequested(!card.expanded)
        }
        Reveal {
            Layout.fillWidth: true; expanded: card.expanded
            ColumnLayout {
                spacing: 8
                RowLayout {
                    Layout.fillWidth: true
                    Text { text: "QUIC"; color: Theme.accent; font.pixelSize: 12; font.bold: true }
                    Text { Layout.fillWidth: true; text: "速率 Mbps"; horizontalAlignment: Text.AlignRight; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10 }
                }
                // Three direction columns keep values comparable without repeating
                // direction labels or units in every metric.
                GridLayout {
                    objectName: "quicGrid"
                    Layout.fillWidth: true; columns: 4; columnSpacing: 4; rowSpacing: 8
                    Repeater {
                        model: 16
                        Text {
                            objectName: "quicCell-" + index
                            Layout.fillWidth: true; Layout.preferredWidth: 1
                            text: card.metricCell(index); color: index < 4 || index % 4 === 0 ? Theme.textMuted : Theme.textPrimary
                            font.family: Theme.fontFamily; font.pixelSize: 10
                            horizontalAlignment: index % 4 === 0 ? Text.AlignLeft : Text.AlignRight
                            wrapMode: Text.Wrap
                        }
                    }
                }
                Text { Layout.fillWidth: true; text: "丢包率本次连接 · 平均按有效使用时长"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10; wrapMode: Text.Wrap }
                Rectangle { Layout.fillWidth: true; Layout.preferredHeight: 1; color: Theme.border }
                DetailList { Layout.fillWidth: true; lines: card.form ? card.form.peerDetailLines(card.peer) : [] }
            }
        }
    }
}
