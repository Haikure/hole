import QtQuick 2.12
import QtTest 1.2
import "../components"

TestCase {
    id: test
    name: "NATPunchExport"
    width: 320; height: 170; visible: true
    when: windowShown

    QtObject {
        id: mockPlugin
        property bool coreReady: true
        property bool reloading: false
        property bool natPunchProcessExportAvailable: true
        property int requests: 0
        property string transport: ""
        signal natPunchProcessExportReady(string requestId, string path)
        signal natPunchProcessExportFailed(string requestId, string message)
        function exportNATPunchProcess(id) { transport = id; requests++; return "export-" + requests }
    }
    QtObject { id: oldPlugin; property bool coreReady: true }
    Component { id: exportComponent; NATPunchExport { x: 54; y: 8; width: 258; transportId: "pair"; plugin: mockPlugin } }

    function init() {
        mockPlugin.coreReady = true; mockPlugin.reloading = false; mockPlugin.natPunchProcessExportAvailable = true
        mockPlugin.requests = 0; mockPlugin.transport = ""
    }
    function load() {
        var panel = createTemporaryObject(exportComponent, test)
        verify(panel !== null)
        wait(20)
        return panel
    }
    function click(panel) {
        var button = findChild(panel, "exportPunchProcess")
        verify(button !== null)
        mouseClick(button, button.width / 2, button.height / 2)
    }

    function test_exportFitsAndUsesCurrentDevice() {
        var panel = load()
        verify(panel.available)
        verify(panel.implicitHeight <= test.height - 16)
        click(panel)
        compare(mockPlugin.requests, 1); compare(mockPlugin.transport, "pair")
        verify(panel.exporting)
        click(panel)
        compare(mockPlugin.requests, 1)
        mockPlugin.natPunchProcessExportReady("other", "/tmp/other.json")
        verify(panel.exporting)
        var path = "/userdisk/PenMods/plugins/hole_plugin/exports/hole-nat-punch-20261123-000000-12345.json"
        mockPlugin.natPunchProcessExportReady("export-1", path)
        verify(!panel.exporting); verify(!panel.failed)
        verify(panel.message.indexOf(path) >= 0)
        tryVerify(function() { return findChild(panel, "punchExportNotice").visible })
        verify(panel.implicitHeight <= test.height - 16)
    }

    function test_failureAllowsRetry() {
        var panel = load()
        click(panel)
        mockPlugin.natPunchProcessExportFailed("other", "ignore")
        verify(panel.exporting)
        mockPlugin.natPunchProcessExportFailed("export-1", "存储空间不足")
        verify(!panel.exporting); verify(panel.failed)
        compare(panel.message, "存储空间不足")
        click(panel)
        compare(mockPlugin.requests, 2)
        verify(panel.exporting); verify(!panel.failed)
        compare(panel.message, "")
    }

    function test_oldCoreAndPluginOfferUpdateMessage() {
        mockPlugin.natPunchProcessExportAvailable = false
        var panel = load()
        verify(!panel.available)
        click(panel); compare(mockPlugin.requests, 0)
        verify(panel.notice.indexOf("hole-core") >= 0)
        panel.plugin = oldPlugin
        verify(!panel.available)
        verify(panel.notice.indexOf("插件包") >= 0)
    }

    function test_coreReloadDisablesExport() {
        mockPlugin.reloading = true
        var panel = load()
        click(panel); compare(mockPlugin.requests, 0)
        mockPlugin.reloading = false
        panel.transportId = "other-pair"
        click(panel); compare(mockPlugin.transport, "other-pair")
    }
}
