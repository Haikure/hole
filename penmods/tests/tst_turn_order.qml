import QtQuick 2.12
import QtTest 1.2
import "../components"

TestCase {
    id: testCase
    name: "PenModsTurnOrder"
    when: windowShown
    visible: true
    width: 320
    height: 600

    Flickable {
        id: scroll
        width: 242
        height: 560
        contentWidth: width
        contentHeight: board.height + 40
        clip: true
        interactive: !board.dragging
        boundsBehavior: Flickable.StopAtBounds

        TurnOrderBoard {
            id: board
            y: 20
            width: scroll.width
            height: implicitHeight
            scrollView: scroll
            onEdited: order = nextOrder
        }
    }

    SignalSpy { id: edits; target: board; signalName: "edited" }

    function init() {
        board.cancelDrag()
        board.order = ["udp", "tcp", "tls"]
        scroll.height = 560
        scroll.cancelFlick()
        scroll.contentY = 0
        wait(50)
        edits.clear()
    }

    function chip(token, selected) {
        var item = findChild(board, (selected ? "selected_" : "available_") + token)
        verify(item !== null, "missing chip " + token)
        return item
    }

    function pressAndHold(item) {
        mousePress(item, item.width / 2, item.height / 2)
        tryCompare(board, "dragging", true, 1000)
    }

    function releaseAt(item, x, y) {
        mouseMove(item, x, y, 20)
        mouseRelease(item, x, y)
        wait(40)
        compare(board.dragging, false)
    }

    function test_labelsAndTapToggle() {
        compare(board.types.map(function(token) { return board.typeLabel(token) }).join(","),
                "UDP,TCP,TLS")
        var tcp = chip("tcp", true)
        mouseClick(tcp, tcp.width / 2, tcp.height / 2)
        compare(board.order, ["udp", "tls"])
        wait(30)
        tcp = chip("tcp", false)
        mouseClick(tcp, tcp.width / 2, tcp.height / 2)
        compare(board.order, ["udp", "tls", "tcp"])
        compare(edits.count, 2)
    }

    function test_dragAcrossRowsCommitsOnlyOnRelease() {
        var last = chip("tls", true)
        var first = chip("udp", true)
        var target = first.mapToItem(scroll, 2, first.height / 2)
        pressAndHold(last)
        mouseMove(scroll, target.x, target.y, 20)
        compare(board.order, ["udp", "tcp", "tls"])
        compare(edits.count, 0)
        releaseAt(scroll, target.x, target.y)
        compare(board.order, ["tls", "udp", "tcp"])
        compare(edits.count, 1)
    }

    function test_dragRightOnSameRow() {
        var first = chip("udp", true)
        var second = chip("tcp", true)
        var target = second.mapToItem(scroll, second.width - 2, second.height / 2)
        pressAndHold(first)
        releaseAt(scroll, target.x, target.y)
        compare(board.order, ["tcp", "udp", "tls"])
        compare(edits.count, 1)
    }

    function test_dragAcrossDividerDisables() {
        var first = chip("udp", true)
        var divider = findChild(board, "turnOrderDivider")
        var target = divider.mapToItem(scroll, 30, 25)
        pressAndHold(first)
        releaseAt(scroll, target.x, target.y)
        compare(board.order, ["tcp", "tls"])
        compare(edits.count, 1)
    }

    function test_dragAvailableIntoEmptyOrder() {
        board.order = []
        wait(30)
        var tls = chip("tls", false)
        var target = board.mapToItem(scroll, 20, 35)
        pressAndHold(tls)
        releaseAt(scroll, target.x, target.y)
        compare(board.order, ["tls"])
        compare(edits.count, 1)
    }

    function test_cancelPreservesOrder() {
        var first = chip("udp", true)
        pressAndHold(first)
        board.cancelDrag()
        mouseRelease(first, first.width / 2, first.height / 2)
        wait(30)
        compare(board.order, ["udp", "tcp", "tls"])
        compare(edits.count, 0)
        compare(scroll.interactive, true)
    }

    function test_smallViewportAutoScrollsDuringDrag() {
        scroll.height = 170
        var first = chip("udp", true)
        pressAndHold(first)
        compare(scroll.interactive, false)
        mouseMove(scroll, 50, 165, 20)
        tryVerify(function() { return scroll.contentY > 0 }, 1000)
        board.cancelDrag()
        mouseRelease(scroll, 50, 165)
        compare(edits.count, 0)
        compare(scroll.interactive, true)
    }

    function test_shortSwipeScrollsWithoutEditing() {
        scroll.height = 170
        var first = chip("udp", true)
        var start = first.mapToItem(scroll, first.width / 2, first.height / 2)
        mousePress(scroll, start.x, start.y)
        for (var i = 1; i <= 5; ++i) mouseMove(scroll, start.x, start.y - i * 9, 20)
        mouseRelease(scroll, start.x, start.y - 45)
        compare(board.dragging, false)
        compare(edits.count, 0)
        verify(scroll.contentY > 0)
    }
}
