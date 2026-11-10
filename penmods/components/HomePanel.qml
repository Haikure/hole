import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

ColumnLayout {
    id: panel
    property string stateTitle: "尚未连接"
    property string identity: "先设置房间与设备"
    property bool connected: false
    property bool requested: false
    property bool busy: false
    property var snapshot: ({})
    property string errorText: ""
    readonly property var peers: snapshot.peer_transports || []
    readonly property int onlinePeers: peers.filter(function(p) { return p.state === "active" || p.state === "switching" }).length
    readonly property int activeMappings: (snapshot.mappings || []).filter(function(m) { return m.state === "active" }).length
    signal toggleRequested()
    signal reloadRequested()
    signal navigate(string destination)
    implicitWidth: 258
    spacing: 6

    RowLayout {
        Layout.fillWidth: true; Layout.preferredHeight: 28
        Text { text: "HOLE"; color: Theme.textPrimary; font.pixelSize: 16; font.bold: true; font.letterSpacing: 2 }
        Item { Layout.fillWidth: true }
        ActionButton {
            objectName: "homeReload"
            Layout.preferredWidth: 88; Layout.preferredHeight: 28
            text: "重载核心"; iconName: "core"; variant: "link"
            enabled: !panel.busy
            onClicked: panel.reloadRequested()
        }
    }
    Rectangle {
        Layout.fillWidth: true; Layout.preferredHeight: 70
        radius: 10; color: panel.connected ? Theme.accentSoft : Theme.surface
        Behavior on color { ColorAnimation { duration: 180 } }
        RowLayout {
            anchors { left: parent.left; right: parent.right; top: parent.top; margins: 9 }
            spacing: 8
            ColumnLayout {
                Layout.fillWidth: true; spacing: 2
                Text { Layout.fillWidth: true; text: panel.stateTitle; color: Theme.textPrimary; font.family: Theme.fontFamily; font.pixelSize: 16; font.bold: true; elide: Text.ElideRight }
                Text { Layout.fillWidth: true; text: panel.identity; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 10; elide: Text.ElideRight }
            }
            ActionButton {
                objectName: "homePower"
                Layout.preferredWidth: 34; Layout.preferredHeight: 34
                iconName: "power"; variant: panel.requested ? "primary" : "secondary"
                enabled: !panel.busy
                Accessible.name: panel.requested ? "停止连接" : "启动连接"
                onClicked: panel.toggleRequested()
            }
        }
        RowLayout {
            anchors { left: parent.left; right: parent.right; bottom: parent.bottom; leftMargin: 10; rightMargin: 10; bottomMargin: 7 }
            spacing: 5
            NATIcon { name: "device"; tint: Theme.textSecondary; Layout.preferredWidth: 12; Layout.preferredHeight: 12 }
            Text { text: panel.onlinePeers + " 台在线"; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 10 }
            Item { Layout.fillWidth: true }
            NATIcon { name: "mapping"; tint: Theme.textSecondary; Layout.preferredWidth: 12; Layout.preferredHeight: 12 }
            Text { text: panel.activeMappings + " 项服务可用"; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 10 }
        }
    }
    RowLayout {
        objectName: "homeNavigation"
        Layout.fillWidth: true; spacing: 5
        Repeater {
            model: [{key: "devices", icon: "device", title: "设备"}, {key: "services", icon: "mapping", title: "服务"},
                {key: "network", icon: "network", title: "网络"}, {key: "settings", icon: "settings", title: "设置"}]
            delegate: Rectangle {
                objectName: "nav-" + modelData.key
                Layout.fillWidth: true; Layout.preferredWidth: 1; Layout.preferredHeight: 40
                radius: 8; color: tap.pressed ? Theme.fieldActive : Theme.surface
                scale: tap.pressed ? 0.96 : 1
                Behavior on scale { NumberAnimation { duration: 100; easing.type: Easing.OutCubic } }
                Behavior on color { ColorAnimation { duration: 100 } }
                NATIcon { name: modelData.icon; width: 17; height: 17; anchors.horizontalCenter: parent.horizontalCenter; y: 4 }
                Text { text: modelData.title; anchors.horizontalCenter: parent.horizontalCenter; y: 23; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 10 }
                Accessible.role: Accessible.Button
                Accessible.name: modelData.title
                Accessible.onPressAction: panel.navigate(modelData.key)
                MouseArea { id: tap; anchors.fill: parent; preventStealing: false; onClicked: panel.navigate(modelData.key) }
            }
        }
    }
    Text {
        Layout.fillWidth: true; visible: text.length > 0
        text: panel.errorText; color: Theme.error; font.family: Theme.fontFamily; font.pixelSize: Theme.fontLabel; wrapMode: Text.Wrap
    }
}
