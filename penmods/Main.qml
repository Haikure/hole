import QtQuick 2.12
import QtQuick.Layouts 1.12
import "components"

Item {
    id: page
    // PenMods normally supplies the viewport size. Keep a small fallback for
    // preview tools, but never pin the form to the preview dimensions.
    width: parent ? parent.width : 320
    height: parent ? parent.height : 170
    implicitWidth: 320
    implicitHeight: 170

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
    property string turnMode: "worker"
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
    property bool statusOpen: false
    property var openedPage: null
    property int pageRequestToken: 0
    property string localError: ""
    property string saveState: ""
    property var statusLines: []
    property string statusRaw: ""

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
        if (preferred === "ice") {
            if (!isDurationBetween(directProbeTimeout, 100, 120000)) return "直连探测时间需在 100ms 到 2m 之间"
            if (!isDurationBetween(gatherTimeout, 100, 120000)) return "候选收集时间需在 100ms 到 2m 之间"
            if (!isDurationBetween(connectivityTimeout, 100, 120000)) return "连通性检查时间需在 100ms 到 2m 之间"
            if (!isDurationBetween(retryMaxDelay, 100, 120000)) return "最大重试间隔需在 100ms 到 2m 之间"
            if (turnMode !== "off" && !isDurationBetween(turnTtl, 60000, 21600000)) return "TURN 凭据期限需在 1m 到 6h 之间"
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
        stunUrls = ice ? formatList(ice.stun_urls) : defaultStun
        directProbeTimeout = ice && asString(ice.direct_probe_timeout) ? asString(ice.direct_probe_timeout) : "3s"
        gatherTimeout = ice && asString(ice.gather_timeout) ? asString(ice.gather_timeout) : "6s"
        connectivityTimeout = ice && asString(ice.connectivity_timeout) ? asString(ice.connectivity_timeout) : "10s"
        retryMaxDelay = ice && asString(ice.retry_max_delay) ? asString(ice.retry_max_delay) : "15s"
        interfaceAllowlist = ice ? formatList(ice.interface_allowlist) : ""
        includeLoopback = ice ? asBool(ice.include_loopback, false) : false
        relayOnly = ice ? asBool(ice.relay_only, false) : false

        var turn = config.turn && typeof config.turn === "object" ? config.turn : {}
        turnMode = asString(turn.mode) || "worker"
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
                urls: manualTurn ? parseList(turnUrls) : [],
                username: manualTurn ? turnUsername : "",
                credential: manualTurn ? turnCredential : ""
            },
            provide: page.collectMappings(provides, false),
            consume: page.collectMappings(consumes, true)
        }
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
        saveState = holePlugin.saveConfig() ? "已保存" : "保存失败"
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
        if (page.connectionEstablished) return "连接成功"
        if (holePlugin.state === "starting") return "正在连接"
        if (holePlugin.state === "reconfiguring") return "正在重连"
        if (holePlugin.state === "recovering") return "正在恢复"
        if (holePlugin.state === "stopping") return "正在停止"
        if (holePlugin.state === "error") return "连接异常"
        if (holePlugin.running) return "正在连接"
        return "尚未连接"
    }

    function stateDetail() {
        if (page.connectionEstablished) {
            return "连接成功，正在运行 " + provides.count + " 项提供与 " + consumes.count + " 项使用映射"
        }
        if (holePlugin.state === "starting") return "正在建立连接，请稍候"
        if (holePlugin.state === "reconfiguring") return "配置已更新，正在重新建立连接，请稍候"
        if (holePlugin.state === "recovering") return "连接已断开，核心正在重试"
        if (holePlugin.state === "stopping") return "正在停止现有连接"
        if (holePlugin.running) return "正在建立连接，请稍候"
        if (holePlugin.lastError.length > 0) return holePlugin.lastError
        if (holePlugin.state === "error") return "核心返回错误，展开运行状态查看详情"
        return "填写连接设置后开始连接设备"
    }

    function beginConnection() {
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

    function clearMessages() {
        localError = ""
        holePlugin.clearError()
    }

    function toggleStatus() {
        statusOpen = !statusOpen
        if (!statusOpen) return
        localError = ""
        page.refreshSnapshot()
    }

    function refreshSnapshot() {
        if (holePlugin.running) holePlugin.refresh()
        else {
            statusLines = []
            statusRaw = "核心未运行，开始连接后可查看会话快照"
        }
    }

    function stateText(value) {
        var states = {
            starting: "正在启动",
            running: "运行中",
            reconfiguring: "重新配置中",
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

    function mappingText(mapping) {
        var parts = [mapping.role === "provide" ? "提供" : "使用", stateText(mapping.state)]
        if (asString(mapping.protocol).length > 0) parts.push(asString(mapping.protocol).toUpperCase())
        if (asString(mapping.endpoint).length > 0) parts.push(asString(mapping.endpoint))
        if (asString(mapping.peer).length > 0) parts.push("对端 " + asString(mapping.peer))
        parts.push("TCP " + asString(mapping.tcp_sessions || "0") + " 个会话")
        parts.push("UDP " + asString(mapping.udp_sessions || "0") + " 个会话")
        parts.push("收 " + bytesText(mapping.tcp_read_bytes) + " / 发 " + bytesText(mapping.tcp_written_bytes))
        if (mapping.error && asString(mapping.error.message).length > 0)
            parts.push("错误：" + asString(mapping.error.message))
        return parts.join(" · ")
    }

    function phaseText(phase) {
        var labels = {
            direct: "探测直连路径",
            relay_udp: "尝试 UDP 中继",
            relay_tls_443: "尝试 TLS 中继 · 443 端口",
            relay_tls: "尝试 TLS 中继 · 5349 / 自定义端口",
            relay_tcp_80: "尝试 TCP 中继 · 80 端口",
            relay_tcp: "尝试 TCP 中继 · 3478 / 自定义端口"
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
        if (path === "relay") return "中继" + (family.length > 0 ? " · " + family : "")
        if (path === "direct") return "设备直连" + (family.length > 0 ? " · " + family : "")
        return "路径未选定"
    }

    function peerPhaseText(peer) {
        var path = asString(peer.path_type)
        var state = asString(peer.state)
        var phase = asString(peer.phase)
        var pending = asString(peer.pending_phase)
        if (pending.length > 0 && pending !== phase)
            return "下一阶段：" + page.phaseText(pending)
        if (phase.length > 0 && path !== "direct" && path !== "relay")
            return page.phaseText(phase)
        if (phase.length > 0 && state !== "active" && path !== "direct")
            return "当前阶段：" + page.phaseText(phase)
        return ""
    }

    function appendPeerSnapshotLines(lines, peer) {
        var peerID = asString(peer.peer_id)
        appendSnapshotLine(lines, "通道 · " + peerID, stateText(peer.state))
        appendSnapshotLine(lines, "  路径", page.peerPathText(peer))
        var path = asString(peer.path_type)
        if (path === "relay" || path === "direct") {
            var localAccess = path === "relay"
                    ? page.localAccessText(peer)
                    : page.candidateAccessText(peer.local_type, "", "local")
            var remoteAccess = path === "relay"
                    ? page.remoteAccessText(peer)
                    : page.candidateAccessText(peer.remote_type, "", "remote")
            appendSnapshotLine(lines, "  本机连接", page.accessWithAddress(localAccess, peer.local_address))
            appendSnapshotLine(lines, "  对端连接", page.accessWithAddress(remoteAccess, peer.remote_address))
        }
        if (path === "relay") {
            appendSnapshotLine(lines, "  中继使用方", page.relaySideText(peer.relay_side))
        }
        appendSnapshotLine(lines, "  探测阶段", page.peerPhaseText(peer))
        if (Number(peer.rtt_ms) > 0) appendSnapshotLine(lines, "  延迟", asString(peer.rtt_ms) + " ms")
        appendSnapshotLine(lines, "  流量", "收 " + bytesText(peer.bytes_received) + " / 发 " + bytesText(peer.bytes_sent))
        if (Number(peer.active_channels) > 0)
            appendSnapshotLine(lines, "  活动通道", asString(peer.active_channels) + " 个")
        if (peer.error && asString(peer.error.message).length > 0)
            appendSnapshotLine(lines, "  错误", asString(peer.error.message))
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

    function refreshStatus() {
        var raw = holePlugin.snapshotJson
        if (!raw || raw.length === 0) {
            statusLines = []
            statusRaw = holePlugin.running ? "核心尚未返回快照，可点击刷新重试" : "核心未运行"
            return
        }
        var data = null
        try {
            data = JSON.parse(raw)
        } catch (error) {
            statusLines = []
            statusRaw = raw
            return
        }
        statusLines = data && typeof data === "object" && !Array.isArray(data)
                      ? page.humanSnapshot(data)
                      : [{label: "快照", text: "数据格式不是对象"}]
        statusRaw = ""
    }

    // ------------------------------------------------------------------- view

    // Keep the page background local to the plugin. The host input page is
    // created above this layer by VirtualKeyboard.
    Item {
        anchors.fill: parent
        clip: true

        Rectangle {
            anchors.fill: parent
            color: Theme.background
        }
    }

    VirtualKeyboard { id: keyboard }

    Flickable {
        id: scroll

        anchors.fill: parent
        anchors.margins: Theme.pageMargin
        clip: true
        contentWidth: width
        contentHeight: content.implicitHeight + Theme.spacing
        interactive: true
        pressDelay: 0
        boundsBehavior: Flickable.StopAtBounds

        ColumnLayout {
            id: content

            width: scroll.width
            spacing: Theme.spacing

            RowLayout {
                Layout.fillWidth: true
                spacing: 8

                Rectangle {
                    Layout.preferredWidth: 30
                    Layout.preferredHeight: 30
                    Layout.alignment: Qt.AlignVCenter
                    radius: 9
                    color: Theme.surfaceRaised
                    border.width: 1
                    border.color: Theme.borderHeader

                    Text {
                        anchors.centerIn: parent
                        text: "H"
                        color: Theme.accent
                        font.family: Theme.fontFamily
                        font.pixelSize: 17
                        font.bold: true
                    }
                }

                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: 0

                    Text {
                        Layout.fillWidth: true
                        text: "HOLE"
                        color: Theme.textPrimary
                        font.family: Theme.fontFamily
                        font.pixelSize: Theme.fontHeader
                        font.bold: true
                    }

                    Text {
                        Layout.fillWidth: true
                        text: "设备连接与端口映射"
                        color: Theme.textSecondary
                        font.family: Theme.fontFamily
                        font.pixelSize: Theme.fontCaption
                    }
                }

                Text {
                    Layout.alignment: Qt.AlignVCenter
                    text: page.stateTitle()
                    color: page.connectionEstablished ? Theme.accent
                                                        : holePlugin.running ? Theme.warning : Theme.textMuted
                    font.family: Theme.fontFamily
                    font.pixelSize: Theme.fontCaption
                }
            }

            Rectangle {
                Layout.fillWidth: true
                implicitHeight: statusCard.implicitHeight + 2 * Theme.cardPadding
                radius: Theme.radiusCard
                color: Theme.surface
                border.width: 1
                border.color: page.connectionEstablished ? Theme.borderActive : Theme.border

                Behavior on border.color { ColorAnimation { duration: 350 } }

                ColumnLayout {
                    id: statusCard

                    x: Theme.cardPadding
                    y: Theme.cardPadding
                    width: parent.width - 2 * Theme.cardPadding
                    spacing: 6

                    RowLayout {
                        Layout.fillWidth: true
                        spacing: 6

                        ColumnLayout {
                            Layout.fillWidth: true
                            spacing: 1

                            Text {
                                Layout.fillWidth: true
                                text: page.stateTitle()
                                color: Theme.textPrimary
                                font.family: Theme.fontFamily
                                font.pixelSize: Theme.fontTitle
                                font.bold: true
                                elide: Text.ElideRight
                            }

                            Text {
                                Layout.fillWidth: true
                                text: page.stateDetail()
                                color: Theme.textSecondary
                                font.family: Theme.fontFamily
                                font.pixelSize: Theme.fontCaption
                                wrapMode: Text.WordWrap
                            }
                        }

                        Rectangle {
                            Layout.preferredWidth: 10
                            Layout.preferredHeight: 10
                            Layout.alignment: Qt.AlignVCenter
                            radius: 5
                            color: page.connectionEstablished ? Theme.accent
                                                                : holePlugin.running ? Theme.warning : Theme.statusIdle
                        }
                    }

                    SwitchRow {
                        Layout.fillWidth: true
                        title: "启动 Hole"
                        summary: page.connectionEstablished ? "已连接并加入房间"
                                : page.runRequested ? "核心正在探测网络并建立连接"
                                : "关闭后停止核心和所有映射"
                        checked: page.runRequested
                        enabled: holePlugin.state !== "stopping"
                        onToggled: function(nextValue) {
                            if (nextValue) page.beginConnection()
                            else page.stopConnection()
                        }
                    }
                }
            }

            RowLayout {
                Layout.fillWidth: true
                spacing: 5

                ActionButton {
                    Layout.fillWidth: true
                    text: "设置"
                    onClicked: page.openSeparatePage("SettingsPage.qml", {
                        form: page,
                        hostKeyboard: keyboard
                    })
                }

                ActionButton {
                    Layout.fillWidth: true
                    text: "配置"
                    onClicked: page.openSeparatePage("ConfigPage.qml", {
                        form: page,
                        hostKeyboard: keyboard,
                        provideModel: provides,
                        consumeModel: consumes
                    })
                }
            }

            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: visible ? errorColumn.implicitHeight + 2 * Theme.cardPadding : 0
                visible: page.errorText.length > 0
                radius: Theme.radiusCard
                color: Theme.surfaceError

                ColumnLayout {
                    id: errorColumn

                    x: Theme.cardPadding
                    y: Theme.cardPadding
                    width: parent.width - 2 * Theme.cardPadding
                    spacing: 5

                    Text {
                        Layout.fillWidth: true
                        text: page.errorText
                        color: Theme.error
                        font.family: Theme.fontFamily
                        font.pixelSize: Theme.fontCaption
                        wrapMode: Text.WordWrap
                    }

                    ActionButton {
                        Layout.preferredWidth: 88
                        Layout.preferredHeight: 32
                        text: "清除提示"
                        variant: "danger"
                        onClicked: page.clearMessages()
                    }
                }
            }

            Section {
                Layout.fillWidth: true
                title: "运行状态"
                subtitle: page.stateTitle()
                collapsible: true
                expanded: page.statusOpen
                onHeaderTapped: page.toggleStatus()

                RowLayout {
                    Layout.fillWidth: true
                    spacing: 5

                    ActionButton {
                        Layout.fillWidth: true
                        Layout.preferredHeight: 36
                        text: "刷新快照"
                        onClicked: page.refreshSnapshot()
                    }

                    ActionButton {
                        Layout.fillWidth: true
                        Layout.preferredHeight: 36
                        text: "重新探测网络路径"
                        enabled: holePlugin.running
                        onClicked: holePlugin.networkChanged()
                    }
                }

                Repeater {
                    model: page.statusLines

                    delegate: RowLayout {
                        Layout.fillWidth: true
                        spacing: 6

                        Text {
                            Layout.preferredWidth: 82
                            text: modelData.label
                            color: Theme.textMuted
                            font.family: Theme.fontFamily
                            font.pixelSize: Theme.fontCaption
                            elide: Text.ElideRight
                        }

                        Text {
                            Layout.fillWidth: true
                            text: modelData.text
                            color: Theme.textSecondary
                            font.family: Theme.fontFamily
                            font.pixelSize: Theme.fontCaption
                            wrapMode: Text.WordWrap
                        }
                    }
                }

                Text {
                    Layout.fillWidth: true
                    Layout.preferredHeight: visible ? implicitHeight : 0
                    visible: text.length > 0
                    text: page.statusRaw
                    color: Theme.textMuted
                    font.family: Theme.fontFamily
                    font.pixelSize: Theme.fontCaption
                    wrapMode: Text.WrapAnywhere
                }
            }

            Text {
                Layout.fillWidth: true
                text: page.saveState.length > 0
                      ? page.saveState
                      : "设置会自动保存在插件目录，下次打开自动恢复。"
                color: page.saveState.length > 0 && page.saveState !== "已保存" ? Theme.warning : Theme.textFooter
                font.family: Theme.fontFamily
                font.pixelSize: Theme.fontCaption
                wrapMode: Text.WordWrap
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
        running: holePlugin.running
        onTriggered: holePlugin.refresh()
    }

    Connections {
        target: holePlugin
        onSnapshotChanged: page.refreshStatus()
        onStateChanged: if (page.statusOpen) page.refreshSnapshot()
        onRunningChanged: if (holePlugin.running) page.refreshSnapshot()
    }

    Component.onCompleted: page.readConfig()
}
