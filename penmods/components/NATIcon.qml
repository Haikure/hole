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
        if (name === "power") {
            c.moveTo(12, 2); c.lineTo(12, 11)
            c.moveTo(6, 5)
            c.bezierCurveTo(0, 10, 3, 21, 12, 21)
            c.bezierCurveTo(21, 21, 24, 10, 18, 5)
        } else if (name === "core") {
            c.rect(6, 6, 12, 12); c.rect(10, 10, 4, 4)
            for (var pin = 8; pin <= 16; pin += 4) {
                c.moveTo(pin, 3); c.lineTo(pin, 6); c.moveTo(pin, 18); c.lineTo(pin, 21)
                c.moveTo(3, pin); c.lineTo(6, pin); c.moveTo(18, pin); c.lineTo(21, pin)
            }
        } else if (name === "settings") {
            c.moveTo(4, 6); c.lineTo(20, 6); c.moveTo(4, 12); c.lineTo(20, 12); c.moveTo(4, 18); c.lineTo(20, 18)
            c.moveTo(8, 3); c.lineTo(8, 9); c.moveTo(16, 9); c.lineTo(16, 15); c.moveTo(10, 15); c.lineTo(10, 21)
        } else if (name === "device") {
            c.rect(3, 4, 18, 12); c.moveTo(8, 21); c.lineTo(16, 21); c.moveTo(12, 16); c.lineTo(12, 21)
        } else if (name === "chevron") {
            c.moveTo(9, 5); c.lineTo(16, 12); c.lineTo(9, 19)
        } else if (name === "plus") {
            c.moveTo(12, 5); c.lineTo(12, 19); c.moveTo(5, 12); c.lineTo(19, 12)
        } else if (name === "close") {
            c.moveTo(6, 6); c.lineTo(18, 18); c.moveTo(18, 6); c.lineTo(6, 18)
        } else if (name === "refresh") {
            c.arc(12, 12, 8, 0.4, 5.6); c.moveTo(19, 3); c.lineTo(19, 8); c.lineTo(14, 8)
        } else if (name === "upload" || name === "download") {
            if (name === "download") { c.translate(24, 24); c.rotate(Math.PI) }
            c.moveTo(12, 20); c.lineTo(12, 4); c.moveTo(6, 10); c.lineTo(12, 4); c.lineTo(18, 10)
        } else if (name === "latency") {
            c.arc(12, 12, 9, 0, Math.PI * 2); c.moveTo(12, 6); c.lineTo(12, 12); c.lineTo(16, 14)
        } else if (name === "activity") {
            c.moveTo(2, 12); c.lineTo(6, 12); c.lineTo(9, 4); c.lineTo(14, 20); c.lineTo(17, 12); c.lineTo(22, 12)
        } else if (name === "trash") {
            c.moveTo(4, 6); c.lineTo(20, 6); c.moveTo(9, 3); c.lineTo(15, 3)
            c.moveTo(6, 6); c.lineTo(7, 21); c.lineTo(17, 21); c.lineTo(18, 6)
            c.moveTo(10, 10); c.lineTo(10, 17); c.moveTo(14, 10); c.lineTo(14, 17)
        } else if (name === "mapping" || name === "映射与过滤") {
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
