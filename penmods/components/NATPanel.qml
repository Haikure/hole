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
            RowLayout {
                width: parent.width; spacing: 8
                NATIcon { Layout.preferredWidth: 26; Layout.preferredHeight: 26; tint: panel.ink }
                ColumnLayout {
                    Layout.fillWidth: true; spacing: 3
                    Text { Layout.fillWidth: true; text: panel.report.badge || "NAT 检测"; color: panel.ink; font.pixelSize: 10; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
                    Text { Layout.fillWidth: true; text: panel.report.title || "检测当前网络"; color: Theme.textPrimary; font.pixelSize: 15; font.bold: true; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
                }
                ActionButton {
                    objectName: "natAction"
                    Layout.preferredWidth: 34; Layout.preferredHeight: 34
                    iconName: panel.busy ? "close" : "refresh"; variant: "primary"
                    Accessible.name: panel.busy ? "取消检测" : "开始检测"
                    onClicked: { if (panel.busy) panel.cancelRequested(); else panel.startRequested() }
                }
            }
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
                    RowLayout {
                        width: parent.width; spacing: 5
                        NATIcon { name: modelData.icon; tint: panel.ink; Layout.preferredWidth: 16; Layout.preferredHeight: 16 }
                        Text { Layout.fillWidth: true; text: modelData.label; color: Theme.textMuted; font.pixelSize: 10; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
                    }
                    Text { width: parent.width; text: modelData.value; color: Theme.textPrimary; font.pixelSize: 12; font.bold: true; font.family: Theme.fontFamily; wrapMode: Text.Wrap }
                }
            }
        }
    }
    RowLayout {
        Layout.fillWidth: true; visible: panel.report.state === "idle"; spacing: 6
        MetricTile { Layout.fillWidth: true; Layout.preferredWidth: 1; label: "映射与过滤"; value: "了解网络类型"; iconName: "shield" }
        MetricTile { Layout.fillWidth: true; Layout.preferredWidth: 1; label: "端口特征"; value: "查看打洞条件"; iconName: "trend" }
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
    Text { Layout.fillWidth: true; visible: (panel.report.sections || []).length > 0; text: "点按分组查看详情"; color: Theme.textMuted; font.pixelSize: Theme.fontCaption; font.family: Theme.fontFamily }
}
