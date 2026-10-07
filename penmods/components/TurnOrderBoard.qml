import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

Item {
    id: board

    property var order: []
    property var scrollView: null
    readonly property var types: ["udp", "tcp", "tls"]
    readonly property var available: types.filter(function(token) { return board.order.indexOf(token) < 0 })
    readonly property bool dragging: dragToken.length > 0
    readonly property int chipSpacing: 6
    readonly property real chipWidth: Math.min(128, (width - 6) / 2)
    property string dragToken: ""
    property var dragOrder: []
    property var dragBounds: ({})
    property point scenePoint: Qt.point(0, 0)
    property point dragPoint: Qt.point(0, 0)
    property int insertIndex: 0
    property bool dropSelected: true
    signal edited(var nextOrder)

    implicitWidth: Theme.contentWidth
    implicitHeight: layout.implicitHeight

    function typeLabel(token) {
        var labels = {udp: "UDP", tcp: "TCP", tls: "TLS"}
        return labels[token] || token
    }

    function flowHeight(count) {
        var rows = Math.ceil(count / 2)
        return rows > 0 ? rows * Theme.tapMin + (rows - 1) * chipSpacing : 0
    }

    function commit(next) {
        if (JSON.stringify(next) !== JSON.stringify(order)) board.edited(next)
    }

    function toggleToken(token) {
        if (dragging) return
        var next = order.slice()
        var index = next.indexOf(token)
        if (index < 0) next.push(token)
        else next.splice(index, 1)
        commit(next)
    }

    function beginDrag(token, point) {
        if (dragging) return
        dragOrder = order.slice()
        var bounds = {}
        for (var i = 0; i < selectedItems.count; ++i) {
            var chip = selectedItems.itemAt(i)
            var origin = chip.mapToItem(board, 0, 0)
            bounds[chip.token] = {x: origin.x, y: origin.y, width: chip.width, height: chip.height}
        }
        dragBounds = bounds
        if (scrollView) scrollView.cancelFlick()
        dragToken = token
        moveDrag(point)
    }

    function moveDrag(point) {
        if (!dragging) return
        scenePoint = point
        dragPoint = mapFromItem(null, point.x, point.y)
        dropSelected = dragPoint.y < divider.y + divider.height / 2
        var index = 0
        for (var i = 0; i < dragOrder.length; ++i) {
            var token = dragOrder[i]
            if (token === dragToken) continue
            var bounds = dragBounds[token]
            if (!bounds) continue
            var centerX = bounds.x + bounds.width / 2
            var centerY = bounds.y + bounds.height / 2
            var slack = bounds.height / 2
            if (centerY < dragPoint.y - slack
                    || (Math.abs(centerY - dragPoint.y) <= slack && centerX < dragPoint.x)) index++
        }
        insertIndex = index
    }

    function finishDrag() {
        if (!dragging) return
        var token = dragToken
        var next = dragOrder.filter(function(item) { return item !== token })
        if (dropSelected) next.splice(Math.min(insertIndex, next.length), 0, token)
        cancelDrag()
        // Let MouseArea finish its release/click delivery before a new model
        // destroys the source delegate. A long press must not also toggle it.
        Qt.callLater(function() { board.commit(next) })
    }

    function cancelDrag() {
        dragToken = ""
        dragBounds = ({})
    }

    function markerRect() {
        var base = dragOrder.filter(function(token) { return token !== board.dragToken })
        if (base.length === 0) return {x: 0, y: selectedFlow.y, height: Theme.tapMin}
        var target = insertIndex < base.length ? base[insertIndex] : base[base.length - 1]
        var bounds = dragBounds[target]
        return {x: bounds.x + (insertIndex < base.length ? 0 : bounds.width), y: bounds.y, height: bounds.height}
    }

    onOrderChanged: if (dragging) cancelDrag()
    onVisibleChanged: if (!visible) cancelDrag()

    Column {
        id: layout
        width: parent.width
        spacing: 6

        RowLayout {
            width: parent.width
            Text {
                text: "尝试顺序"
                color: Theme.textPrimary
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontLabel
                Layout.fillWidth: true
            }
            Text {
                text: board.order.length === 0 ? "默认顺序" : board.order.length + " 种"
                color: Theme.textMuted
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontCaption
            }
        }

        Flow {
            id: selectedFlow
            width: parent.width
            spacing: board.chipSpacing
            height: board.flowHeight(Math.max(1, board.order.length))
            Repeater {
                id: selectedItems
                model: board.order
                delegate: TurnOrderChip {
                    objectName: "selected_" + modelData
                    owner: board
                    token: modelData
                    position: index + 1
                    selected: true
                    width: board.chipWidth
                    height: implicitHeight
                }
            }
            Text {
                width: parent.width
                height: Theme.tapMin
                visible: board.order.length === 0
                text: "留空使用默认顺序；点按下方类型可添加"
                color: Theme.textMuted
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontCaption
                verticalAlignment: Text.AlignVCenter
                wrapMode: Text.WordWrap
            }
        }

        Rectangle {
            id: divider
            objectName: "turnOrderDivider"
            width: parent.width
            height: 1
            color: Theme.border
        }

        RowLayout {
            width: parent.width
            Text {
                text: "可添加"
                color: Theme.textPrimary
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontLabel
                Layout.fillWidth: true
            }
            Text {
                text: board.available.length + " 种"
                color: Theme.textMuted
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontCaption
            }
        }

        Flow {
            width: parent.width
            spacing: board.chipSpacing
            height: board.flowHeight(board.available.length)
            Repeater {
                model: board.available
                delegate: TurnOrderChip {
                    objectName: "available_" + modelData
                    owner: board
                    token: modelData
                    width: board.chipWidth
                    height: implicitHeight
                }
            }
        }

        Text {
            width: parent.width
            text: "长按拖动可调整顺序或跨区启用、停用；点按可启用或停用。"
            color: Theme.textMuted
            font.family: Theme.fontFamily
            font.pixelSize: Theme.fontCaption
            wrapMode: Text.WordWrap
        }
    }

    Rectangle {
        property var bounds: board.dragging ? board.markerRect() : ({x: 0, y: 0, height: 0})
        visible: board.dragging && board.dropSelected
        x: bounds.x - 1
        y: bounds.y
        width: 3
        height: bounds.height
        radius: 1
        color: Theme.accent
        z: 1
    }

    TurnOrderChip {
        owner: board
        token: board.dragToken
        position: board.insertIndex + 1
        selected: board.dropSelected
        interactive: false
        visible: board.dragging
        x: Math.max(0, Math.min(board.width - width, board.dragPoint.x - width / 2))
        y: board.dragPoint.y - height / 2
        width: board.chipWidth
        height: implicitHeight
        z: 2
    }

    // The board can exceed the pen's viewport. Keep the held chip reachable
    // while crossing the divider, without turning a short swipe into a drag.
    Timer {
        interval: 30
        repeat: true
        running: board.dragging && board.scrollView !== null
        onTriggered: {
            var view = board.scrollView
            var point = view.mapFromItem(null, board.scenePoint.x, board.scenePoint.y)
            var edge = Math.min(28, view.height / 4)
            var step = point.y < edge ? -6 : point.y > view.height - edge ? 6 : 0
            if (step === 0) return
            view.contentY = Math.max(view.originY, Math.min(view.originY + Math.max(0, view.contentHeight - view.height), view.contentY + step))
            board.moveDrag(board.scenePoint)
        }
    }
}
