import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

// Compact actions for the 320×170 viewport.
Rectangle {
    id: button

    property string text: ""
    property string variant: "secondary" // primary | secondary | danger | link
    property string iconName: ""
    readonly property color ink: variant === "primary" ? Theme.accentInk : variant === "danger" ? Theme.error : variant === "link" ? Theme.accent : Theme.textPrimary

    signal clicked()

    implicitWidth: Theme.contentWidth
    implicitHeight: Theme.actionHeight
    radius: Theme.radius
    color: {
        if (!button.enabled) return Theme.surfaceRaised
        if (button.variant === "primary") return Theme.accent
        if (button.variant === "danger") return Theme.surfaceError
        if (button.variant === "link") return "transparent"
        return Theme.surfaceRaised
    }
    border.width: button.variant === "danger" ? 1 : 0
    border.color: button.variant === "danger" ? Theme.error : Theme.border
    opacity: button.enabled ? (tap.pressed ? 0.7 : 1) : 0.45
    Behavior on opacity { NumberAnimation { duration: 90 } }
    scale: tap.pressed ? 0.96 : 1
    Behavior on scale { NumberAnimation { duration: 100; easing.type: Easing.OutCubic } }
    Behavior on color { ColorAnimation { duration: 120 } }

    RowLayout {
        anchors.centerIn: parent
        width: Math.min(implicitWidth, parent.width - 12)
        spacing: 6
        NATIcon {
            visible: button.iconName.length > 0
            name: button.iconName; tint: button.ink
            Layout.preferredWidth: 16; Layout.preferredHeight: 16
        }
        Text {
            Layout.fillWidth: true
            visible: button.text.length > 0
            text: button.text
            color: button.ink
            font.family: Theme.fontFamily
            font.pixelSize: Theme.fontLabel
            font.bold: button.variant !== "link"
            horizontalAlignment: Text.AlignHCenter
            elide: Text.ElideRight
        }
    }
    Accessible.role: Accessible.Button
    Accessible.name: button.text.length > 0 ? button.text : button.iconName
    Accessible.onPressAction: if (button.enabled) button.clicked()

    MouseArea {
        id: tap
        anchors.fill: parent
        acceptedButtons: Qt.LeftButton
        preventStealing: false
        onClicked: button.clicked()
    }
}
