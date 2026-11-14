import QtQuick 2.12
import "."

Flickable {
    id: chips
    property var options: []
    property string selectedValue: ""
    signal chosen(string value)
    implicitHeight: 30
    clip: true
    contentWidth: row.width; contentHeight: height
    flickableDirection: Flickable.HorizontalFlick
    boundsBehavior: Flickable.StopAtBounds
    Row {
        id: row
        spacing: 5
        Repeater {
            model: chips.options
            Rectangle {
                readonly property var entry: modelData
                readonly property bool selected: String(entry.value) === chips.selectedValue
                width: Math.max(38, label.implicitWidth + 16); height: 28; radius: 8
                color: selected ? Theme.accentSoft : Theme.surfaceRaised
                border.width: selected ? 1 : 0; border.color: Theme.borderActive
                Text { id: label; anchors.centerIn: parent; text: parent.entry.label; color: parent.selected ? Theme.accent : Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 10 }
                MouseArea { anchors.fill: parent; preventStealing: false; onClicked: chips.chosen(String(parent.entry.value)) }
                Accessible.role: Accessible.Button
                Accessible.name: entry.label
                Accessible.onPressAction: chips.chosen(String(entry.value))
            }
        }
    }
}
