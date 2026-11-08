import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."
import "NATTime.js" as NATTime

ColumnLayout {
    id: panel
    property var report: ({state: "idle", title: "检测当前网络", summary: "检查映射、过滤与端口特征，无需加入房间。", sections: []})
    property string errorText: ""
    readonly property bool busy: report.state === "queued" || report.state === "probing"
    readonly property color ink: Theme.natInk(report.tone || "neutral")
    signal startRequested()
    signal cancelRequested()
    implicitWidth: Theme.contentWidth
    spacing: 10

    Rectangle {
        Layout.fillWidth: true
        Layout.preferredHeight: hero.implicitHeight + 20
        radius: Theme.radiusCard
        color: Theme.surface
        Column {
            id: hero
            x: 10; y: 10
            width: parent.width - 20
            spacing: 8
            Row {
                spacing: 6
                NATIcon { width: 16; height: 16; tint: panel.ink }
                Text { text: panel.report.badge || "NAT 检测"; color: panel.ink; font.pixelSize: Theme.fontLabel; font.family: Theme.fontFamily; anchors.verticalCenter: parent.verticalCenter }
            }
            Text { width: parent.width; text: panel.report.title || "检测当前网络"; color: panel.ink; font.pixelSize: 17; font.bold: true; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
            Text { width: parent.width; text: panel.report.summary || "检查当前 IPv4 网络的映射与过滤方式。"; color: Theme.textSecondary; font.pixelSize: Theme.fontLabel; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
            Rectangle {
                width: parent.width; height: 3
                visible: panel.busy; color: Theme.surfaceRaised; clip: true
                Rectangle {
                    width: parent.width / 3; height: 3; color: Theme.accent
                    SequentialAnimation on x {
                        running: panel.busy && panel.visible; loops: Animation.Infinite
                        NumberAnimation { from: 0; to: hero.width * 2 / 3; duration: 900; easing.type: Easing.InOutSine }
                        NumberAnimation { to: 0; duration: 900; easing.type: Easing.InOutSine }
                    }
                }
            }
            ActionButton {
                objectName: "natAction"
                width: parent.width
                text: panel.busy ? "取消检测" : panel.report.state === "idle" ? "开始检测" : "重新检测"
                variant: "primary"
                onClicked: { if (panel.busy) panel.cancelRequested(); else panel.startRequested() }
            }
        }
    }
    Text { Layout.fillWidth: true; visible: text.length > 0; text: panel.errorText; color: Theme.error; font.pixelSize: Theme.fontLabel; wrapMode: Text.Wrap }
    GridLayout {
        id: tiles
        objectName: "natTiles"
        Layout.fillWidth: true
        visible: (panel.report.highlights || []).length > 0
        columns: width >= 210 ? 2 : 1
        columnSpacing: 6; rowSpacing: 6
        Repeater {
            model: panel.report.highlights || []
            delegate: Rectangle {
                Layout.fillWidth: true
                Layout.preferredWidth: 1
                Layout.preferredHeight: tileContent.implicitHeight + 16
                radius: Theme.radiusCard; color: Theme.surface
                Column {
                    id: tileContent
                    x: 8; y: 8; width: parent.width - 16; spacing: 5
                    NATIcon { name: modelData.icon; tint: panel.ink; width: 18; height: 18 }
                    Text { width: parent.width; text: modelData.label; color: Theme.textMuted; font.pixelSize: Theme.fontLabel; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
                    Text { width: parent.width; text: modelData.value; color: Theme.textPrimary; font.pixelSize: 12; font.bold: true; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
                }
            }
        }
    }
    Text {
        Layout.fillWidth: true; visible: panel.report.state === "idle"
        text: "完整结果包含 NAT 类型、映射与过滤行为、公网地址、端口规律、IPv4 / IPv6 地址和检测记录。"
        color: Theme.textSecondary; font.pixelSize: Theme.fontLabel; font.family: Theme.fontFamily; wrapMode: Text.Wrap
    }
    Repeater {
        model: panel.report.sections || []
        delegate: Section {
            id: group
            property var section: modelData
            Layout.fillWidth: true
            title: section.title
            collapsible: true
            expanded: index === 0
            onHeaderTapped: expanded = !expanded
            Repeater {
                model: group.section.fields || []
                delegate: ColumnLayout {
                    Layout.fillWidth: true
                    spacing: 5
                    RowLayout {
                        Layout.fillWidth: true; spacing: 5
                        NATIcon { name: group.section.title; tint: panel.ink; Layout.preferredWidth: 14; Layout.preferredHeight: 14 }
                        Text { Layout.fillWidth: true; text: modelData.label; color: panel.ink; font.pixelSize: Theme.fontLabel; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
                    }
                    Text { Layout.fillWidth: true; text: NATTime.format(modelData.value); color: Theme.textPrimary; font.pixelSize: Theme.fontValue; font.family: Theme.fontFamily; wrapMode: Text.WrapAnywhere }
                    Text { Layout.fillWidth: true; visible: text.length > 0; text: modelData.detail || ""; color: Theme.textSecondary; font.pixelSize: Theme.fontCaption; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
                    Rectangle { Layout.fillWidth: true; Layout.preferredHeight: 1; color: Theme.border; opacity: 0.5 }
                    Item { Layout.preferredHeight: 4 }
                }
            }
        }
    }
    Text { Layout.fillWidth: true; text: "点按分组标题查看完整字段。未知表示证据不足，IPv6 地址和 NAT 类型不能替代实际直连检查。"; color: Theme.textMuted; font.pixelSize: Theme.fontCaption; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
}
