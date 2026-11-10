import QtQuick 2.12
import QtQuick.Layouts 1.12
import "qrc:/qml/commons"
import "components"
import "components/NATTime.js" as NATTime

Item {
    id: page
    // PenMods normally supplies the viewport size. Keep a small fallback for
    // preview tools, but never pin the form to the preview dimensions.
    width: parent ? parent.width : 320
    height: parent ? parent.height : 170
    implicitWidth: 320
    implicitHeight: 170

    signal backButtonClicked()

    // Connection settings
    property string serverUrl: ""
    property string password: ""
    property string room: ""
    property string token: ""
    property string deviceName: "pen"
    property string sessionTimeout: "10m"
    property string preferred: "ice"
    property bool allowLegacy: true
    property bool allowInsecureSignal: false
    property string stunUrls: ""
    property string portMapping: "auto"
    property var natStunUrls: null
    property string turnMode: "worker"
    property string turnOrder: ""
    property string relayDevice: ""
    property string relayOrder: ""
    property bool relayOrderExplicit: false
    property bool relayServeUnsupported: false
    property string turnUrls: ""
    property string turnUsername: ""
    property string turnCredential: ""
    property string directProbeTimeout: "3s"
    property string gatherTimeout: "6s"
    property string connectivityTimeout: "10s"
    property string retryMaxDelay: "15s"
    property string turnTtl: "6h"
    property string interfaceAllowlist: ""
    property string candidateAddresses: ""
    property bool includeLoopback: false
    property bool relayOnly: false

    // Page state
    property bool initialized: false
    property bool advancedOpen: false
    property var openedPage: null
    property int pageRequestToken: 0
    property string localError: ""
    property string saveState: ""
    property string snapshotError: ""
    readonly property string statusRaw: holePlugin.snapshotJson.length > 0 ? snapshotError
        : holePlugin.reloading ? "正在重载核心，请稍候"
        : holePlugin.running ? "核心尚未返回快照，可点击刷新重试" : "核心未运行"
    property string lastSnapshotRaw: ""
    property int snapshotRevision: 0
    property int dispatchedRevision: 0
    property bool snapshotDecoding: false
    property string natReportKey: ""
    property string currentView: "home"
    property var runtimeSnapshot: ({})
    property var natReport: ({state: "idle", title: "检测当前网络", summary: "检查映射、过滤与端口特征，无需加入房间。", sections: []})

    readonly property string defaultStun: "stun:stun.cloudflare.com:3478"
    readonly property string errorText: localError.length > 0 ? localError : holePlugin.lastError
    // The core only publishes engine=running after the signal join has been
    // acknowledged. Do not infer success from an old snapshot or the start
    // RPC response: ICE probing can still be in progress at that point.
    readonly property bool connectionEstablished: holePlugin.state === "running"
    readonly property bool runRequested: holePlugin.running
                                        || holePlugin.state === "starting"
                                        || holePlugin.state === "running"
                                        || holePlugin.state === "recovering"
                                        || holePlugin.state === "reconfiguring"

    ListModel { id: provides }
    ListModel { id: consumes }

    // ---------------------------------------------------------------- helpers

    function asString(value) {
        if (value === undefined || value === null) return ""
        if (typeof value === "string") return value
        if (typeof value === "number" || typeof value === "boolean") return String(value)
        return ""
    }

    function asBool(value, fallback) {
        if (value === undefined || value === null) return fallback
        if (typeof value === "boolean") return value
        if (typeof value === "number") return value !== 0
        if (typeof value === "string") return value === "true" || value === "1"
        return fallback
    }

    // Splits the comma/newline separated text the user types into a clean list.
    function parseList(text) {
        var seen = {}
        var result = []
        String(text).split(/[\s,，]+/).forEach(function (part) {
            if (part.length === 0) return
            if (Object.prototype.hasOwnProperty.call(seen, part)) return
            seen[part] = true
            result.push(part)
        })
        return result
    }

    function formatList(value) {
        if (!Array.isArray(value)) return ""
        var parts = []
        for (var i = 0; i < value.length; ++i) {
            var item = asString(value[i])
            if (item.length > 0) parts.push(item)
        }
        return parts.join(", ")
    }

    function parseRelayOrder(text) {
        var order = String(text).split(/[\s,，;；]+/).map(function (part) { return part.trim() }).filter(function (part) { return part.length > 0 })
        if (order.indexOf("tcp_80") < 0 && order.indexOf("tls_443") < 0) return order
        return order.map(function(token) { return token === "tcp_80" ? "tcp" : token === "tls_443" ? "tls" : token }).filter(function(token,index,all) { return all.indexOf(token) === index })
    }

    function snapshotRelayOrder(order) {
        if (!Array.isArray(order) || order.length === 0) return "默认：UDP → TCP → TLS"
        var labels = {hole: "Hole", udp: "UDP", tcp_80: "TCP 80", tcp: "TCP 3478", tls_443: "TLS 443", tls: "TLS 5349"}
        return order.map(function (token) { return labels[token] || token }).join(" → ")
    }

    // The core configuration deliberately uses compact endpoint strings:
    // provide.service is "tcp://host:port" and consume.expose is
    // "host:port". Keep the form's split fields internal to the page.
    function splitHostPort(value) {
        var text = asString(value).trim()
        if (text.length === 0) return {addr: "", port: ""}
        if (text.charAt(0) === "[") {
            var closing = text.indexOf("]:")
            if (closing > 1)
                return {addr: text.substring(1, closing), port: text.substring(closing + 2)}
        }
        var separator = text.lastIndexOf(":")
        if (separator > 0)
            return {addr: text.substring(0, separator), port: text.substring(separator + 1)}
        return {addr: text, port: ""}
    }

    function parseServiceEndpoint(value) {
        var text = asString(value).trim()
        var marker = text.indexOf("://")
        if (marker <= 0) return {protocol: "", addr: "", port: ""}
        var hostPort = page.splitHostPort(text.substring(marker + 3))
        return {
            protocol: text.substring(0, marker).toLowerCase(),
            addr: hostPort.addr,
            port: hostPort.port
        }
    }

    function endpointText(protocol, addr, port) {
        var host = asString(addr).trim()
        // net.JoinHostPort's equivalent for the QML form. The brackets are
        // required only for IPv6 literals in a host:port string.
        if (host.indexOf(":") >= 0 && host.charAt(0) !== "[") host = "[" + host + "]"
        return asString(protocol).trim().toLowerCase() + "://" + host + ":" + asString(port).trim()
    }

    function hostPortText(addr, port) {
        var host = asString(addr).trim()
        if (host.indexOf(":") >= 0 && host.charAt(0) !== "[") host = "[" + host + "]"
        return host + ":" + asString(port).trim()
    }

    function isDuration(text) {
        return /^([0-9]+(\.[0-9]+)?(ms|s|m|h))+$/.test(String(text).trim())
    }

    function durationMilliseconds(text) {
        var value = String(text).trim()
        if (!page.isDuration(value)) return -1
        var total = 0
        var pattern = /([0-9]+(\.[0-9]+)?)(ms|s|m|h)/g
        var match
        while ((match = pattern.exec(value)) !== null) {
            var multiplier = match[3] === "ms" ? 1
                           : match[3] === "s" ? 1000
                           : match[3] === "m" ? 60000 : 3600000
            total += Number(match[1]) * multiplier
        }
        return total
    }

    function isDurationBetween(text, minimum, maximum) {
        var milliseconds = page.durationMilliseconds(text)
        return milliseconds >= minimum && milliseconds <= maximum
    }

    function isPort(text) {
        var value = String(text).trim()
        if (!/^[0-9]+$/.test(value)) return false
        var number = Number(value)
        return number >= 1 && number <= 65535
    }

    function isIpLiteral(text) {
        var value = String(text).trim()
        var octets = value.split(".")
        if (octets.length === 4) {
            for (var i = 0; i < octets.length; ++i) {
                if (!/^[0-9]+$/.test(octets[i])) return false
                var octet = Number(octets[i])
                if (octet < 0 || octet > 255) return false
            }
            return true
        }
        if (value.indexOf(":") < 0 || !/^[0-9a-fA-F:]+$/.test(value)) return false
        if (value.indexOf("::") !== value.lastIndexOf("::")) return false
        var groups = value.split(":")
        var nonEmpty = 0
        for (var g = 0; g < groups.length; ++g) {
            if (groups[g].length === 0) continue
            if (groups[g].length > 4 || !/^[0-9a-fA-F]+$/.test(groups[g])) return false
            nonEmpty += 1
        }
        return value.indexOf("::") >= 0 ? nonEmpty < 8 : nonEmpty === 8
    }

    function rowIssue(mappingId, port, addr, label, requireIp) {
        if (String(mappingId).trim().length === 0) return label + "缺少映射 ID"
        if (!page.isPort(port)) return label + "端口需在 1-65535 之间"
        if (String(addr).trim().length === 0) return label + "缺少地址"
        if (requireIp && !page.isIpLiteral(addr)) return label + "地址需填写字面 IP"
        return ""
    }

    function mappingIssue(model, label, requireIp) {
        var seen = {}
        for (var i = 0; i < model.count; ++i) {
            var entry = model.get(i)
            if (!entry.active) continue
            var mappingId = asString(entry.mappingId).trim()
            if (!requireIp) {
                var protocol = asString(entry.protocol).trim().toLowerCase()
                if (protocol !== "tcp" && protocol !== "udp")
                    return label + "第 " + (i + 1) + " 项协议只支持 TCP 或 UDP"
            }
            var issue = page.rowIssue(mappingId, entry.port, entry.addr, label + "第 " + (i + 1) + " 项", requireIp)
            if (issue.length > 0) return issue
            if (Object.prototype.hasOwnProperty.call(seen, mappingId)) return label + "的映射 ID「" + mappingId + "」重复"
            seen[mappingId] = true
        }
        return ""
    }

    function validationMessage() {
        serverUrl = serverUrl.trim()
        if (serverUrl.length === 0) return "请填写信令服务器地址"
        if (!/^(ws|wss):\/\/.+/.test(serverUrl)) return "信令地址应以 ws:// 或 wss:// 开头"
        if (room.trim().length === 0) return "请填写房间号"
        if (password.trim().length === 0) return "请填写信令密码"
        if (token.trim().length === 0) return "请填写房间密码"
        if (deviceName.trim().length === 0) return "请填写设备名"
        if (!isDuration(sessionTimeout) || durationMilliseconds(sessionTimeout) <= 0)
            return "会话保留期限格式应类似 10m、30s 或 2h"
        if (preferred !== "ice" && preferred !== "ipv6") return "连接方式无效"
        if (turnMode !== "worker" && turnMode !== "manual" && turnMode !== "off") return "TURN 模式无效"
        if (relayServeUnsupported) return "中继服务仅 CLI 支持"
        if (relayDevice.length > 0 && (!/^[a-zA-Z0-9._-]{1,128}$/.test(relayDevice) || relayDevice === deviceName)) return "中继设备名无效或指向本机"
        var holeOrder = parseRelayOrder(relayOrder)
        for (var hi=0;hi<holeOrder.length;++hi) {
            if (["hole","udp","tcp","tls"].indexOf(holeOrder[hi])<0 || holeOrder.indexOf(holeOrder[hi])!==hi) return "中继顺序只接受不重复的 Hole、UDP、TCP、TLS"
        }
        if (preferred === "ice") {
            if (!isDurationBetween(directProbeTimeout, 100, 120000)) return "直连探测时间需在 100ms 到 2m 之间"
            if (!isDurationBetween(gatherTimeout, 100, 120000)) return "候选收集时间需在 100ms 到 2m 之间"
            if (!isDurationBetween(connectivityTimeout, 100, 120000)) return "连通性检查时间需在 100ms 到 2m 之间"
            if (!isDurationBetween(retryMaxDelay, 100, 120000)) return "最大重试间隔需在 100ms 到 2m 之间"
            if (turnMode !== "off" && !isDurationBetween(turnTtl, 60000, 21600000)) return "TURN 凭据期限需在 1m 到 6h 之间"
            var order = parseRelayOrder(turnOrder)
            var allowedOrder = ["udp", "tcp", "tls"]
            if (order.length > 3) return "TURN 类型顺序最多 3 项"
            for (var oi = 0; oi < order.length; ++oi) {
                if (allowedOrder.indexOf(order[oi]) < 0) return "TURN 类型仅接受 udp、tcp、tls"
                if (order.indexOf(order[oi]) !== oi) return "TURN 类型顺序不能重复"
            }
            if (turnMode === "manual") {
                if (parseList(turnUrls).length === 0) return "TURN 模式为手动时需要填写 TURN 地址"
                if (turnUsername.trim().length === 0) return "手动 TURN 需要填写用户名"
                if (turnCredential.trim().length === 0) return "手动 TURN 需要填写凭据"
            }
        }
        var problem = mappingIssue(provides, "提供映射", false)
        if (problem.length > 0) return problem
        problem = mappingIssue(consumes, "使用映射", true)
        if (problem.length > 0) return problem
        var provideIds = {}
        for (var p = 0; p < provides.count; ++p) {
            var provide = provides.get(p)
            if (provide.active) provideIds[asString(provide.mappingId).trim()] = true
        }
        for (var c = 0; c < consumes.count; ++c) {
            var consume = consumes.get(c)
            var consumeId = asString(consume.mappingId).trim()
            if (consume.active && provideIds[consumeId])
                return "映射 ID「" + consumeId + "」不能同时用于提供和使用"
        }
        return ""
    }

    function normalizeProvide(entry) {
        var source = entry && typeof entry === "object" ? entry : {}
        var serviceValue = source.service
        var service = serviceValue && typeof serviceValue === "object" ? serviceValue : {}
        var parsedService = typeof serviceValue === "string" ? page.parseServiceEndpoint(serviceValue) : {}
        var mappingId = source.mappingId !== undefined ? source.mappingId : source.id
        var protocol = source.protocol !== undefined ? source.protocol
                      : service.protocol !== undefined ? service.protocol : parsedService.protocol
        var addr = source.addr !== undefined ? source.addr
                  : service.addr !== undefined ? service.addr : parsedService.addr
        var port = source.port !== undefined ? source.port
                  : service.port !== undefined ? service.port : parsedService.port
        return {
            mappingId: asString(mappingId).trim(),
            active: asBool(source.active !== undefined ? source.active : source.enabled, true),
            protocol: asString(protocol).trim() || "tcp",
            addr: asString(addr).trim() || "127.0.0.1",
            port: asString(port).trim()
        }
    }

    function normalizeConsume(entry) {
        var source = entry && typeof entry === "object" ? entry : {}
        var exposeValue = source.expose
        var expose = exposeValue && typeof exposeValue === "object" ? exposeValue : {}
        var parsedExpose = typeof exposeValue === "string" ? page.splitHostPort(exposeValue) : {}
        var mappingId = source.mappingId !== undefined ? source.mappingId : source.id
        var addr = source.addr !== undefined ? source.addr
                  : expose.addr !== undefined ? expose.addr : parsedExpose.addr
        var port = source.port !== undefined ? source.port
                  : expose.port !== undefined ? expose.port : parsedExpose.port
        return {
            mappingId: asString(mappingId).trim(),
            active: asBool(source.active !== undefined ? source.active : source.enabled, true),
            protocol: "tcp",
            addr: asString(addr).trim() || "127.0.0.1",
            port: asString(port).trim()
        }
    }

    function mappingState() {
        var state = {provide: [], consume: []}
        for (var p = 0; p < provides.count; ++p) {
            var provide = provides.get(p)
            state.provide.push({
                mappingId: asString(provide.mappingId),
                active: asBool(provide.active, true),
                protocol: asString(provide.protocol),
                addr: asString(provide.addr),
                port: asString(provide.port)
            })
        }
        for (var c = 0; c < consumes.count; ++c) {
            var consume = consumes.get(c)
            state.consume.push({
                mappingId: asString(consume.mappingId),
                active: asBool(consume.active, true),
                addr: asString(consume.addr),
                port: asString(consume.port)
            })
        }
        return state
    }

    // ------------------------------------------------------------ config I/O

    function readConfig() {
        var config = null
        try {
            config = JSON.parse(holePlugin.configJson)
        } catch (error) {
            console.warn("hole_plugin: 插件配置不是合法 JSON：" + error)
        }
        if (!config || typeof config !== "object") config = {}

        serverUrl = asString(holePlugin.serverUrl)
        password = asString(config.password)
        room = asString(config.room)
        token = asString(config.token)
        deviceName = asString(config.device_name) || "pen"
        sessionTimeout = asString(config.session_timeout) || "10m"

        var transport = config.transport && typeof config.transport === "object" ? config.transport : {}
        preferred = asString(transport.preferred) || "ice"
        allowLegacy = asBool(transport.allow_legacy, true)
        allowInsecureSignal = asBool(transport.allow_insecure_signal, false)

        var ice = config.ice && typeof config.ice === "object" ? config.ice : null
        portMapping = ice && ice.port_mapping ? ice.port_mapping : "auto"
        natStunUrls = ice && Array.isArray(ice.nat_stun_urls) ? ice.nat_stun_urls : null
        stunUrls = ice ? formatList(ice.stun_urls) : defaultStun
        directProbeTimeout = ice && asString(ice.direct_probe_timeout) ? asString(ice.direct_probe_timeout) : "3s"
        gatherTimeout = ice && asString(ice.gather_timeout) ? asString(ice.gather_timeout) : "6s"
        connectivityTimeout = ice && asString(ice.connectivity_timeout) ? asString(ice.connectivity_timeout) : "10s"
        retryMaxDelay = ice && asString(ice.retry_max_delay) ? asString(ice.retry_max_delay) : "15s"
        interfaceAllowlist = ice ? formatList(ice.interface_allowlist) : ""
        includeLoopback = ice ? asBool(ice.include_loopback, false) : false
        relayOnly = ice ? asBool(ice.relay_only, false) : false

        var turn = config.turn && typeof config.turn === "object" ? config.turn : {}
        // 运行配置会清空未启用的 TURN 字段，表单优先恢复本地编辑状态。
        if (holePlugin.turnStateJson && holePlugin.turnStateJson.length > 0) {
            try {
                var savedTurn = JSON.parse(holePlugin.turnStateJson)
                if (savedTurn && typeof savedTurn === "object" && !Array.isArray(savedTurn)) turn = savedTurn
            } catch (error) {
                console.warn("hole_plugin: TURN 编辑状态不是合法 JSON：" + error)
            }
        }
        turnMode = asString(turn.mode) || "worker"
        turnOrder = formatList(parseRelayOrder(formatList(turn.order)))
        var relay = config.relay && typeof config.relay === "object" ? config.relay : {}
        relayDevice = asString(relay.device)
        relayOrderExplicit = Array.isArray(relay.order)
        relayOrder = relayOrderExplicit ? formatList(relay.order) : ((relayDevice.length > 0 ? "hole, " : "") + (turnOrder || "udp, tcp, tls"))
        relayServeUnsupported = relay.serve && asBool(relay.serve.enabled, false) ? true : false
        turnTtl = asString(turn.ttl) || "6h"
        turnUrls = formatList(turn.urls)
        turnUsername = asString(turn.username)
        turnCredential = asString(turn.credential)

        candidateAddresses = formatList(config.candidate_addresses)

        // mapping_state keeps disabled and partially edited rows out of the
        // runtime config without losing them when the page is reopened.
        var savedMappings = null
        if (holePlugin.mappingStateJson && holePlugin.mappingStateJson.length > 0) {
            try {
                savedMappings = JSON.parse(holePlugin.mappingStateJson)
            } catch (error) {
                console.warn("hole_plugin: 映射编辑状态不是合法 JSON：" + error)
            }
        }

        provides.clear()
        var provideEntries = savedMappings && Array.isArray(savedMappings.provide)
                             ? savedMappings.provide
                             : (Array.isArray(config.provide) ? config.provide : [])
        for (var p = 0; p < provideEntries.length; ++p) provides.append(page.normalizeProvide(provideEntries[p]))

        consumes.clear()
        var consumeEntries = savedMappings && Array.isArray(savedMappings.consume)
                             ? savedMappings.consume
                             : (Array.isArray(config.consume) ? config.consume : [])
        for (var c = 0; c < consumeEntries.length; ++c) consumes.append(page.normalizeConsume(consumeEntries[c]))

        initialized = true
        page.refreshStatus()
    }

    function collectMappings(model, isConsume) {
        var rows = []
        for (var i = 0; i < model.count; ++i) {
            var entry = model.get(i)
            if (!entry.active) continue
            var mappingId = asString(entry.mappingId).trim()
            if (mappingId.length === 0 || !isPort(entry.port)) continue
            var port = Number(String(entry.port).trim())
            var addr = asString(entry.addr).trim()
            if (isConsume) rows.push({id: mappingId, expose: page.hostPortText(addr, port)})
            else rows.push({id: mappingId, service: page.endpointText(entry.protocol, addr, port)})
        }
        return rows
    }

    function syncConfig() {
        if (!initialized) return
        localError = ""
        saveState = ""
        var iceActive = preferred === "ice"
        var turnActive = iceActive && turnMode !== "off"
        var manualTurn = turnActive && turnMode === "manual"
        holePlugin.mappingStateJson = JSON.stringify(page.mappingState())
        var turnState = {
            mode: turnMode,
            ttl: turnTtl,
            urls: parseList(turnUrls),
            username: turnUsername,
            credential: turnCredential,
            order: parseRelayOrder(turnOrder)
        }
        holePlugin.turnStateJson = JSON.stringify(turnState)
        var payload = {
            room: room,
            password: password,
            token: token,
            device_name: deviceName,
            session_timeout: sessionTimeout,
            candidate_addresses: preferred === "ipv6" ? parseList(candidateAddresses) : [],
            transport: {
                preferred: preferred,
                allow_legacy: allowLegacy,
                allow_insecure_signal: allowInsecureSignal
            },
            ice: {
                port_mapping: portMapping,
                nat_stun_urls: natStunUrls,
                // The core validates the complete transport object even when
                // IPv6 is selected. Do not carry hidden, stale ICE values into
                // that mode.
                stun_urls: iceActive ? parseList(stunUrls) : [page.defaultStun],
                direct_probe_timeout: iceActive ? directProbeTimeout : "3s",
                gather_timeout: iceActive ? gatherTimeout : "6s",
                connectivity_timeout: iceActive ? connectivityTimeout : "10s",
                retry_max_delay: iceActive ? retryMaxDelay : "15s",
                interface_allowlist: iceActive ? parseList(interfaceAllowlist) : [],
                include_loopback: iceActive && includeLoopback,
                relay_only: iceActive && relayOnly
            },
            turn: {
                mode: iceActive ? turnMode : "off",
                ttl: turnActive ? turnTtl : "6h",
                urls: manualTurn ? turnState.urls : [],
                username: manualTurn ? turnState.username : "",
                credential: manualTurn ? turnState.credential : "",
                order: iceActive ? turnState.order : []
            },
            provide: page.collectMappings(provides, false),
            consume: page.collectMappings(consumes, true)
        }
        payload.relay = {device: relayDevice, order: relayOrderExplicit ? parseRelayOrder(relayOrder) : null}
        if (relayServeUnsupported) payload.relay.serve = {enabled: true}
        holePlugin.serverUrl = asString(serverUrl).trim()
        holePlugin.configJson = JSON.stringify(payload)
        autosave.restart()
    }

    function saveNow() {
        if (!initialized) return
        if (asString(serverUrl).trim().length === 0) {
            saveState = "未保存：缺少信令服务器地址"
            return
        }
        if (!page.runRequested) {
            saveState = holePlugin.saveConfig() ? "已保存" : "保存失败"
            return
        }

        var problem = page.validationMessage()
        if (problem.length > 0) {
            saveState = holePlugin.saveConfig()
                        ? "已保存，暂未应用：" + problem
                        : "保存失败"
            return
        }

        saveState = holePlugin.applyConfig() ? "已保存，正在应用" : "保存失败"
    }

    // --------------------------------------------------------- mapping edits

    function addProvide() {
        provides.append({mappingId: "", active: true, protocol: "tcp", addr: "127.0.0.1", port: ""})
        page.syncConfig()
    }

    function addConsume() {
        consumes.append({mappingId: "", active: true, protocol: "tcp", addr: "127.0.0.1", port: ""})
        page.syncConfig()
    }

    function setProvide(index, property, value) {
        provides.setProperty(index, property, value)
        page.syncConfig()
    }

    function setConsume(index, property, value) {
        consumes.setProperty(index, property, value)
        page.syncConfig()
    }

    function removeProvide(index) {
        provides.remove(index)
        page.syncConfig()
    }

    function removeConsume(index) {
        consumes.remove(index)
        page.syncConfig()
    }

    // ---------------------------------------------------------- child pages

    function closeSeparatePage() {
        page.pageRequestToken += 1
        var child = page.openedPage
        page.openedPage = null
        if (child) {
            child.visible = false
            Qt.callLater(function() {
                if (child) child.destroy()
            })
        }
    }

    function openSeparatePage(fileName, properties) {
        page.closeSeparatePage()
        var token = page.pageRequestToken
        var component = Qt.createComponent(Qt.resolvedUrl(fileName))
        if (component.status === Component.Error) {
            page.localError = "无法打开页面：" + component.errorString()
            return
        }

        function attach() {
            if (token !== page.pageRequestToken || component.status !== Component.Ready) {
                component.destroy()
                return
            }
            var child = component.createObject(page, properties || {})
            if (!child) {
                component.destroy()
                page.localError = "无法创建页面：" + fileName
                return
            }
            child.z = 100
            page.openedPage = child
            if (child.hasOwnProperty("backButtonClickedCallback")) {
                child.backButtonClickedCallback.connect(function() {
                    if (page.openedPage === child) page.openedPage = null
                })
            }
            child.Component.destruction.connect(function() {
                if (page.openedPage === child) page.openedPage = null
                component.destroy()
            })
            child.show()
        }

        if (component.status === Component.Ready) {
            attach()
        } else {
            var onStatusChanged = function() {
                if (component.status === Component.Loading) return
                component.statusChanged.disconnect(onStatusChanged)
                if (component.status === Component.Ready) attach()
                else {
                    var reason = component.errorString()
                    component.destroy()
                    page.localError = "无法打开页面：" + reason
                }
            }
            component.statusChanged.connect(onStatusChanged)
        }
    }

    // ---------------------------------------------------------------- actions

    function stateTitle() {
        if (holePlugin.reloading) return "正在重载核心"
        if (page.connectionEstablished) return "连接成功"
        if (holePlugin.state === "starting") return "正在连接"
        if (holePlugin.state === "reconfiguring") return "正在应用配置"
        if (holePlugin.state === "recovering") return "正在恢复"
        if (holePlugin.state === "stopping") return "正在停止"
        if (holePlugin.state === "error") return "连接异常"
        if (holePlugin.running) return "正在连接"
        return "尚未连接"
    }

    function beginConnection() {
        if (holePlugin.reloading) return
        if (holePlugin.state === "starting" || holePlugin.state === "stopping") return
        var problem = page.validationMessage()
        localError = problem
        if (problem.length > 0) return
        localError = ""
        page.syncConfig()
        if (holePlugin.running) holePlugin.applyConfig()
        else holePlugin.start()
    }

    function stopConnection() {
        localError = ""
        holePlugin.stop()
    }

    function reloadCore() {
        localError = ""
        holePlugin.reloadCore()
    }

    function renominateTransports() {
        if (!page.connectionEstablished || holePlugin.state === "stopping") return
        localError = ""
        holePlugin.renominateTransports()
    }

    function clearMessages() {
        localError = ""
        holePlugin.clearError()
    }

    function refreshSnapshot() {
        if (!holePlugin.reloading) holePlugin.refresh()
    }

    function stateText(value) {
        var states = {
            starting: "正在启动",
            running: "运行中",
            reconfiguring: "重新配置中",
            reloading: "重载核心中",
            recovering: "恢复中",
            stopping: "正在停止",
            stopped: "已停止",
            error: "错误",
            connecting: "连接中",
            joining: "加入房间",
            joined: "已加入房间",
            reconnecting: "重新连接中",
            disconnected: "未连接",
            waiting_peer: "等待对端",
            active: "已建立",
            waiting_credentials: "等待中继凭据",
            switching: "正在切换路径",
            paused: "对端暂时离线",
            connecting_peer: "连接对端",
            unavailable: "不可用",
            requesting: "请求中",
            pending: "等待中"
        }
        var text = asString(value)
        return states[text] || (text.length > 0 ? text : "未知")
    }

    function boolText(value) {
        return value ? "是" : "否"
    }

    function bytesText(value) {
        var number = Number(value)
        if (!isFinite(number) || number < 0) return "0 B"
        var units = ["B", "KB", "MB", "GB"]
        var unit = 0
        while (number >= 1024 && unit < units.length - 1) {
            number /= 1024
            unit += 1
        }
        var digits = unit === 0 ? 0 : number >= 10 ? 1 : 2
        return number.toFixed(digits) + " " + units[unit]
    }

    function appendSnapshotLine(lines, label, text) {
        var value = asString(text)
        if (value.length > 0) lines.push({label: label, text: value})
    }

    function counterText(value, unit) {
        // Core counters are uint64 strings. Keep all digits and distinguish
        // a missing counter from an explicitly reported zero.
        var text = asString(value)
        return /^[0-9]+$/.test(text) ? text + unit : "未上报"
    }

    function sendLossText(loss) {
        if (!loss || typeof loss !== "object") return "—"
        if (loss.rate_percent === null) return "暂无样本"
        var rate = loss.rate_percent
        if (typeof rate !== "number" || !isFinite(rate) || rate < 0 || rate > 100) return "—"
        return rate.toFixed(2) + "%"
    }

    function quicLossText(peer) {
        return "合计 " + (peer.quic_loss ? sendLossText(peer.quic_loss) : "暂无反馈")
                + " · 上行 " + sendLossText(peer.send_loss)
                + " · 下行 " + (peer.receive_loss ? sendLossText(peer.receive_loss) : "暂无反馈")
    }

    function bandwidthText(bandwidth, peak) {
        if (!bandwidth || typeof bandwidth !== "object") return "—"
        var key = peak ? "peak_bps" : "average_bps"
        var parts = []
        var keys = ["total", "send", "receive"]
        var labels = ["合计", "上行", "下行"]
        for (var i = 0; i < keys.length; ++i) {
            var rates = bandwidth[keys[i]]
            var value = rates && rates[key]
            if (typeof value !== "number" || !isFinite(value) || value < 0) return "—"
            parts.push(labels[i] + " " + (value / 1000000).toFixed(2).replace(/\.?0+$/, "") + " Mbps")
        }
        return parts.join(" · ")
    }

    function mappingText(mapping) {
        var parts = [mapping.role === "provide" ? "提供" : "使用", stateText(mapping.state)]
        if (asString(mapping.protocol).length > 0) parts.push(asString(mapping.protocol).toUpperCase())
        if (asString(mapping.endpoint).length > 0) parts.push(asString(mapping.endpoint))
        if (asString(mapping.peer).length > 0) parts.push("对端 " + asString(mapping.peer))
        var protocol = asString(mapping.protocol).toLowerCase()
        if (protocol === "tcp")
            parts.push("TCP " + asString(mapping.tcp_sessions || "0") + " 个会话")
        else if (protocol === "udp")
            parts.push("UDP " + asString(mapping.udp_sessions || "0") + " 个会话")
        else
            parts.push("会话 " + asString((mapping.tcp_sessions || 0) + (mapping.udp_sessions || 0)) + " 个")
        var readBytes = mapping.read_bytes !== undefined ? mapping.read_bytes : mapping.tcp_read_bytes
        var writtenBytes = mapping.written_bytes !== undefined ? mapping.written_bytes : mapping.tcp_written_bytes
        // read_bytes is data read from the local endpoint and sent to the peer;
        // written_bytes is data received from the peer and written locally.
        parts.push("收 " + bytesText(writtenBytes) + " / 发 " + bytesText(readBytes))
        if (mapping.error && asString(mapping.error.message).length > 0)
            parts.push("错误：" + asString(mapping.error.message))
        return parts.join(" · ")
    }

    function phaseText(phase) {
        var labels = {
            fixed: "连接固定入口",
            direct: "探测直连路径",
            hole: "连接 Hole 中继",
            relay_udp: "尝试 UDP 中继",
            relay_tls_443: "尝试 TLS 中继 · 443 端口",
            relay_tls: "尝试 TLS 中继 · 5349 / 自定义端口",
            relay_tcp_80: "尝试 TCP 中继 · 80 端口",
            relay_tcp: "尝试 TCP 中继 · 3478 / 自定义端口",
            relay_wait: "等待对端的 TURN 中继"
        }
        var text = asString(phase)
        return labels[text] || (text.length > 0 ? text : "正在选择连接路径")
    }

    function relayAccessText(protocol, side) {
        var value = asString(protocol).toLowerCase()
        if (value === "udp") return "UDP 中继"
        if (value === "tcp") return "TCP 中继"
        if (value === "tls") return "TLS 中继"
        if (side === "remote") return "中继（接入协议未上报）"
        return "中继（接入协议待确认）"
    }

    function relaySideText(side) {
        var value = asString(side)
        if (value === "local") return "本机"
        if (value === "remote") return "对端"
        if (value === "both") return "本机与对端"
        return "等待确认"
    }

    function candidateAccessText(candidateType, protocol, side) {
        var type = asString(candidateType).toLowerCase()
        if (type === "fixed") return "固定入口直连"
        if (asString(protocol).length > 0) return relayAccessText(protocol, side)
        if (type === "relay") return relayAccessText(protocol, side)
        if (type === "host" || type === "srflx" || type === "prflx") return "直连"
        return type.length > 0 ? type : "路径未上报"
    }

    function localAccessText(peer) {
        var protocol = asString(peer.local_relay_protocol)
        if (protocol.length === 0) protocol = asString(peer.relay_protocol)
        return candidateAccessText(peer.local_type, protocol, "local")
    }

    function remoteAccessText(peer) {
        return candidateAccessText(peer.remote_type, peer.remote_relay_protocol, "remote")
    }

    function accessWithAddress(access, address) {
        var value = asString(access)
        var endpoint = asString(address)
        return value + (endpoint.length > 0 ? " · " + endpoint : "")
    }

    function peerPathText(peer) {
        var path = asString(peer.path_type)
        var family = asString(peer.address_family)
        if (page.isFixedPeer(peer)) return "固定入口直连" + (family.length > 0 ? " · " + family : "")
        if (path === "hole") return "Hole 中继 · " + asString(peer.relay_device)
        if (path === "relay") return "TURN 中继" + (family.length > 0 ? " · " + family : "")
        if (path === "direct") return "设备直连" + (family.length > 0 ? " · " + family : "")
        return "路径未选定"
    }

    function isFixedPeer(peer) {
        return asString(peer.profile) === "fixed-quic-mux-v1"
                || asString(peer.phase) === "fixed" || asString(peer.path_type) === "fixed"
    }

    function fixedIssueText(code) {
        if (code === "fixed_path_failed")
            return "固定入口连接失败，仅重试固定入口，不会切换到 ICE 或 TURN。请检查公布地址、UDP 端口和监听端。"
        if (code === "fixed_relay_conflict")
            return "固定入口直连与任一端的“仅中继”要求冲突，请调整固定入口或仅中继配置。"
        return ""
    }

    function peerPhaseText(peer) {
        var path = asString(peer.path_type)
        var state = asString(peer.state)
        var phase = asString(peer.phase)
        var pending = asString(peer.pending_phase)
        if (page.isFixedPeer(peer)) return page.phaseText("fixed")
        if (pending.length > 0 && pending !== phase)
            return "下一阶段：" + page.phaseText(pending)
        if (phase.length > 0 && path !== "direct" && path !== "relay")
            return page.phaseText(phase)
        if (phase.length > 0 && state !== "active" && path !== "direct")
            return "当前阶段：" + page.phaseText(phase)
        return ""
    }

    function appendPeerSnapshotLines(lines, peer) {
        appendSnapshotLine(lines, "  本机 NAT", natText(peer.local_nat, true))
        appendSnapshotLine(lines, "  对端 NAT", natText(peer.peer_nat, false))
        var peerID = asString(peer.peer_id)
        appendSnapshotLine(lines, "通道 · " + peerID, stateText(peer.state))
        appendSnapshotLine(lines, "  路径", page.peerPathText(peer))
        var path = asString(peer.path_type)
        var fixed = page.isFixedPeer(peer)
        if (asString(peer.relay_device).length > 0) {
            appendSnapshotLine(lines, "  Hole 中继设备", peer.relay_device)
            appendSnapshotLine(lines, "  公布入口", peer.relay_endpoint)
            appendSnapshotLine(lines, "  实际接入地址", peer.relay_access_address)
        }
        if (fixed) {
            appendSnapshotLine(lines, "  公布入口", peer.fixed_endpoint)
            appendSnapshotLine(lines, "  监听方", peer.listener_id)
            appendSnapshotLine(lines, "  拨号方", peer.dialer_id)
            if (asString(peer.local_address).length > 0)
                appendSnapshotLine(lines, "  本机连接", page.accessWithAddress(page.candidateAccessText(peer.local_type, "", "local"), peer.local_address))
            if (asString(peer.remote_address).length > 0)
                appendSnapshotLine(lines, "  对端连接", page.accessWithAddress(page.candidateAccessText(peer.remote_type, "", "remote"), peer.remote_address))
        } else if (path === "relay" || path === "direct") {
            var localAccess = path === "relay"
                    ? page.localAccessText(peer)
                    : page.candidateAccessText(peer.local_type, "", "local")
            var remoteAccess = path === "relay"
                    ? page.remoteAccessText(peer)
                    : page.candidateAccessText(peer.remote_type, "", "remote")
            appendSnapshotLine(lines, "  本机连接", page.accessWithAddress(localAccess, peer.local_address))
            appendSnapshotLine(lines, "  对端连接", page.accessWithAddress(remoteAccess, peer.remote_address))
        }
        if (!fixed && path === "relay") {
            appendSnapshotLine(lines, "  中继使用方", page.relaySideText(peer.relay_side))
        }
        appendSnapshotLine(lines, fixed ? "  连接阶段" : "  探测阶段", page.peerPhaseText(peer))
        if (!fixed && (Array.isArray(peer.relay_order) || Array.isArray(peer.peer_relay_order))) {
            appendSnapshotLine(lines, "  本机中继顺序", page.snapshotRelayOrder(peer.relay_order))
            appendSnapshotLine(lines, "  对端中继顺序", page.snapshotRelayOrder(peer.peer_relay_order))
            if (asBool(peer.relay_order_fallback, false)) appendSnapshotLine(lines, "  顺序状态", "不支持自定义顺序，已回退默认顺序")
            else if (Number(peer.relay_round) > 0) appendSnapshotLine(lines, "  共享中继轮次", "第 " + asString(peer.relay_round) + " 轮")
        }
        if (Number(peer.rtt_ms) > 0) appendSnapshotLine(lines, "  延迟", asString(peer.rtt_ms) + " ms")
        appendSnapshotLine(lines, "  线路流量", "收 " + bytesText(peer.bytes_received) + " / 发 " + bytesText(peer.bytes_sent))
        appendSnapshotLine(lines, "  QUIC 统计", "丢包率近 1 分钟 · 速率本次连接")
        appendSnapshotLine(lines, "  丢包率", page.quicLossText(peer))
        appendSnapshotLine(lines, "  峰值速率", page.bandwidthText(peer.bandwidth, true))
        appendSnapshotLine(lines, "  平均速率", page.bandwidthText(peer.bandwidth, false))
        appendSnapshotLine(lines, "  连接重试", page.counterText(peer.retry_count, " 次"))
        appendSnapshotLine(lines, "  丢弃报文", page.counterText(peer.dropped_datagrams, " 个"))
        if (Number(peer.active_channels) > 0)
            appendSnapshotLine(lines, "  活动通道", asString(peer.active_channels) + " 个")
        if (peer.error && asString(peer.error.message).length > 0)
            appendSnapshotLine(lines, "  错误", asString(peer.error.message))
        if (peer.error) appendSnapshotLine(lines, "  连接提示", page.fixedIssueText(peer.error.code))
    }

    function natText(n, local) {
        if (!n || !n.status) return local ? "信息不足" : "对端未上报"
        var states = {probing: "探测中", insufficient: "信息不足", expired: "已过期", not_applicable: "IPv4 不适用"}
        var kinds = {open: "公网直连", firewall: "防火墙过滤", full_cone: "完全锥型", restricted_cone: "地址受限锥型", port_restricted_cone: "端口受限锥型", symmetric: "对称型", endpoint_independent: "端点无关映射"}
        var patterns = {stable: "映射稳定", predictable_incremental: "可预测（递增）", predictable_decremental: "可预测（递减）", incremental: "可能可预测（递增）", decremental: "可能可预测（递减）", irregular: "未观察到规律"}
        return (states[n.status] ? states[n.status] + " · " : "") + (kinds[n.type] || "未知") + " · " + (patterns[n.predictability] || "可预测性未知") + " · " + NATTime.format(n.probed_at || "暂无探测时间")
    }

    function orderedPeers(peers) {
        var result = peers.slice(0)
        result.sort(function(left, right) {
            var leftPeer = asString(left.peer_id)
            var rightPeer = asString(right.peer_id)
            if (leftPeer !== rightPeer) return leftPeer < rightPeer ? -1 : 1
            var leftTransport = asString(left.transport_id)
            var rightTransport = asString(right.transport_id)
            if (leftTransport === rightTransport) return 0
            return leftTransport < rightTransport ? -1 : 1
        })
        return result
    }

    function humanSnapshot(data) {
        var lines = []
        appendSnapshotLine(lines, "核心状态", stateText(data.engine_state))
        appendSnapshotLine(lines, "信令状态", stateText(data.signal_state))
        appendSnapshotLine(lines, "配置", data.configured ? "已配置" : "未配置")
        appendSnapshotLine(lines, "运行请求", data.run_requested ? "运行中" : "已停止")
        appendSnapshotLine(lines, "核心版本", asString(data.core_version))
        appendSnapshotLine(lines, "核心构建", asString(data.core_build_id))
        appendSnapshotLine(lines, "启动时间", asString(data.started_at))
        appendSnapshotLine(lines, "连接代次", asString(data.generation))
        appendSnapshotLine(lines, "重连次数", asString(data.reconnects) || "0")
        appendSnapshotLine(lines, "网络变化", asString(data.network_changes) || "0")
        appendSnapshotLine(lines, "丢弃事件", asString(data.events_dropped) || "0")

        var capabilities = data.capabilities
        if (capabilities && typeof capabilities === "object") {
            var transport = asString(capabilities.transport)
            if (capabilities.requires_ipv6) transport += " · 需要 IPv6"
            appendSnapshotLine(lines, "传输方式", transport)
            appendSnapshotLine(lines, "网络绑定", capabilities.network_binding ? "支持" : "不支持")
        }

        var network = data.network
        if (network) appendSnapshotLine(lines, "本机 NAT", natText(network.nat, true))
        if (network && typeof network === "object") {
            var networkParts = [network.available ? "可用" : "不可用"]
            if (asString(network.transport).length > 0) networkParts.push(asString(network.transport))
            if (asString(network.interface).length > 0) networkParts.push("网卡 " + asString(network.interface))
            if (Array.isArray(network.addresses) && network.addresses.length > 0)
                networkParts.push(network.addresses.join(", "))
            appendSnapshotLine(lines, "当前网络", networkParts.join(" · "))
        }

        if (data.error && asString(data.error.message).length > 0)
            appendSnapshotLine(lines, "核心错误", asString(data.error.message))
        if (data.error) appendSnapshotLine(lines, "连接提示", page.fixedIssueText(data.error.code))

        if (Array.isArray(data.mappings)) {
            appendSnapshotLine(lines, "映射数量", data.mappings.length + " 项")
            for (var m = 0; m < data.mappings.length; ++m) {
                var mapping = data.mappings[m]
                appendSnapshotLine(lines, "映射 · " + asString(mapping.id), page.mappingText(mapping))
            }
        }
        if (Array.isArray(data.peer_transports)) {
            var peers = page.orderedPeers(data.peer_transports)
            appendSnapshotLine(lines, "对端通道", peers.length + " 条")
            for (var p = 0; p < peers.length; ++p) {
                var peer = peers[p]
                page.appendPeerSnapshotLines(lines, peer)
            }
        }
        return lines
    }

    function peerDetailLines(peer) {
        var lines = []
        page.appendPeerSnapshotLines(lines, peer)
        var summaryLabels = ["路径", "延迟", "线路流量", "QUIC 统计", "丢包率", "峰值速率", "平均速率"]
        return lines.filter(function(line) { return line.label.indexOf("通道 · ") !== 0 && summaryLabels.indexOf(line.label.trim()) < 0 })
    }

    function refreshStatus() {
        var raw = holePlugin.snapshotJson
        if (raw === lastSnapshotRaw) return
        lastSnapshotRaw = raw
        snapshotRevision++
        snapshotError = ""
        if (raw.length === 0) {
            runtimeSnapshot = ({})
            natReportKey = ""
            natReport = {state: "idle", title: "检测当前网络", summary: "检查映射、过滤与端口特征，无需加入房间。", sections: []}
            return
        }
        decodeSnapshot()
    }

    function decodeSnapshot() {
        // Coalesce bursts while the worker is busy; never queue an unbounded
        // backlog or publish a reply from a core that has since been reloaded.
        if (snapshotDecoding || lastSnapshotRaw.length === 0) return
        snapshotDecoding = true
        dispatchedRevision = snapshotRevision
        snapshotWorker.sendMessage({revision: snapshotRevision, json: lastSnapshotRaw})
    }

    WorkerScript {
        id: snapshotWorker
        source: "components/SnapshotWorker.js"
        onMessage: {
            page.snapshotDecoding = false
            if (messageObject.revision === page.snapshotRevision) {
                page.runtimeSnapshot = messageObject.snapshot
                page.snapshotError = messageObject.error
                if (page.natReportKey !== messageObject.natKey) {
                    page.natReportKey = messageObject.natKey
                    page.natReport = messageObject.snapshot.nat_diagnostic || {state: "idle", title: "检测当前网络", summary: "检查映射、过滤与端口特征，无需加入房间。", sections: []}
                }
            }
            if (page.dispatchedRevision !== page.snapshotRevision) page.decodeSnapshot()
        }
    }

    // ------------------------------------------------------------------- view

    Rectangle { anchors.fill: parent; color: Theme.background }
    VirtualKeyboard { id: keyboard }
    PageEntrance { id: pageEntrance; target: scroll }
    onCurrentViewChanged: pageEntrance.restart()

    YVerticalTitleBar {
        z: 100
        onCallBack: {
            if (page.currentView !== "home") { page.currentView = "home"; scroll.contentY = 0 }
            else page.backButtonClicked()
        }
    }

    Flickable {
        id: scroll
        anchors.fill: parent
        anchors.leftMargin: 54; anchors.rightMargin: 8; anchors.topMargin: 8; anchors.bottomMargin: 8
        clip: true
        contentWidth: width
        contentHeight: content.height
        boundsBehavior: Flickable.StopAtBounds

        ColumnLayout {
            id: content
            width: scroll.width
            spacing: 8
            HomePanel {
                Layout.fillWidth: true
                visible: page.currentView === "home"
                stateTitle: page.stateTitle()
                identity: page.room.length > 0 ? page.deviceName + " · " + page.room : "先设置房间与设备"
                connected: page.connectionEstablished
                requested: page.runRequested
                busy: holePlugin.reloading || holePlugin.state === "stopping"
                snapshot: page.runtimeSnapshot
                errorText: page.errorText
                onToggleRequested: { if (page.runRequested) page.stopConnection(); else page.beginConnection() }
                onReloadRequested: page.reloadCore()
                onNavigate: {
                    if (destination === "devices") {
                        page.currentView = "devices"
                        scroll.contentY = 0
                        page.refreshSnapshot()
                    } else if (destination === "settings") {
                        page.openSeparatePage("SettingsPage.qml", {form: page, hostKeyboard: keyboard})
                    } else if (destination === "services") {
                        page.openSeparatePage("ConfigPage.qml", {form: page, hostKeyboard: keyboard, provideModel: provides, consumeModel: consumes})
                    } else if (destination === "network") {
                        page.openSeparatePage("NATPage.qml", {form: page})
                    }
                }
            }
            Loader {
                Layout.fillWidth: true
                Layout.preferredHeight: item ? item.implicitHeight : 0
                active: page.currentView === "devices"
                visible: active
                sourceComponent: RuntimePanel {
                    width: scroll.width
                    form: page
                    snapshot: page.runtimeSnapshot
                    connected: page.connectionEstablished
                    busy: holePlugin.reloading || holePlugin.state === "stopping"
                    onRefreshRequested: page.refreshSnapshot()
                    onReconnectRequested: page.renominateTransports()
                }
            }
            ActionButton {
                Layout.fillWidth: true
                visible: page.errorText.length > 0
                text: "清除提示"; iconName: "close"; variant: "link"
                onClicked: page.clearMessages()
            }
        }
    }

    Timer {
        id: autosave
        interval: 600
        onTriggered: page.saveNow()
    }

    Timer {
        id: statusPoller
        interval: 2000
        repeat: true
        running: holePlugin.running || page.natReport.state === "queued" || page.natReport.state === "probing"
        onTriggered: holePlugin.refresh()
    }

    Connections {
        target: holePlugin
        function onSnapshotChanged() { page.refreshStatus() }
        function onRunningChanged() { if (holePlugin.running) page.refreshSnapshot() }
    }

    Component.onCompleted: page.readConfig()
}
