import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

// One provide/consume mapping. The card reports every edit as a signal and
// never mutates the model itself, so the owner can store it with
// ListModel.setProperty instead of rebuilding the whole list.
Rectangle {
    id: card

    property string kind: "provide"
    property bool withProtocol: true
    property string entryId: ""
    property bool entryActive: true
    property string entryProtocol: "tcp"
    property string entryAddr: "127.0.0.1"
    property string entryPort: ""
    property bool entryPortInvalid: false
    property string entryIssue: ""
    property var hostKeyboard: null

    signal idEdited(string nextValue)
    signal protocolChosen(string nextValue)
    signal addrEdited(string nextValue)
    signal portEdited(string nextValue)
    signal activeToggled(bool nextValue)
    signal removed()

    implicitWidth: Theme.contentWidth
    implicitHeight: body.implicitHeight + 2 * Theme.cardPadding
    radius: Theme.radiusCard
    color: Theme.surface
    border.width: card.entryIssue.length > 0 ? 1 : 0
    border.color: Theme.warning
    opacity: card.entryActive ? 1 : 0.62

    ColumnLayout {
        id: body

        x: Theme.cardPadding
        y: Theme.cardPadding
        width: parent.width - 2 * Theme.cardPadding
        spacing: 4

        RowLayout {
            Layout.fillWidth: true
            spacing: 4

            Text {
                text: card.kind === "provide" ? "提供" : "使用"
                color: Theme.accent
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontCaption
                font.bold: true
            }

            Text {
                Layout.fillWidth: true
                text: card.entryIssue.length > 0
                      ? card.entryIssue
                      : (card.entryActive ? "" : "已停用")
                visible: text.length > 0
                color: Theme.warning
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontCaption
                elide: Text.ElideRight
            }

            ActionButton {
                Layout.preferredWidth: 60
                Layout.preferredHeight: 30
                text: "删除"
                variant: "danger"
                onClicked: card.removed()
            }
        }

        Field {
            Layout.fillWidth: true
            hostKeyboard: card.hostKeyboard
            label: "映射 ID"
            value: card.entryId
            placeholder: "例如 ssh"
            onEdited: function(nextValue) { card.idEdited(nextValue) }
        }

        ChoiceRow {
            Layout.fillWidth: true
            Layout.preferredHeight: visible ? implicitHeight : 0
            visible: card.withProtocol
            title: "协议"
            options: [{value: "tcp", label: "TCP"}, {value: "udp", label: "UDP"}]
            selected: card.entryProtocol
            onChosen: function(nextValue) { card.protocolChosen(nextValue) }
        }

        Field {
            Layout.fillWidth: true
            hostKeyboard: card.hostKeyboard
            label: card.withProtocol ? "本机服务地址" : "本机监听地址"
            value: card.entryAddr
            placeholder: "127.0.0.1"
            onEdited: function(nextValue) { card.addrEdited(nextValue) }
        }

        Field {
            Layout.fillWidth: true
            hostKeyboard: card.hostKeyboard
            label: "端口"
            value: card.entryPort
            placeholder: "1-65535"
            numeric: true
            invalid: card.entryPortInvalid
            onEdited: function(nextValue) { card.portEdited(nextValue) }
        }

        SwitchRow {
            Layout.fillWidth: true
            title: "启用此映射"
            summary: "停用时保留配置，不加入运行集合"
            checked: card.entryActive
            onToggled: function(nextValue) { card.activeToggled(nextValue) }
        }
    }
}
