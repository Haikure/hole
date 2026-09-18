import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

// A mapping section owns the delegate plumbing. Main.qml only supplies the
// model and controller, which keeps model writes in one place.
Section {
    id: mappings

    property var form: null
    property var mappingModel: null
    property var hostKeyboard: null
    property string mappingKind: "provide"
    property bool withProtocol: true

    title: mappingKind === "provide" ? "提供服务" : "使用服务"
    subtitle: mappingKind === "provide" ? "provide" : "consume"

    Repeater {
        model: mappings.mappingModel

        delegate: MappingCard {
            Layout.fillWidth: true
            kind: mappings.mappingKind
            withProtocol: mappings.withProtocol
            hostKeyboard: mappings.hostKeyboard
            entryId: mappingId
            entryActive: active
            entryProtocol: protocol
            entryAddr: addr
            entryPort: port
            entryPortInvalid: entryActive && mappings.form && !mappings.form.isPort(port)
            entryIssue: entryActive && mappings.form
                         ? mappings.form.rowIssue(entryId, entryPort, entryAddr,
                                                  mappings.mappingKind === "provide" ? "提供映射" : "使用映射",
                                                  mappings.mappingKind === "consume")
                         : ""
            onIdEdited: function(nextValue) {
                if (mappings.mappingKind === "provide") mappings.form.setProvide(index, "mappingId", nextValue)
                else mappings.form.setConsume(index, "mappingId", nextValue)
            }
            onProtocolChosen: function(nextValue) { mappings.form.setProvide(index, "protocol", nextValue) }
            onAddrEdited: function(nextValue) {
                if (mappings.mappingKind === "provide") mappings.form.setProvide(index, "addr", nextValue)
                else mappings.form.setConsume(index, "addr", nextValue)
            }
            onPortEdited: function(nextValue) {
                if (mappings.mappingKind === "provide") mappings.form.setProvide(index, "port", nextValue)
                else mappings.form.setConsume(index, "port", nextValue)
            }
            onActiveToggled: function(nextValue) {
                if (mappings.mappingKind === "provide") mappings.form.setProvide(index, "active", nextValue)
                else mappings.form.setConsume(index, "active", nextValue)
            }
            onRemoved: {
                if (mappings.mappingKind === "provide") mappings.form.removeProvide(index)
                else mappings.form.removeConsume(index)
            }
        }
    }

    Text {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: !mappings.mappingModel || mappings.mappingModel.count === 0
        text: mappings.mappingKind === "provide"
              ? "还没有提供映射。添加后，本机服务会通过房间提供给其他设备。"
              : "还没有使用映射。添加后，远端提供的服务会映射到本机地址。"
        color: Theme.textMuted
        font.family: Theme.fontFamily
        font.pixelSize: Theme.fontCaption
        wrapMode: Text.WordWrap
    }

    ActionButton {
        Layout.fillWidth: true
        text: mappings.mappingKind === "provide" ? "新增提供映射" : "新增使用映射"
        variant: "secondary"
        onClicked: {
            if (mappings.mappingKind === "provide") mappings.form.addProvide()
            else mappings.form.addConsume()
        }
    }
}
