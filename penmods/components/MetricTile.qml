import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

Rectangle {
    id: tile
    property string label: ""
    property string value: "—"
    property string iconName: "activity"
    implicitHeight: 48
    radius: 7; color: Theme.field
    ColumnLayout {
        anchors.fill: parent; anchors.margins: 7; spacing: 3
        RowLayout {
            spacing: 4
            NATIcon { name: tile.iconName; tint: Theme.textMuted; Layout.preferredWidth: 12; Layout.preferredHeight: 12 }
            Text { text: tile.label; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: Theme.fontCaption }
        }
        Text { Layout.fillWidth: true; text: tile.value; color: Theme.textPrimary; font.family: Theme.fontFamily; font.pixelSize: 12; font.bold: true; elide: Text.ElideRight }
    }
}
