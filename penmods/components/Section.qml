import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

// Grouped settings card: a title row plus a card that sizes itself to the
// content declared inside. Collapsible sections keep the long form navigable.
ColumnLayout {
    id: section

    property string title: ""
    property string subtitle: ""
    property string iconName: ""
    property bool collapsible: false
    property bool expanded: true

    default property alias content: body.data

    signal headerTapped()

    readonly property bool open: !section.collapsible || section.expanded

    implicitWidth: Theme.contentWidth
    spacing: 4

    Item {
        id: header
        Layout.fillWidth: true
        Layout.preferredHeight: visible
                               ? Math.max(headerRow.implicitHeight, section.collapsible ? Theme.tapMin : 0)
                               : 0
        visible: section.title.length > 0

        RowLayout {
            id: headerRow
            anchors.fill: parent
            spacing: 4

            NATIcon {
                visible: section.iconName.length > 0
                name: section.iconName
                Layout.preferredWidth: 17; Layout.preferredHeight: 17
            }

            Text {
                Layout.alignment: Qt.AlignVCenter
                text: section.title
                color: Theme.textPrimary
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontTitle
                font.bold: true
            }

            Text {
                Layout.alignment: Qt.AlignVCenter
                Layout.fillWidth: true
                text: section.subtitle
                visible: text.length > 0
                color: Theme.textMuted
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontCaption
                elide: Text.ElideRight
                horizontalAlignment: Text.AlignRight
            }

            NATIcon {
                Layout.alignment: Qt.AlignVCenter
                visible: section.collapsible
                name: "chevron"
                Layout.preferredWidth: 16; Layout.preferredHeight: 16
                rotation: section.open ? 90 : 0
                Behavior on rotation { NumberAnimation { duration: 120 } }
            }
        }

        MouseArea {
            anchors.fill: parent
            z: 1
            acceptedButtons: Qt.LeftButton
            enabled: section.collapsible
            preventStealing: false
            onClicked: section.headerTapped()
        }
    }

    Rectangle {
        id: card

        property real expansion: section.open ? 1 : 0
        Layout.fillWidth: true
        Layout.preferredHeight: implicitHeight
        implicitHeight: (body.height + 2 * Theme.cardPadding) * expansion
        visible: expansion > 0
        opacity: expansion
        clip: true
        Behavior on expansion { NumberAnimation { duration: 160; easing.type: Easing.OutCubic } }
        radius: Theme.radiusCard
        color: Theme.surface

        ColumnLayout {
            id: body

            x: Theme.cardPadding; y: Theme.cardPadding
            width: parent.width - 2 * Theme.cardPadding
            spacing: Theme.cardSpacing
        }
    }
}
