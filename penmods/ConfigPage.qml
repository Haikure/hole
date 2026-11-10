import QtQuick 2.12
import QtQuick.Layouts 1.12
import "qrc:/qml/commons"
import "components"

// Service editing uses the existing host page and shared configuration model.
YBackButtonPage {
    id: configPage

    property var form: null
    property var hostKeyboard: null
    property var provideModel: null
    property var consumeModel: null
    property string selectedKind: "provide"

    objectName: "HoleConfigPage"
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
        contentHeight: configColumn.height + Theme.spacing
        interactive: true
        pressDelay: 0
        boundsBehavior: Flickable.StopAtBounds

        ColumnLayout {
            id: configColumn

            width: scroll.width
            spacing: Theme.spacing

            PageHeading {
                Layout.fillWidth: true
                title: "服务"; iconName: "mapping"
                subtitle: configPage.form ? configPage.form.saveState : ""
            }
            ChoiceRow {
                Layout.fillWidth: true
                options: [{value: "provide", label: "提供服务"}, {value: "consume", label: "使用服务"}]
                selected: configPage.selectedKind
                onChosen: { configPage.selectedKind = nextValue; scroll.contentY = 0 }
            }

            MappingSection {
                Layout.fillWidth: true
                form: configPage.form
                mappingModel: configPage.provideModel
                mappingKind: "provide"
                visible: configPage.selectedKind === "provide"
                withProtocol: true
                hostKeyboard: configPage.hostKeyboard
            }

            MappingSection {
                Layout.fillWidth: true
                form: configPage.form
                mappingModel: configPage.consumeModel
                mappingKind: "consume"
                visible: configPage.selectedKind === "consume"
                withProtocol: false
                hostKeyboard: configPage.hostKeyboard
            }
        }
    }
}
