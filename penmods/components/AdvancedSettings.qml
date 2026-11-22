import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

Section {
    id: settings

    property var form: null
    property var hostKeyboard: null
    property var scrollView: null
    readonly property bool turnOrderDragging: formLoader.item ? formLoader.item.turnOrderDragging : false

    title: "高级连接参数"
    subtitle: "ICE / TURN"
    collapsible: true

    ContentLoader {
        id: formLoader
        Layout.fillWidth: true
        contentWidth: settings.contentWidth
        // Drafts live in the page model, independently of these items.
        active: settings.open && settings.visible && settings.form !== null
        sourceComponent: AdvancedSettingsForm {
            width: settings.contentWidth
            form: settings.form
            hostKeyboard: settings.hostKeyboard
            scrollView: settings.scrollView
        }
    }
}
