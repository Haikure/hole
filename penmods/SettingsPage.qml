import QtQuick 2.12
import QtQuick.Layouts 1.12
import "qrc:/qml/commons"
import "components"

// A real child page opened from Main.qml. The connection status stays on the
// home page; this page only contains editable connection settings.
YBackButtonPage {
    id: settingsPage

    property var form: null
    property var hostKeyboard: null

    objectName: "HoleSettingsPage"
    PageEntrance { target: scroll }

    Rectangle {
        anchors.fill: parent
        z: -1
        color: Theme.background
    }

    Flickable {
        id: scroll

        anchors.top: parent.top
        anchors.bottom: parent.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.leftMargin: 54
        anchors.rightMargin: 8
        anchors.topMargin: 8
        anchors.bottomMargin: 8
        clip: true
        contentWidth: width
        contentHeight: settingsColumn.height + Theme.spacing
        interactive: !advancedSettings.turnOrderDragging
        pressDelay: 0
        boundsBehavior: Flickable.StopAtBounds

        ColumnLayout {
            id: settingsColumn

            width: scroll.width
            spacing: Theme.spacing

            PageHeading {
                Layout.fillWidth: true
                title: "设置"; iconName: "settings"
                subtitle: settingsPage.form ? settingsPage.form.saveState : ""
            }

            Section {
                Layout.fillWidth: true
                title: "连接设置"
                subtitle: "房间与信令"
                iconName: "network"

                Field {
                    Layout.fillWidth: true
                    hostKeyboard: settingsPage.hostKeyboard
                    label: "信令服务器"
                    value: settingsPage.form ? settingsPage.form.serverUrl : ""
                    placeholder: "wss://HOST/ws"
                    invalid: settingsPage.form && (settingsPage.form.serverUrl.length === 0
                                                     || !/^(ws|wss):\/\/.+/.test(settingsPage.form.serverUrl))
                    onEdited: function(nextValue) {
                        settingsPage.form.serverUrl = nextValue
                        settingsPage.form.syncConfig()
                    }
                }

                Field {
                    Layout.fillWidth: true
                    hostKeyboard: settingsPage.hostKeyboard
                    label: "信令密码"
                    value: settingsPage.form ? settingsPage.form.password : ""
                    secret: true
                    onEdited: function(nextValue) {
                        settingsPage.form.password = nextValue
                        settingsPage.form.syncConfig()
                    }
                }

                GridLayout {
                    Layout.fillWidth: true
                    columns: width >= 210 ? 2 : 1
                    columnSpacing: 5; rowSpacing: 5
                    Field {
                        Layout.fillWidth: true
                        Layout.preferredWidth: 1
                        hostKeyboard: settingsPage.hostKeyboard
                        label: "房间号"
                        value: settingsPage.form ? settingsPage.form.room : ""
                        onEdited: function(nextValue) {
                            settingsPage.form.room = nextValue
                            settingsPage.form.syncConfig()
                        }
                    }

                    Field {
                        Layout.fillWidth: true
                        hostKeyboard: settingsPage.hostKeyboard
                        label: "房间密码"
                        Layout.preferredWidth: 1
                        value: settingsPage.form ? settingsPage.form.token : ""
                        secret: true
                        onEdited: function(nextValue) {
                            settingsPage.form.token = nextValue
                            settingsPage.form.syncConfig()
                        }
                    }

                    Field {
                        Layout.fillWidth: true
                        hostKeyboard: settingsPage.hostKeyboard
                        label: "设备名"
                        Layout.preferredWidth: 1
                        value: settingsPage.form ? settingsPage.form.deviceName : ""
                        placeholder: "pen"
                        onEdited: function(nextValue) {
                            settingsPage.form.deviceName = nextValue
                            settingsPage.form.syncConfig()
                        }
                    }

                    Field {
                        Layout.fillWidth: true
                        hostKeyboard: settingsPage.hostKeyboard
                        label: "会话保留期限"
                        Layout.preferredWidth: 1
                        value: settingsPage.form ? settingsPage.form.sessionTimeout : ""
                        placeholder: "10m"
                        invalid: settingsPage.form && !settingsPage.form.isDuration(settingsPage.form.sessionTimeout)
                        onEdited: function(nextValue) {
                            settingsPage.form.sessionTimeout = nextValue
                            settingsPage.form.syncConfig()
                        }
                    }
                }
            }

            Section {
                Layout.fillWidth: true
                title: "连接策略"
                subtitle: "优先路径"
                iconName: "mapping"

                ConnectionModeRow {
                    Layout.fillWidth: true
                    form: settingsPage.form
                }

                SwitchRow {
                    Layout.fillWidth: true
                    title: "允许不安全信令"
                    summary: "仅用于本地 ws 测试，生产环境应关闭"
                    checked: settingsPage.form && settingsPage.form.allowInsecureSignal
                    onToggled: function(nextValue) {
                        settingsPage.form.allowInsecureSignal = nextValue
                        settingsPage.form.syncConfig()
                    }
                }
            }

            ActionButton {
                Layout.fillWidth: true
                text: "检测网络"; iconName: "network"
                onClicked: settingsPage.form.openSeparatePage("NATPage.qml", {form: settingsPage.form})
            }

            AdvancedSettings {
                id: advancedSettings
                Layout.fillWidth: true
                form: settingsPage.form
                scrollView: scroll
                hostKeyboard: settingsPage.hostKeyboard
                expanded: settingsPage.form ? settingsPage.form.advancedOpen : false
                onHeaderTapped: {
                    if (settingsPage.form) settingsPage.form.advancedOpen = !settingsPage.form.advancedOpen
                }
            }
        }
    }
}
