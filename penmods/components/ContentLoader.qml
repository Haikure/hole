import QtQuick 2.12

// Measure at the available width before a collapsed layout assigns geometry.
// The loaded item always keeps its natural height, outside layout management.
Item {
    id: content
    property bool active: true
    property real contentWidth: width
    property alias item: loader.item
    default property alias sourceComponent: loader.sourceComponent
    implicitWidth: 1
    implicitHeight: loader.item ? loader.height : 0

    Loader {
        id: loader
        width: Math.max(0, content.contentWidth)
        active: content.active && width > 0
    }
}
