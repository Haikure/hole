import QtQuick 2.12
import QtQuick.Layouts 1.12
import "."

Rectangle {
    id: card
    property var hero: ({})
    property bool paused: false
    readonly property color ink: Theme.natInk(hero.tone || "blue")
    readonly property string sequence: hero.sequence || ""
    implicitHeight: body.implicitHeight + 18
    radius: Theme.radiusCard
    gradient: Gradient {
        GradientStop { position: 0; color: Theme.surfaceRaised }
        GradientStop { position: 1; color: Theme.natSurface(card.hero.tone || "blue") }
    }
    ColumnLayout {
        id: body
        x: 9; y: 9; width: parent.width - 18; spacing: 7
        RowLayout {
            Layout.fillWidth: true; spacing: 6
            NATIcon { name: "mapping"; tint: card.ink; Layout.preferredWidth: 19; Layout.preferredHeight: 19 }
            Text { Layout.fillWidth: true; text: card.hero.title || "准备连接"; color: Theme.textPrimary; font.family: Theme.fontFamily; font.pixelSize: 13; font.bold: true; wrapMode: Text.Wrap }
            Text { text: card.paused ? "回放" : card.hero.state || "准备中"; color: card.ink; font.family: Theme.fontFamily; font.pixelSize: 10 }
        }
        Text { Layout.fillWidth: true; text: card.hero.subtitle || ""; color: card.ink; font.family: Theme.fontFamily; font.pixelSize: 10; wrapMode: Text.Wrap }
        RowLayout {
            Layout.fillWidth: true; spacing: 6
            ColumnLayout {
                Layout.fillWidth: true; Layout.preferredWidth: 1; spacing: 2
                Text { text: card.hero.localLabel || "本机 UDP"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9 }
                Text { Layout.fillWidth: true; text: card.hero.local ? card.hero.local.host || "待观测" : "待观测"; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.WrapAnywhere; maximumLineCount: 2; elide: Text.ElideRight }
                Text { text: card.hero.local ? card.hero.local.port || "—" : "—"; color: Theme.textPrimary; font.family: "monospace"; font.pixelSize: 25; font.bold: true }
                Text { text: card.hero.localSource || "待观测"; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9 }
            }
            NATIcon { name: "upload"; rotation: card.hero.direction === "in" ? -90 : 90; tint: card.ink; Layout.preferredWidth: 17; Layout.preferredHeight: 17 }
            ColumnLayout {
                Layout.fillWidth: true; Layout.preferredWidth: 1; spacing: 2
                Text { Layout.fillWidth: true; text: card.hero.remoteLabel || "目标 / 来源"; horizontalAlignment: Text.AlignRight; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9 }
                Text { Layout.fillWidth: true; text: card.hero.remote ? card.hero.remote.host || "待观测" : "待观测"; horizontalAlignment: Text.AlignRight; color: Theme.textSecondary; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.WrapAnywhere; maximumLineCount: 2; elide: Text.ElideRight }
                Text { Layout.fillWidth: true; text: card.hero.remote ? card.hero.remote.port || "—" : "—"; horizontalAlignment: Text.AlignRight; color: card.ink; font.family: "monospace"; font.pixelSize: 25; font.bold: true }
                Text { Layout.fillWidth: true; text: card.hero.remoteSource || "待观测"; horizontalAlignment: Text.AlignRight; color: Theme.textMuted; font.family: Theme.fontFamily; font.pixelSize: 9 }
            }
        }
        Item {
            id: flow
            Layout.fillWidth: true; Layout.preferredHeight: 8
            property real progress: 1
            Rectangle { anchors.verticalCenter: parent.verticalCenter; width: parent.width; height: 1; color: card.ink; opacity: 0.25 }
            Rectangle { width: 5; height: 5; radius: 3; anchors.verticalCenter: parent.verticalCenter; color: card.ink; visible: card.hero.direction === "in" || card.hero.direction === "out"; x: (parent.width - width) * (card.hero.direction === "in" ? 1 - flow.progress : flow.progress) }
            NumberAnimation { id: flight; target: flow; property: "progress"; from: 0; to: 1; duration: 380 }
        }
        Text { Layout.fillWidth: true; text: card.hero.activity || "等待实际收发"; color: card.ink; font.family: Theme.fontFamily; font.pixelSize: 9; wrapMode: Text.Wrap }
    }
    onSequenceChanged: if (!paused && sequence.length > 0 && hero.direction) flight.restart()
    onPausedChanged: if (paused) { flight.stop(); flow.progress = 1 }
}
