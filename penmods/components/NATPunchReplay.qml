import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

Rectangle {
    id: replay
    property var frame: ({})
    property bool playing: false
    signal playRequested()
    signal stepRequested(int direction)
    signal seekRequested(real fraction)
    implicitHeight: body.implicitHeight + 16
    radius: Theme.radiusCard; color: Theme.surfaceRaised
    ColumnLayout {
        id: body
        x: 8; y: 8; width: parent.width - 16; spacing: 5
        Text { Layout.fillWidth: true; text: replay.frame.frozen ? (replay.frame.time || "0 ms") + " / " + (replay.frame.durationText || "0 ms") : "正在读取暂停位置…"; color: Theme.accent; font.family: "monospace"; font.pixelSize: 10 }
        Item {
            id: track
            Layout.fillWidth: true; Layout.preferredHeight: 26
            readonly property real progress: Math.max(0, Math.min(1, Number(replay.frame.progress || 0)))
            Rectangle { anchors.verticalCenter: parent.verticalCenter; width: parent.width; height: 2; radius: 1; color: Theme.border }
            Rectangle { anchors.verticalCenter: parent.verticalCenter; width: parent.width * track.progress; height: 2; radius: 1; color: Theme.accent }
            Repeater {
                model: replay.frame.markers || []
                Rectangle { x: Math.max(0, Math.min(1, Number(modelData))) * track.width; anchors.verticalCenter: parent.verticalCenter; width: 1; height: 7; color: Theme.textMuted }
            }
            Rectangle { x: (parent.width - width) * track.progress; anchors.verticalCenter: parent.verticalCenter; width: 8; height: 8; radius: 4; color: Theme.accent }
            MouseArea {
                anchors.fill: parent; enabled: !!replay.frame.frozen; preventStealing: false
                property real startX: 0
                property real startY: 0
                property bool scrubbing: false
                onPressed: { startX = mouse.x; startY = mouse.y; scrubbing = false }
                onPositionChanged: {
                    if (!pressed) return
                    if (!scrubbing && Math.abs(mouse.x - startX) > 5 && Math.abs(mouse.x - startX) > Math.abs(mouse.y - startY)) { scrubbing = true; preventStealing = true }
                    if (scrubbing) replay.seekRequested(Math.max(0, Math.min(1, mouse.x / width)))
                }
                onClicked: replay.seekRequested(Math.max(0, Math.min(1, mouse.x / width)))
                onReleased: { scrubbing = false; preventStealing = false }
                onCanceled: { scrubbing = false; preventStealing = false }
            }
        }
        RowLayout {
            Layout.fillWidth: true; spacing: 6
            ActionButton { Layout.preferredWidth: 32; iconName: "previous"; Accessible.name: "上一个事件"; enabled: !!replay.frame.frozen; onClicked: replay.stepRequested(-1) }
            ActionButton { Layout.fillWidth: true; text: replay.playing ? "暂停回放" : "播放回放"; iconName: replay.playing ? "pause" : "play"; enabled: !!replay.frame.frozen; onClicked: replay.playRequested() }
            ActionButton { Layout.preferredWidth: 32; iconName: "next"; Accessible.name: "下一个事件"; enabled: !!replay.frame.frozen; onClicked: replay.stepRequested(1) }
        }
    }
}
