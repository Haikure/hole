import QtQuick 2.12

// Keep content alive through the closing animation, then release its bindings.
// Animate only the expansion factor, so polling never animates numeric updates.
Item {
    id: reveal
    property bool expanded: false
    property real progress: expanded ? 1 : 0
    default property alias content: loader.sourceComponent
    implicitHeight: loader.item ? loader.item.height * progress : 0
    visible: expanded || progress > 0
    clip: true
    Behavior on progress { NumberAnimation { duration: 160; easing.type: Easing.OutCubic } }
    Loader {
        id: loader
        width: parent.width
        active: reveal.expanded || reveal.progress > 0
        opacity: reveal.progress
    }
}
