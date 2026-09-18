import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

// Segmented single-choice row. Segments share the available width, so the
// component stays correct at any width the caller gives it.
ColumnLayout {
    id: choice

    property string title: ""
    property string summary: ""
    property var options: []
    property string selected: ""

    signal chosen(string nextValue)

    implicitWidth: Theme.contentWidth
    spacing: 3

    Text {
        Layout.fillWidth: true
        text: choice.title
        visible: text.length > 0
        color: Theme.textPrimary
        font.family: Theme.fontFamily
        font.pixelSize: Theme.fontLabel
        elide: Text.ElideRight
    }

    Text {
        Layout.fillWidth: true
        text: choice.summary
        visible: text.length > 0
        color: Theme.textMuted
        font.family: Theme.fontFamily
        font.pixelSize: Theme.fontCaption
        wrapMode: Text.WordWrap
    }

    RowLayout {
        Layout.fillWidth: true
        spacing: 3

        Repeater {
            model: choice.options

            delegate: Rectangle {
                id: segment

                readonly property bool current: choice.selected === modelData.value

                Layout.fillWidth: true
                implicitHeight: Theme.rowHeight
                radius: Theme.radius - 2
                color: segment.current ? Theme.accent : Theme.field
                opacity: tap.pressed ? 0.7 : 1

                Behavior on color { ColorAnimation { duration: 180 } }

                Text {
                    anchors.centerIn: parent
                    width: parent.width - 8
                    text: modelData.label
                    color: segment.current ? Theme.accentInk : Theme.textSecondary
                    font.family: Theme.fontFamily
                    font.pixelSize: Theme.fontLabel
                    font.bold: true
                    horizontalAlignment: Text.AlignHCenter
                    elide: Text.ElideRight
                }

                MouseArea {
                    id: tap
                    anchors.fill: parent
                    acceptedButtons: Qt.LeftButton
                    preventStealing: false
                    onClicked: choice.chosen(modelData.value)
                }
            }
        }
    }
}
