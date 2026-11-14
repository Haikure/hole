import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

Rectangle {
    id: card
    property string title: ""
    property string iconName: "samples"
    property string tone: "neutral"
    property var lines: []
    implicitHeight: body.implicitHeight + 16
    radius: Theme.radiusCard
    color: Theme.natSurface(tone)
    ColumnLayout {
        id: body
        x: 8; y: 8; width: parent.width - 16; spacing: 7
        RowLayout {
            Layout.fillWidth: true; spacing: 5; visible: card.title.length > 0
            NATIcon { name: card.iconName; tint: Theme.natInk(card.tone); Layout.preferredWidth: 15; Layout.preferredHeight: 15 }
            Text { Layout.fillWidth: true; text: card.title; color: Theme.textPrimary; font.family: Theme.fontFamily; font.pixelSize: 11; font.bold: true; wrapMode: Text.Wrap }
        }
        Repeater {
            model: card.lines.length
            RowLayout {
                readonly property var fact: card.lines[index] || ({})
                Layout.fillWidth: true; spacing: 5
                Text { Layout.fillWidth: true; Layout.preferredWidth: 2; Layout.alignment: Qt.AlignTop; text: parent.fact.label || ""; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.Wrap }
                Text { Layout.fillWidth: true; Layout.preferredWidth: 3; Layout.alignment: Qt.AlignTop; text: parent.fact.text === undefined ? "待观测" : String(parent.fact.text); color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 10; wrapMode: Text.WrapAnywhere }
            }
        }
    }
}
