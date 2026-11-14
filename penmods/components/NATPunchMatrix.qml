import QtQuick 2.12
import "."

Item {
    id: matrixView
    property var matrix: ({sockets: [], targets: [], cells: []})
    readonly property var sockets: matrix.sockets || []
    readonly property var targets: matrix.targets || []
    readonly property bool empty: sockets.length === 0 || targets.length === 0
    signal cellChosen(string socketId, string target)
    implicitHeight: empty ? 40 : board.implicitHeight + 8
    Text { anchors.fill: parent; visible: matrixView.empty; text: "本轮尚无业务 socket 与目标端口"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10; wrapMode: Text.Wrap; verticalAlignment: Text.AlignVCenter }
    Flickable {
        anchors.fill: parent; visible: !matrixView.empty
        clip: true; contentWidth: board.width; contentHeight: board.implicitHeight
        flickableDirection: Flickable.HorizontalFlick; boundsBehavior: Flickable.StopAtBounds
        Column {
            id: board
            width: 90 + matrixView.sockets.length * 58; spacing: 4
            Row {
                spacing: 4
                Text { width: 86; height: 38; text: "目标端口 ↓\nsocket →"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9; verticalAlignment: Text.AlignVCenter }
                Repeater {
                    model: matrixView.sockets
                    Rectangle {
                        width: 54; height: 38; radius: 6; color: Theme.surfaceRaised
                        Column {
                            anchors.centerIn: parent; spacing: 2
                            Text { anchors.horizontalCenter: parent.horizontalCenter; text: modelData.label; color: Theme.accent; font.family: "monospace"; font.pixelSize: 11 }
                            Text { anchors.horizontalCenter: parent.horizontalCenter; text: modelData.port || "—"; color: Theme.textSecondary; font.family: "monospace"; font.pixelSize: 10 }
                        }
                    }
                }
            }
            Repeater {
                model: matrixView.targets.length
                Row {
                    id: targetRow
                    readonly property int targetIndex: index
                    readonly property var endpoint: matrixView.targets[index] || ({})
                    spacing: 4
                    Column {
                        width: 86; height: 44; spacing: 2
                        Text { text: targetRow.endpoint.label || "—"; color: Theme.textPrimary; font.family: "monospace"; font.pixelSize: 17 }
                        Text { width: parent.width; text: targetRow.endpoint.host || ""; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 8; wrapMode: Text.WrapAnywhere; maximumLineCount: 2; elide: Text.ElideRight }
                    }
                    Repeater {
                        model: matrixView.sockets.length
                        Rectangle {
                            id: cell
                            readonly property var entry: (matrixView.matrix.cells[targetRow.targetIndex] || [])[index] || ({})
                            width: 54; height: 44; radius: 6; color: Theme.natSurface(entry.tone || "neutral")
                            border.width: touch.pressed ? 1 : 0; border.color: Theme.natInk(entry.tone || "neutral")
                            Column {
                                anchors.centerIn: parent; spacing: 4
                                Text { anchors.horizontalCenter: parent.horizontalCenter; text: cell.entry.label || "未发送"; color: Theme.natInk(cell.entry.tone || "neutral"); font.family: Theme.fontFamily; font.pixelSize: 9 }
                                Text { anchors.horizontalCenter: parent.horizontalCenter; text: cell.entry.count || "↑0 ↓0"; color: Theme.textMuted; font.family: "monospace"; font.pixelSize: 8 }
                            }
                            MouseArea { id: touch; anchors.fill: parent; preventStealing: false; onClicked: matrixView.cellChosen(cell.entry.socket || "", cell.entry.target || "") }
                            Accessible.role: Accessible.Button
                            Accessible.name: (entry.socket || "") + " → " + (entry.target || "") + " · " + (entry.label || "未发送")
                            Accessible.onPressAction: matrixView.cellChosen(entry.socket || "", entry.target || "")
                        }
                    }
                }
            }
        }
    }
}
