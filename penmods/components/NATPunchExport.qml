import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

ColumnLayout {
    id: panel
    objectName: "natPunchExport"
    spacing: 5
    property var plugin: null
    property string transportId: ""
    property string requestId: ""
    property string message: ""
    property bool failed: false
    readonly property bool exporting: requestId.length > 0
    readonly property bool available: !!plugin && typeof plugin.exportNATPunchProcess === "function" && !!plugin.natPunchProcessExportAvailable
    readonly property string notice: message.length > 0 ? message
        : !plugin || !plugin.coreReady ? ""
        : typeof plugin.exportNATPunchProcess !== "function" ? "更新插件包后可导出记录"
        : !available ? "更新 hole-core 后可导出记录" : ""

    function exportRecords() {
        if (!available || exporting || !transportId || plugin.reloading) return
        failed = false; message = ""
        requestId = plugin.exportNATPunchProcess(transportId)
        if (!requestId) { failed = true; message = "导出请求未提交，请稍后重试" }
    }

    ActionButton {
        objectName: "exportPunchProcess"
        Layout.fillWidth: true
        text: panel.exporting ? "正在导出…" : "导出完整记录"
        iconName: "download"
        enabled: panel.available && !panel.exporting && panel.transportId.length > 0 && !panel.plugin.reloading
        onClicked: panel.exportRecords()
    }
    Text {
        objectName: "punchExportNotice"
        Layout.fillWidth: true
        visible: panel.notice.length > 0
        text: panel.notice
        color: panel.failed ? Theme.natRed : Theme.textMuted
        font.family: Theme.fontFamily
        font.pixelSize: 10
        wrapMode: Text.WrapAnywhere
    }
    Connections {
        target: panel.plugin
        ignoreUnknownSignals: true
        function onNatPunchProcessExportReady(id, path) {
            if (id !== panel.requestId) return
            panel.requestId = ""; panel.failed = false
            panel.message = "已导出至\n" + path
        }
        function onNatPunchProcessExportFailed(id, message) {
            if (id !== panel.requestId) return
            panel.requestId = ""; panel.failed = true; panel.message = message
        }
    }
}
