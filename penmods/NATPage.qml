import QtQuick 2.12
import QtQuick.Layouts 1.12
import "qrc:/qml/commons"
import "components"

YBackButtonPage {
    id: natPage
    property var form: null
    objectName: "HoleNATPage"
    PageEntrance { target: scroll }
    Rectangle { anchors.fill: parent; z: -1; color: Theme.background }
    Flickable {
        id: scroll
        anchors.fill: parent
        anchors.leftMargin: 54; anchors.rightMargin: 8; anchors.topMargin: 8; anchors.bottomMargin: 8
        clip: true; contentWidth: width; contentHeight: content.height + 12
        boundsBehavior: Flickable.StopAtBounds
        NATPanel {
            id: content
            width: scroll.width
            report: natPage.form ? natPage.form.natReport : ({state: "idle", sections: []})
            errorText: holePlugin.lastError
            onStartRequested: {
                holePlugin.startNATProbe(JSON.stringify({stun_urls: natPage.form.parseList(natPage.form.stunUrls), nat_stun_urls: natPage.form.natStunUrls, interface_allowlist: natPage.form.parseList(natPage.form.interfaceAllowlist)}))
            }
            onCancelRequested: holePlugin.cancelNATProbe()
        }
    }
    Component.onCompleted: holePlugin.refresh()
}
