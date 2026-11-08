import QtQuick 2.12
import "."

Canvas {
    id: icon
    property string name: "network"
    property color tint: Theme.accent
    implicitWidth: 20; implicitHeight: 20
    onNameChanged: requestPaint()
    onTintChanged: requestPaint()
    onWidthChanged: requestPaint()
    onHeightChanged: requestPaint()
    onPaint: {
        var c = getContext("2d")
        c.reset(); c.scale(width / 24, height / 24)
        c.strokeStyle = tint; c.lineWidth = 1.8; c.lineCap = "round"; c.lineJoin = "round"
        c.beginPath()
        if (name === "mapping" || name === "映射与过滤") {
            c.moveTo(3, 7); c.lineTo(19, 7); c.lineTo(15, 3)
            c.moveTo(19, 7); c.lineTo(15, 11)
            c.moveTo(21, 17); c.lineTo(5, 17); c.lineTo(9, 13)
            c.moveTo(5, 17); c.lineTo(9, 21)
        } else if (name === "shield") {
            c.moveTo(12, 3); c.lineTo(20, 6); c.lineTo(20, 12)
            c.quadraticCurveTo(20, 17, 12, 21); c.quadraticCurveTo(4, 17, 4, 12); c.lineTo(4, 6); c.closePath()
            c.moveTo(8, 12); c.lineTo(11, 15); c.lineTo(16, 9)
        } else if (name === "trend" || name === "端口特征") {
            c.moveTo(3, 17); c.lineTo(9, 11); c.lineTo(13, 14); c.lineTo(21, 5)
            c.moveTo(15, 5); c.lineTo(21, 5); c.lineTo(21, 11)
        } else if (name === "steps") {
            c.moveTo(3, 20); c.lineTo(9, 20); c.lineTo(9, 14); c.lineTo(15, 14); c.lineTo(15, 8); c.lineTo(21, 8); c.lineTo(21, 3)
        } else if (name === "samples" || name === "检测记录") {
            c.rect(5, 3, 14, 18)
            c.moveTo(9, 8); c.lineTo(15, 8); c.moveTo(9, 12); c.lineTo(15, 12); c.moveTo(9, 16); c.lineTo(13, 16)
        } else {
            c.rect(9, 2, 6, 5); c.rect(2, 17, 6, 5); c.rect(16, 17, 6, 5)
            c.moveTo(12, 7); c.lineTo(12, 12); c.moveTo(5, 17); c.lineTo(5, 12); c.lineTo(19, 12); c.lineTo(19, 17)
        }
        c.stroke()
    }
}
