import QtQuick 2.12
import "."

// Title + summary with a toggle. The whole row is the tap target so the control
// stays comfortable with the pen instead of relying on the small knob.
Item {
    id: row

    property string title: ""
    property string summary: ""
    property bool checked: false

    signal toggled(bool nextValue)

    implicitWidth: Theme.contentWidth
    implicitHeight: Math.max(Theme.tapMin, textColumn.implicitHeight)

    Column {
        id: textColumn

        anchors.left: parent.left
        anchors.right: toggleSlot.left
        anchors.rightMargin: 6
        anchors.verticalCenter: parent.verticalCenter
        spacing: 1

        Text {
            width: parent.width
            text: row.title
            color: Theme.textPrimary
            font.family: Theme.fontFamily
            font.pixelSize: Theme.fontLabel
            elide: Text.ElideRight
        }

        Text {
            width: parent.width
            text: row.summary
            visible: text.length > 0
            color: Theme.textMuted
            font.family: Theme.fontFamily
            font.pixelSize: Theme.fontCaption
            wrapMode: Text.WordWrap
        }
    }

    Item {
        id: toggleSlot

        // Reserve a fixed trailing slot. Using the switch rectangle itself as
        // the row item lets some PenMods Qt builds place it at the row's
        // implicit-width origin instead of the right edge.
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        width: 46
        height: 30

        Rectangle {
            id: toggle

            anchors.right: parent.right
            anchors.verticalCenter: parent.verticalCenter
            width: 38
            height: 22
            radius: height / 2
            color: row.checked ? Theme.accent : Theme.trackOff
            border.width: row.checked ? 0 : 1
            border.color: Theme.trackOffBorder
            opacity: row.enabled && !tap.pressed ? 1 : 0.55

            Behavior on color { ColorAnimation { duration: 140 } }

            Rectangle {
                x: row.checked ? parent.width - width - 3 : 3
                y: (parent.height - height) / 2
                width: 16
                height: 16
                radius: 8
                color: row.checked ? Theme.accentInk : Theme.knobOff

                Behavior on x { NumberAnimation { duration: 140; easing.type: Easing.OutCubic } }
            }
        }
    }

    MouseArea {
        id: tap
        anchors.fill: parent
        acceptedButtons: Qt.LeftButton
        enabled: row.enabled
        preventStealing: false
        onClicked: row.toggled(!row.checked)
    }
}
