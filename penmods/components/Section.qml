import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

// Grouped settings card: a title row plus a card that sizes itself to the
// content declared inside. Collapsible sections keep the long form navigable.
ColumnLayout {
    id: section

    property string title: ""
    property string subtitle: ""
    property bool collapsible: false
    property bool expanded: true

    default property alias content: body.data

    signal headerTapped()

    readonly property bool open: !section.collapsible || section.expanded

    implicitWidth: Theme.contentWidth
    spacing: 4

    RowLayout {
        Layout.fillWidth: true
        Layout.preferredHeight: visible ? implicitHeight : 0
        visible: section.title.length > 0
        spacing: 4

        Text {
            text: section.title
            color: Theme.textPrimary
            font.family: Theme.fontFamily
            font.pixelSize: Theme.fontTitle
            font.bold: true
        }

        Text {
            Layout.fillWidth: true
            text: section.subtitle
            visible: text.length > 0
            color: Theme.textMuted
            font.family: Theme.fontFamily
            font.pixelSize: Theme.fontCaption
            elide: Text.ElideRight
            horizontalAlignment: Text.AlignRight
        }

        Text {
            visible: section.collapsible
            text: section.open ? "收起" : "展开"
            color: Theme.accent
            font.family: Theme.fontFamily
            font.pixelSize: Theme.fontCaption
        }

        MouseArea {
            anchors.fill: parent
            acceptedButtons: Qt.LeftButton
            enabled: section.collapsible
            preventStealing: false
            onClicked: section.headerTapped()
        }
    }

    Rectangle {
        id: card

        Layout.fillWidth: true
        Layout.preferredHeight: card.visible ? card.implicitHeight : 0
        visible: section.open
        implicitHeight: body.implicitHeight + 2 * Theme.cardPadding
        radius: Theme.radiusCard
        color: Theme.surface

        ColumnLayout {
            id: body

            x: Theme.cardPadding
            y: Theme.cardPadding
            width: parent.width - 2 * Theme.cardPadding
            spacing: Theme.cardSpacing
        }
    }
}
