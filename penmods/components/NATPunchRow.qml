import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

Rectangle {
    id: card
    property var record: ({})
    signal toggleRequested(var value)
    signal socketRequested(string socketId, bool sample)
    signal transactionRequested(string transactionId, bool sample)
    signal stagePacketsRequested(string sequence, int round)
    signal moreStepsRequested(string key)
    property var childData: ({})
    ListModel { id: childModel }
    onRecordChanged: syncChildren()
    Component.onCompleted: syncChildren()
    function syncChildren() {
        var children = record.children || [], next = {}
        for (var i = 0; i < children.length; ++i) {
            var key = children[i].key, found = -1
            next[key] = children[i]
            for (var j = i; j < childModel.count; ++j)
                if (childModel.get(j).rowKey === key) { found = j; break }
            if (found < 0) childModel.insert(i, {rowKey: key})
            else if (found !== i) childModel.move(found, i, 1)
        }
        if (childModel.count > children.length) childModel.remove(children.length, childModel.count - children.length)
        childData = next
    }
    readonly property color ink: Theme.natInk(record.tone || "neutral")
    implicitHeight: body.implicitHeight + 16
    radius: Theme.radiusCard
    color: record.tone === "blue" && record.kind === "stage" ? Theme.accentSoft : Theme.surface
    border.width: tap.pressed ? 1 : 0; border.color: ink

    ColumnLayout {
        id: body
        x: 8; y: 8; width: parent.width - 16; spacing: 6
        Item {
            Layout.fillWidth: true; Layout.preferredHeight: heading.implicitHeight
            ColumnLayout {
                id: heading
                width: parent.width; spacing: 5
                RowLayout {
                    Layout.fillWidth: true; spacing: 6
                    Rectangle {
                        Layout.preferredWidth: 26; Layout.preferredHeight: 26; radius: 8; color: Theme.natSurface(card.record.tone || "neutral")
                        Text { anchors.centerIn: parent; visible: card.record.kind === "stage" && !card.record.complete; text: card.record.step || ""; color: card.ink; font.family: "monospace"; font.pixelSize: 11; font.bold: true }
                        NATIcon { anchors.centerIn: parent; width: 16; height: 16; visible: card.record.kind !== "stage" || !!card.record.complete; name: card.record.kind === "stage" ? "check" : card.record.kind === "socket" ? "core" : card.record.direction === "in" ? "download" : "upload"; tint: card.ink }
                    }
                    Text { Layout.fillWidth: true; text: card.record.title || "记录"; color: Theme.textPrimary; font.family: Theme.fontFamily; font.pixelSize: 12; font.bold: true; wrapMode: Text.Wrap }
                    NATIcon { name: "chevron"; rotation: card.record.expanded ? 90 : 0; tint: Theme.textMuted; Layout.preferredWidth: 12; Layout.preferredHeight: 12 }
                }
                Text { Layout.fillWidth: true; text: card.record.subtitle || ""; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.Wrap }
                RowLayout {
                    Layout.fillWidth: true; spacing: 7
                    visible: card.record.endpointFlow || card.record.kind === "socket"
                    ColumnLayout {
                        Layout.fillWidth: true; Layout.preferredWidth: 1; spacing: 2
                        Text { text: "本机端点"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9 }
                        Text { Layout.fillWidth: true; text: card.record.local ? card.record.local.host || "本机" : "本机"; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.WrapAnywhere; maximumLineCount: 2; elide: Text.ElideRight }
                        Text { text: card.record.local ? card.record.local.port || "—" : "—"; color: Theme.textPrimary; font.family: "monospace"; font.pixelSize: 21 }
                    }
                    NATIcon { name: card.record.kind === "socket" ? "mapping" : "upload"; rotation: card.record.kind === "socket" ? 0 : card.record.direction === "in" ? -90 : 90; tint: card.ink; Layout.preferredWidth: 16; Layout.preferredHeight: 16 }
                    ColumnLayout {
                        Layout.fillWidth: true; Layout.preferredWidth: 1; spacing: 2
                        Text { Layout.fillWidth: true; text: card.record.kind === "socket" ? "观测公网映射" : card.record.direction === "in" ? "实际来源" : "发送目标"; horizontalAlignment: Text.AlignRight; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9 }
                        Text { Layout.fillWidth: true; text: card.record.remote ? card.record.remote.host || (card.record.kind === "socket" ? "公网待观测" : "对端") : "对端"; horizontalAlignment: Text.AlignRight; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.WrapAnywhere; maximumLineCount: 2; elide: Text.ElideRight }
                        Text { Layout.fillWidth: true; text: card.record.remote ? card.record.remote.port || "—" : "—"; horizontalAlignment: Text.AlignRight; color: card.ink; font.family: "monospace"; font.pixelSize: 21 }
                    }
                }
                Text { Layout.fillWidth: true; text: [card.record.packet || "", card.record.socket || "", card.record.bytes || "", card.record.validation || ""].filter(function(v) { return v.length > 0 }).join(" · "); visible: text.length > 0; color: card.ink; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.Wrap }
                Text { visible: card.record.kind === "stage" && card.record.packetCount > 0; text: "发送 ↑" + (card.record.sent || 0) + "    接收 ↓" + (card.record.received || 0); color: card.ink; font.family: Theme.fontFamily; font.pixelSize: 10 }
            }
            MouseArea { id: tap; anchors.fill: parent; preventStealing: false; onClicked: card.toggleRequested(card.record) }
        }
        Reveal {
            Layout.fillWidth: true; expanded: !!card.record.expanded
            ColumnLayout {
                spacing: 7
                DetailList { Layout.fillWidth: true; lines: card.record.lines || [] }
                Repeater {
                    model: childModel
                    delegate: Loader {
                        id: nestedRow
                        readonly property string rowKey: model.rowKey
                        Layout.fillWidth: true; Layout.preferredHeight: item ? item.implicitHeight : 0
                        source: Qt.resolvedUrl("NATPunchRow.qml")
                        onLoaded: item.record = Qt.binding(function() { return card.childData[nestedRow.rowKey] || ({}) })
                        Connections {
                            target: nestedRow.item
                            function onToggleRequested(value) { card.toggleRequested(value) }
                            function onSocketRequested(socketId, sample) { card.socketRequested(socketId, sample) }
                            function onTransactionRequested(transactionId, sample) { card.transactionRequested(transactionId, sample) }
                            function onStagePacketsRequested(sequence, round) { card.stagePacketsRequested(sequence, round) }
                            function onMoreStepsRequested(key) { card.moreStepsRequested(key) }
                        }
                    }
                }
                ActionButton { Layout.fillWidth: true; visible: card.record.remainingSteps > 0; text: "展开更多步骤 · " + card.record.remainingSteps; iconName: "plus"; variant: "link"; onClicked: card.moreStepsRequested(card.record.key) }
                ActionButton { Layout.fillWidth: true; visible: card.record.kind === "stage" && card.record.packetCount > 0; text: "本阶段全部收发 · " + (card.record.packetCount || 0); iconName: "mapping"; variant: "link"; onClicked: card.stagePacketsRequested(card.record.stageSequence, card.record.round || 0) }
                ActionButton { Layout.fillWidth: true; visible: !!card.record.socketId; text: "只看这个 socket 的报文"; iconName: "core"; variant: "link"; onClicked: card.socketRequested(card.record.socketId, String(card.record.key).indexOf("/discovery/") >= 0) }
                ActionButton { Layout.fillWidth: true; visible: !!card.record.transaction; text: "此事务的请求与响应"; iconName: "samples"; variant: "link"; onClicked: card.transactionRequested(card.record.transaction, String(card.record.key).indexOf("/discovery/") >= 0) }
            }
        }
    }
}
