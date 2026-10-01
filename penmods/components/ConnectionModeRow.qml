import QtQuick 2.12
import "."

ChoiceRow {
    id: mode
    property var form: null

    title: "连接方式"
    options: [{value: "auto", label: "自动"}, {value: "ice", label: "仅 ICE"}, {value: "legacy", label: "仅 IPv6"}]
    selected: !form ? "auto" : form.preferred === "ipv6" ? "legacy" : form.allowLegacy ? "auto" : "ice"
    summary: selected === "legacy"
             ? "仅使用公网 IPv6 直连，双方都需要可用的公网 IPv6；不使用 ICE、STUN 或 TURN。"
             : selected === "ice"
             ? "使用 ICE 探测 IPv4 / IPv6 直连，直连不通时按设置尝试 TURN；协调服务和对端都需支持 ICE。"
             : "优先使用 ICE；对端仅支持 IPv6 协议时兼容直连，不因 ICE 探测失败而切换协议。"
    onChosen: {
        if (!form) return
        form.preferred = nextValue === "legacy" ? "ipv6" : "ice"
        form.allowLegacy = nextValue === "auto"
        form.syncConfig()
    }
}
