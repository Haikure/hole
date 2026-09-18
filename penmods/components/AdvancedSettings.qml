import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

// The advanced form is kept out of Main.qml so the page remains a small
// controller instead of becoming another configuration implementation.
Section {
    id: settings

    property var form: null
    property var hostKeyboard: null

    title: "高级连接参数"
    subtitle: form && form.preferred === "ipv6" ? "IPv6" : "ICE / TURN"
    collapsible: true

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
        hostKeyboard: settings.hostKeyboard
        label: "STUN 服务器（逗号或换行分隔）"
        value: settings.form ? settings.form.stunUrls : ""
        placeholder: "留空仅使用本地候选"
        onEdited: function(nextValue) {
            settings.form.stunUrls = nextValue
            settings.form.syncConfig()
        }
    }

    ChoiceRow {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
        title: "TURN 模式"
        summary: "直连失败后的中继方式"
        options: [
            {value: "worker", label: "协调服务"},
            {value: "manual", label: "手动"},
            {value: "off", label: "关闭"}
        ]
        selected: settings.form ? settings.form.turnMode : "off"
        onChosen: function(nextValue) {
            settings.form.turnMode = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
                 && settings.form.turnMode === "manual"
        hostKeyboard: settings.hostKeyboard
        label: "TURN 地址（逗号或换行分隔）"
        value: settings.form ? settings.form.turnUrls : ""
        invalid: settings.form && settings.form.parseList(settings.form.turnUrls).length === 0
        onEdited: function(nextValue) {
            settings.form.turnUrls = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
                 && settings.form.turnMode === "manual"
        hostKeyboard: settings.hostKeyboard
        label: "TURN 用户名"
        value: settings.form ? settings.form.turnUsername : ""
        onEdited: function(nextValue) {
            settings.form.turnUsername = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
                 && settings.form.turnMode === "manual"
        hostKeyboard: settings.hostKeyboard
        label: "TURN 凭据"
        value: settings.form ? settings.form.turnCredential : ""
        secret: true
        onEdited: function(nextValue) {
            settings.form.turnCredential = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
        hostKeyboard: settings.hostKeyboard
        label: "直连探测时间"
        value: settings.form ? settings.form.directProbeTimeout : ""
        placeholder: "3s"
        invalid: settings.form && !settings.form.isDurationBetween(settings.form.directProbeTimeout, 100, 120000)
        onEdited: function(nextValue) {
            settings.form.directProbeTimeout = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
        hostKeyboard: settings.hostKeyboard
        label: "候选收集时间"
        value: settings.form ? settings.form.gatherTimeout : ""
        placeholder: "6s"
        invalid: settings.form && !settings.form.isDurationBetween(settings.form.gatherTimeout, 100, 120000)
        onEdited: function(nextValue) {
            settings.form.gatherTimeout = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
        hostKeyboard: settings.hostKeyboard
        label: "连通性检查时间"
        value: settings.form ? settings.form.connectivityTimeout : ""
        placeholder: "10s"
        invalid: settings.form && !settings.form.isDurationBetween(settings.form.connectivityTimeout, 100, 120000)
        onEdited: function(nextValue) {
            settings.form.connectivityTimeout = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
        hostKeyboard: settings.hostKeyboard
        label: "最大重试间隔"
        value: settings.form ? settings.form.retryMaxDelay : ""
        placeholder: "15s"
        invalid: settings.form && !settings.form.isDurationBetween(settings.form.retryMaxDelay, 100, 120000)
        onEdited: function(nextValue) {
            settings.form.retryMaxDelay = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
                 && settings.form.turnMode !== "off"
        hostKeyboard: settings.hostKeyboard
        label: "TURN 凭据期限"
        value: settings.form ? settings.form.turnTtl : ""
        placeholder: "6h"
        invalid: settings.form && !settings.form.isDurationBetween(settings.form.turnTtl, 60000, 21600000)
        onEdited: function(nextValue) {
            settings.form.turnTtl = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
        hostKeyboard: settings.hostKeyboard
        label: "允许使用的网卡"
        value: settings.form ? settings.form.interfaceAllowlist : ""
        placeholder: "留空表示全部"
        onEdited: function(nextValue) {
            settings.form.interfaceAllowlist = nextValue
            settings.form.syncConfig()
        }
    }

    Field {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ipv6"
        hostKeyboard: settings.hostKeyboard
        label: "手动 IPv6 候选地址"
        value: settings.form ? settings.form.candidateAddresses : ""
        placeholder: "留空表示自动收集"
        onEdited: function(nextValue) {
            settings.form.candidateAddresses = nextValue
            settings.form.syncConfig()
        }
    }

    SwitchRow {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
        title: "包含回环候选"
        summary: "同机调试时可开启"
        checked: settings.form && settings.form.includeLoopback
        onToggled: function(nextValue) {
            settings.form.includeLoopback = nextValue
            settings.form.syncConfig()
        }
    }

    SwitchRow {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: settings.form && settings.form.preferred === "ice"
        title: "仅使用中继"
        summary: "始终走 TURN，不做直连尝试"
        checked: settings.form && settings.form.relayOnly
        onToggled: function(nextValue) {
            settings.form.relayOnly = nextValue
            settings.form.syncConfig()
        }
    }
}
