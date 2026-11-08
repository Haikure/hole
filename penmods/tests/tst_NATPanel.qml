import QtQuick 2.12
import QtQuick.Layouts 1.12
import QtTest 1.2
import "../components"
import "../components/NATTime.js" as NATTime

TestCase {
    id: scene
    name: "NATPanel"
    width: 250; height: 170
    visible: true
    when: windowShown
    Flickable {
        id: scroll
        anchors.fill: parent
        contentWidth: width; contentHeight: panel.height
        clip: true
        NATPanel { id: panel; width: scroll.width }
    }
    SignalSpy { id: startSpy; target: panel; signalName: "startRequested" }
    SignalSpy { id: cancelSpy; target: panel; signalName: "cancelRequested" }
    function test_systemTimeFormatting() {
        var local = new Date(2026, 10, 8, 7, 59, 59, 123)
        compare(NATTime.format(local.toISOString()), "2026-11-08 07:59:59")
        compare(NATTime.format(local.toISOString().replace(".123", ".123456789")), "2026-11-08 07:59:59")
        compare(NATTime.format("2026-11-07T23:59:59Z"), NATTime.format("2026-11-08T05:44:59+05:45"))
        compare(NATTime.format(""), "")
        compare(NATTime.format("暂无"), "暂无")
        compare(NATTime.format("2001:db8::1"), "2001:db8::1")
    }
    function test_actionsAndDetails() {
        var action = findChild(panel, "natAction")
        verify(action !== null)
        action.clicked()
        compare(startSpy.count, 1)
        panel.report = {state: "probing", title: "正在检测网络", sections: []}
        action.clicked()
        compare(cancelSpy.count, 1)
        panel.report = {state: "complete", tone: "yellow", badge: "可能可预测", title: "对称型", summary: "端口随目标变化", highlights: [
            {icon: "mapping", label: "映射行为", value: "端点相关"}, {icon: "shield", label: "过滤行为", value: "地址与端口相关"},
            {icon: "trend", label: "端口规律", value: "可能递增"}, {icon: "steps", label: "端口步长", value: "2"},
            {icon: "samples", label: "有效样本", value: "18"}, {icon: "network", label: "不同公网映射", value: "6"}
        ], sections: [
            {title: "映射与过滤", fields: [{label: "过滤行为", value: "未知 / 尚未确认", detail: "未知不等于无限制"}]},
            {title: "检测时的网络", fields: [{label: "IPv6", value: "2001:db8:1234:5678:abcd:ef01:2345:6789"}]}
        ]}
        wait(100)
        verify(panel.implicitHeight > 170)
        verify(scroll.contentHeight > scroll.height)
        scroll.contentY = 120
        compare(scroll.contentY, 120)
        compare(panel.width, 250)
        verify(!panel.busy)
        compare(panel.ink, Theme.natYellow)
        compare(findChild(panel, "natTiles").columns, 2)
        scene.width = 190
        wait(50)
        compare(findChild(panel, "natTiles").columns, 1)
    }
}
