import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

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
    property bool expanded: false
    signal idEdited(string nextValue)
    signal protocolChosen(string nextValue)
    signal addrEdited(string nextValue)
    signal portEdited(string nextValue)
    signal activeToggled(bool nextValue)
    signal removed()
    implicitWidth: Theme.contentWidth
    implicitHeight: body.implicitHeight + 14
    radius: Theme.radiusCard; color: Theme.field
    border.width: card.entryIssue.length > 0 ? 1 : 0; border.color: Theme.warning
    ColumnLayout {
        id: body
        x: 7; y: 7; width: parent.width - 14; spacing: 6
        Item {
            Layout.fillWidth: true; Layout.preferredHeight: 44
            RowLayout {
                anchors.fill: parent; spacing: 7
                NATIcon { name: card.withProtocol ? "upload" : "download"; tint: card.entryActive ? Theme.accent : Theme.textMuted; Layout.preferredWidth: 18; Layout.preferredHeight: 18 }
                ColumnLayout {
                    Layout.fillWidth: true; spacing: 3
                    Text { Layout.fillWidth: true; text: card.entryId || "新服务"; color: Theme.textPrimary; font.family: Theme.fontFamily; font.pixelSize: 13; font.bold: true; elide: Text.ElideRight }
                    Text { Layout.fillWidth: true; text: (card.withProtocol ? card.entryProtocol.toUpperCase() + " · " : "") + card.entryAddr + ":" + (card.entryPort || "—"); color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 10; elide: Text.ElideMiddle }
                }
                NATIcon { name: "chevron"; rotation: card.expanded ? 90 : 0; Layout.preferredWidth: 16; Layout.preferredHeight: 16; Behavior on rotation { NumberAnimation { duration: 160 } } }
            }
            MouseArea { objectName: "mappingExpand"; anchors.fill: parent; preventStealing: false; onClicked: card.expanded = !card.expanded }
        }
        Text { Layout.fillWidth: true; visible: text.length > 0; text: card.entryIssue; color: Theme.warning; font.family: Theme.fontFamily; font.pixelSize: 10; wrapMode: Text.Wrap }
        SwitchRow { Layout.fillWidth: true; title: card.entryActive ? "已启用" : "已停用"; checked: card.entryActive; onToggled: card.activeToggled(nextValue) }
        Reveal {
            Layout.fillWidth: true; expanded: card.expanded
            ColumnLayout {
                spacing: 6
                Field { Layout.fillWidth: true; hostKeyboard: card.hostKeyboard; label: "服务 ID"; value: card.entryId; placeholder: "例如 ssh"; onEdited: card.idEdited(nextValue) }
                ChoiceRow {
                    Layout.fillWidth: true; visible: card.withProtocol
                    options: [{value: "tcp", label: "TCP"}, {value: "udp", label: "UDP"}]
                    selected: card.entryProtocol; onChosen: card.protocolChosen(nextValue)
                }
                RowLayout {
                    Layout.fillWidth: true; spacing: 5
                    Field { Layout.fillWidth: true; hostKeyboard: card.hostKeyboard; label: card.withProtocol ? "服务地址" : "监听地址"; value: card.entryAddr; placeholder: "127.0.0.1"; onEdited: card.addrEdited(nextValue) }
                    Field { Layout.preferredWidth: 70; hostKeyboard: card.hostKeyboard; label: "端口"; value: card.entryPort; placeholder: "1–65535"; numeric: true; invalid: card.entryPortInvalid; onEdited: card.portEdited(nextValue) }
                }
                ActionButton { Layout.fillWidth: true; text: "删除服务"; iconName: "trash"; variant: "danger"; onClicked: card.removed() }
            }
        }
    }
    Component.onCompleted: expanded = entryId.length === 0 || entryPort.length === 0
}
