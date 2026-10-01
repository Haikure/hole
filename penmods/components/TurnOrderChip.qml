import QtQuick 2.12
import "."

Rectangle {
    id: chip

    property var owner: null
    property string token: ""
    property int position: 0
    property bool selected: false
    property bool interactive: true

    implicitWidth: 112
    implicitHeight: Theme.tapMin
    radius: Theme.radius
    color: selected ? Theme.fieldActive : Theme.surfaceRaised
    border.color: selected ? Theme.accent : Theme.border
    border.width: 1
    opacity: interactive && owner && owner.dragToken === token ? 0.3 : 1
    Accessible.role: Accessible.Button
    Accessible.name: (selected ? "移除 " : "启用 ") + (owner ? owner.typeLabel(token) : token)
    Accessible.onPressAction: if (owner && interactive) owner.toggleToken(token)

    Text {
        anchors.left: parent.left
        anchors.leftMargin: 7
        anchors.verticalCenter: parent.verticalCenter
        text: chip.selected ? String(chip.position) : "+"
        color: Theme.accent
        font.family: Theme.fontFamily
        font.pixelSize: Theme.fontLabel
    }

    Text {
        anchors.centerIn: parent
        text: chip.owner ? chip.owner.typeLabel(chip.token) : chip.token
        color: Theme.textPrimary
        font.family: Theme.fontFamily
        font.pixelSize: Theme.fontLabel
    }

    Text {
        anchors.right: parent.right
        anchors.rightMargin: 5
        anchors.verticalCenter: parent.verticalCenter
        text: "≡"
        color: Theme.textMuted
        font.pixelSize: Theme.fontLabel
    }

    MouseArea {
        id: touch
        anchors.fill: parent
        enabled: chip.interactive
        acceptedButtons: Qt.LeftButton
        pressAndHoldInterval: 450
        // A normal swipe still belongs to the settings Flickable. Only take
        // the grab after the long press has started a reorder operation.
        preventStealing: dragged
        property bool dragged: false

        onPressed: dragged = false
        onPressAndHold: {
            dragged = true
            var point = mapToItem(null, mouse.x, mouse.y)
            chip.owner.beginDrag(chip.token, point)
        }
        onPositionChanged: {
            if (dragged && pressed)
                chip.owner.moveDrag(mapToItem(null, mouse.x, mouse.y))
        }
        onReleased: {
            if (dragged) {
                chip.owner.moveDrag(mapToItem(null, mouse.x, mouse.y))
                chip.owner.finishDrag()
            }
        }
        onCanceled: {
            if (dragged) chip.owner.cancelDrag()
            dragged = false
        }
        onClicked: if (!dragged) chip.owner.toggleToken(chip.token)
    }
}
