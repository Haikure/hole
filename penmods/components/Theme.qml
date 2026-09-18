pragma Singleton

import QtQuick 2.12

// Single source of truth for the plugin palette and metrics. Components must
// not hardcode colours; they read them from here.
QtObject {
    // Keep plugin text consistent with the requested host UI font. Qt falls
    // back to the nearest installed font when the device does not provide it.
    readonly property string fontFamily: "Microsoft YaHei"

    // Palette
    // Keep the plugin visually aligned with YInputPage/YColors instead of
    // introducing a second blue-black palette for the same host application.
    readonly property color background: "#101114"
    readonly property color surface: "#1a1b1f"
    readonly property color surfaceRaised: "#2d2e33"
    readonly property color surfaceError: "#30171b"
    readonly property color field: "#24252a"
    readonly property color fieldActive: "#36373d"
    readonly property color border: "#41434a"
    readonly property color borderHeader: "#515259"
    readonly property color borderActive: "#509deb"

    readonly property color textPrimary: "#f4f4f5"
    readonly property color textSecondary: "#c5c6ca"
    readonly property color textMuted: "#909199"
    readonly property color textPlaceholder: "#74767d"
    readonly property color textFooter: "#70727a"

    readonly property color accent: "#509deb"
    readonly property color accentInk: "#ffffff"
    readonly property color warning: "#e9900c"
    readonly property color error: "#f03043"

    readonly property color trackOff: "#515259"
    readonly property color trackOffBorder: "#686a72"
    readonly property color knobOff: "#d6d7da"
    readonly property color statusIdle: "#515259"

    // Metrics
    readonly property int pageMargin: 8
    readonly property int contentWidth: 304
    readonly property int spacing: 6
    readonly property int cardPadding: 7
    readonly property int cardSpacing: 5
    readonly property int radius: 9
    readonly property int radiusCard: 9
    readonly property int controlHeight: 38
    readonly property int rowHeight: 36
    readonly property int tapMin: 40

    // Type scale
    readonly property int fontHeader: 14
    readonly property int fontTitle: 13
    readonly property int fontValue: 11
    readonly property int fontLabel: 10
    readonly property int fontCaption: 9
}
