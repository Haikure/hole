import QtQuick 2.12
import "."

// Label + value row that hands editing to the host input page.
//
// The component never writes to its own `value`: it only reports the committed
// text through `edited`. The owner updates the model and the declarative
// binding on `value` pushes the result back, so bindings survive editing.
Rectangle {
    id: field

    property string label: ""
    property string value: ""
    property string placeholder: ""
    property bool secret: false
    property bool numeric: false
    property bool invalid: false
    property var hostKeyboard: null

    signal edited(string nextValue)

    property bool lit: false

    implicitWidth: Theme.contentWidth
    implicitHeight: Theme.controlHeight
    radius: Theme.radius
    color: field.highlighted ? Theme.fieldActive : Theme.field
    border.width: field.invalid || field.highlighted ? 1 : 0
    border.color: field.invalid ? Theme.warning : Theme.accent
    readonly property bool highlighted: tap.pressed || field.lit

    Behavior on color { ColorAnimation { duration: 90 } }
    Behavior on border.color { ColorAnimation { duration: 90 } }

    function masked(text) {
        return new Array(Math.min(text.length, 64) + 1).join("•")
    }

    function requestInput() {
        field.lit = true
        highlightTimer.restart()
        if (!field.hostKeyboard) {
            console.warn("hole_plugin: 输入框没有可用的宿主输入页")
            return
        }
        field.hostKeyboard.open(field.value, field.placeholder || field.label, function (text) {
            var next = String(text)
            if (field.numeric) next = next.replace(/[^0-9]/g, "")
            field.edited(next)
        })
    }

    Text {
        id: labelText
        anchors {
            left: parent.left; right: parent.right; top: parent.top
            leftMargin: 8; rightMargin: 8; topMargin: 4
        }
        text: field.label
        visible: text.length > 0
        color: field.invalid ? Theme.warning : Theme.textSecondary
        font.family: Theme.fontFamily
        font.pixelSize: Theme.fontCaption
        elide: Text.ElideRight
    }

    Text {
        anchors {
            left: parent.left; right: parent.right
            top: labelText.visible ? labelText.bottom : parent.top
            bottom: parent.bottom
            leftMargin: 8; rightMargin: 8; topMargin: 1; bottomMargin: 4
        }
        text: field.value.length === 0 && field.placeholder.length > 0
              ? field.placeholder
              : (field.secret ? field.masked(field.value) : field.value)
        color: field.value.length === 0 ? Theme.textPlaceholder : Theme.textPrimary
        font.family: Theme.fontFamily
        font.pixelSize: Theme.fontValue
        elide: Text.ElideRight
        verticalAlignment: Text.AlignVCenter
    }

    // MouseArea leaves preventStealing disabled so the parent Flickable can
    // take over when a finger moves. A tap still opens the host input page,
    // while a drag that starts on the field scrolls the form normally.
    MouseArea {
        id: tap
        anchors.fill: parent
        acceptedButtons: Qt.LeftButton
        preventStealing: false
        onPressed: field.lit = true
        onClicked: field.requestInput()
        onReleased: if (!highlightTimer.running) field.lit = false
        onCanceled: if (!highlightTimer.running) field.lit = false
    }

    // Keeps the pressed state visible for at least 150ms after a tap.
    Timer {
        id: highlightTimer
        interval: 150
        onTriggered: if (!tap.pressed) field.lit = false
    }
}
