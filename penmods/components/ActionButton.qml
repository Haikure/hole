import QtQuick 2.12
import "."

// Shared action target. Every variant keeps a touch height of at least
// Theme.tapMin so buttons stay usable with the pen.
Rectangle {
    id: button

    property string text: ""
    property string variant: "secondary" // primary | secondary | danger | link

    signal clicked()

    implicitWidth: Theme.contentWidth
    implicitHeight: Theme.tapMin
    radius: Theme.radius
    color: {
        if (!button.enabled) return Theme.surfaceRaised
        if (button.variant === "primary") return Theme.accent
        if (button.variant === "danger") return Theme.surfaceError
        if (button.variant === "link") return "transparent"
        return Theme.surfaceRaised
    }
    border.width: button.variant === "link" ? 0 : 1
    border.color: button.variant === "danger" ? Theme.error : Theme.border
    opacity: button.enabled ? (tap.pressed ? 0.7 : 1) : 0.45
    Behavior on opacity { NumberAnimation { duration: 90 } }

    Text {
        anchors.centerIn: parent
        width: parent.width - 12
        text: button.text
        color: {
            if (button.variant === "primary") return Theme.accentInk
            if (button.variant === "danger") return Theme.error
            if (button.variant === "link") return Theme.accent
            return Theme.textPrimary
        }
        font.family: Theme.fontFamily
        font.pixelSize: Theme.fontLabel
        font.bold: button.variant !== "link"
        horizontalAlignment: Text.AlignHCenter
        elide: Text.ElideRight
    }

    MouseArea {
        id: tap
        anchors.fill: parent
        acceptedButtons: Qt.LeftButton
        preventStealing: false
        onClicked: button.clicked()
    }
}
