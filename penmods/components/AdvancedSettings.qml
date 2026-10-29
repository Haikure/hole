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
    subtitle: form && form.preferred === "ipv6" ? "IPv6" : "ICE / TURN"
    collapsible: true

    Loader {
        id: formLoader
        Layout.fillWidth: true
        Layout.preferredWidth: Math.max(0, settings.width - 2 * Theme.cardPadding)
        // Build the form with the available width and current data on each open.
        // Drafts live in the page model, independently of these visual items.
        active: settings.open && settings.visible && settings.form !== null
                && settings.width > 2 * Theme.cardPadding
        sourceComponent: AdvancedSettingsForm {
            width: formLoader.width
            form: settings.form
            hostKeyboard: settings.hostKeyboard
            scrollView: settings.scrollView
        }
    }
}
