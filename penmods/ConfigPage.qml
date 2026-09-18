import QtQuick 2.12
import QtQuick.Layouts 1.12
import "qrc:/qml/commons"
import "components"

// A real child page opened from Main.qml. Runtime status intentionally remains
// on the home page so this page is dedicated to mapping configuration.
YBackButtonPage {
    id: configPage

    property var form: null
    property var hostKeyboard: null
    property var provideModel: null
    property var consumeModel: null

    objectName: "HoleConfigPage"

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
        contentHeight: configColumn.implicitHeight + Theme.spacing
        interactive: true
        pressDelay: 0
        boundsBehavior: Flickable.StopAtBounds

        ColumnLayout {
            id: configColumn

            width: scroll.width
            spacing: Theme.spacing

            Text {
                Layout.fillWidth: true
                text: "配置"
                color: Theme.textPrimary
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontHeader
                font.bold: true
            }

            Text {
                Layout.fillWidth: true
                text: "管理提供和使用的 TCP / UDP 映射"
                color: Theme.textMuted
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontCaption
                wrapMode: Text.WordWrap
            }

            MappingSection {
                Layout.fillWidth: true
                form: configPage.form
                mappingModel: configPage.provideModel
                mappingKind: "provide"
                withProtocol: true
                hostKeyboard: configPage.hostKeyboard
            }

            MappingSection {
                Layout.fillWidth: true
                form: configPage.form
                mappingModel: configPage.consumeModel
                mappingKind: "consume"
                withProtocol: false
                hostKeyboard: configPage.hostKeyboard
            }
        }
    }
}
