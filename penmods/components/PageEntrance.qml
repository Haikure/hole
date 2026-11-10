import QtQuick 2.12

NumberAnimation {
    property: "opacity"
    from: 0; to: 1; duration: 150
    easing.type: Easing.OutCubic
    Component.onCompleted: start()
}
