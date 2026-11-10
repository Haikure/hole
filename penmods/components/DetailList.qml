import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

ColumnLayout {
    id: list
    property var lines: []
    spacing: 8
    Repeater {
        model: list.lines.length
        delegate: ColumnLayout {
            readonly property var line: list.lines[index] || ({label: "", text: ""})
            Layout.fillWidth: true; spacing: 3
            Text { Layout.fillWidth: true; text: line.label.trim(); color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: Theme.fontCaption; wrapMode: Text.Wrap }
            Text { Layout.fillWidth: true; text: line.text; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: Theme.fontValue; wrapMode: Text.WrapAnywhere }
        }
    }
}
