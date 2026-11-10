import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

RowLayout {
    id: heading
    property string title: ""
    property string subtitle: ""
    property string iconName: "network"
    spacing: 7
    NATIcon { name: heading.iconName; Layout.preferredWidth: 20; Layout.preferredHeight: 20 }
    Text { text: heading.title; color: Theme.textPrimary; font.family: Theme.fontFamily; font.pixelSize: Theme.fontHeader; font.bold: true }
    Text { Layout.fillWidth: true; text: heading.subtitle; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: Theme.fontCaption; elide: Text.ElideRight; horizontalAlignment: Text.AlignRight }
}
