import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

ColumnLayout {
    id: samples
    property var view: ({})
    property string sampleIp: ""
    property string sampleRole: "all"
    property string selectedSequence: ""
    property bool recordsOpen: false
    property bool showSteps: false
    property var rowData: ({})
    readonly property var points: view.samplePoints || []
    readonly property var selectedPoint: selectedSample()
    signal ipChosen(string value)
    signal roleChosen(string value)
    signal toggleRequested(var value)
    signal socketRequested(string socketId, bool sample)
    signal transactionRequested(string transactionId, bool sample)
    spacing: 7
    ListModel { id: rows }
    onViewChanged: syncRows()
    onShowStepsChanged: syncRows()
    Component.onCompleted: syncRows()
    function selectedSample() {
        for (var i = 0; i < points.length; ++i) if (points[i].sequence === selectedSequence) return points[i]
        return null
    }
    function syncRows() {
        var entries = showSteps ? view.sampleSteps || [] : view.sampleRows || [], next = {}
        for (var i = 0; i < entries.length; ++i) {
            var key = entries[i].key, found = -1
            next[key] = entries[i]
            for (var j = i; j < rows.count; ++j) if (rows.get(j).rowKey === key) { found = j; break }
            if (found < 0) rows.insert(i, {rowKey: key})
            else if (found !== i) rows.move(found, i, 1)
        }
        if (rows.count > entries.length) rows.remove(entries.length, rows.count - entries.length)
        rowData = next
    }
    NATPunchChips { Layout.fillWidth: true; options: [{value: "all", label: "全部用途"}, {value: "nat_base", label: "基础映射"}, {value: "nat_validation", label: "跨 socket"}, {value: "nat_filtering", label: "过滤探测"}]; selectedValue: samples.sampleRole; onChosen: samples.roleChosen(value) }
    NATPunchChips { Layout.fillWidth: true; visible: options.length > 0; options: samples.view.sampleIps || []; selectedValue: samples.sampleIp || (options.length ? options[0].value : ""); onChosen: samples.ipChosen(value) }
    Text { Layout.fillWidth: true; text: samples.view.sampleRange || "暂无对应采样"; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 10; wrapMode: Text.Wrap }
    Canvas {
        id: plot
        property var points: samples.points
        Layout.fillWidth: true; Layout.preferredHeight: 88
        onPointsChanged: requestPaint()
        onWidthChanged: requestPaint()
        onHeightChanged: requestPaint()
        Connections { target: samples; function onSelectedSequenceChanged() { plot.requestPaint() } }
        onPaint: {
            var c = getContext("2d"), left = 8, top = 8, spanX = Math.max(1, width - 16), spanY = Math.max(1, height - 20)
            c.reset(); c.strokeStyle = Theme.border; c.lineWidth = 1
            for (var line = 0; line < 3; ++line) { var y = top + spanY * line / 2; c.beginPath(); c.moveTo(left, y); c.lineTo(left + spanX, y); c.stroke() }
            if (points.length === 0) return
            c.strokeStyle = Theme.accent; c.lineWidth = 1.5; c.beginPath()
            for (var i = 0; i < points.length; ++i) { var x = left + points[i].x * spanX, py = top + (1 - points[i].y) * spanY; if (i === 0) c.moveTo(x, py); else c.lineTo(x, py) }
            c.stroke()
            for (var j = 0; j < points.length; ++j) { c.fillStyle = points[j].sequence === samples.selectedSequence ? Theme.natYellow : Theme.accent; c.beginPath(); c.arc(left + points[j].x * spanX, top + (1 - points[j].y) * spanY, points[j].sequence === samples.selectedSequence ? 4 : 2.5, 0, Math.PI * 2); c.fill() }
        }
        MouseArea {
            anchors.fill: parent; enabled: samples.points.length > 0; preventStealing: false
            onClicked: {
                var fraction = Math.max(0, Math.min(1, (mouse.x - 8) / Math.max(1, width - 16)))
                samples.selectedSequence = samples.points[Math.round(fraction * (samples.points.length - 1))].sequence
            }
        }
    }
    Text { Layout.fillWidth: true; visible: !samples.selectedPoint; text: "点按样本查看端口与相邻差值"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9 }
    NATPunchFacts {
        Layout.fillWidth: true; visible: !!samples.selectedPoint; title: "样本 #" + (samples.selectedPoint ? samples.selectedPoint.sequence : ""); iconName: "trend"; tone: "blue"
        lines: samples.selectedPoint ? [{label: "socket / 时间", text: samples.selectedPoint.socket + " · " + samples.selectedPoint.time}, {label: "观测映射", text: samples.selectedPoint.mapped}, {label: "相邻端口差", text: samples.selectedPoint.delta}, {label: "STUN 目标", text: samples.selectedPoint.server || "待观测"}] : []
    }
    ActionButton { Layout.fillWidth: true; visible: !!samples.selectedPoint && !!samples.selectedPoint.transaction; text: "查看此 STUN 事务"; iconName: "samples"; variant: "link"; onClicked: samples.transactionRequested(samples.selectedPoint.transaction, true) }
    ActionButton { Layout.fillWidth: true; text: samples.recordsOpen ? "收起采样记录" : "展开采样记录"; iconName: "samples"; variant: "link"; onClicked: samples.recordsOpen = !samples.recordsOpen }
    NATPunchChips { Layout.fillWidth: true; visible: samples.recordsOpen; options: [{value: "mapped", label: "最近映射"}, {value: "steps", label: "采样步骤"}]; selectedValue: samples.showSteps ? "steps" : "mapped"; onChosen: samples.showSteps = value === "steps" }
    Loader {
        Layout.fillWidth: true; Layout.preferredHeight: item ? item.implicitHeight : 0; active: samples.recordsOpen; visible: active
        sourceComponent: ColumnLayout {
            spacing: 7
            Repeater {
                model: rows
                NATPunchRow {
                    Layout.fillWidth: true; record: samples.rowData[model.rowKey] || ({})
                    onToggleRequested: samples.toggleRequested(value)
                    onSocketRequested: samples.socketRequested(socketId, sample)
                    onTransactionRequested: samples.transactionRequested(transactionId, sample)
                }
            }
        }
    }
}
