var records = {}
var attempts = []
var options = {}
var frozen = null
var replayMs = 0
var replayIndex = -1
var selectedKey = ""
var pageSize = 64
var currentEpoch = -1

function text(value) { return value === undefined || value === null ? "" : String(value) }
function number(value) { var n = Number(value); return isFinite(n) ? n : 0 }
function compare(a, b) {
    a = text(a).replace(/^0+(?=\d)/, ""); b = text(b).replace(/^0+(?=\d)/, "")
    return a.length !== b.length ? a.length - b.length : a < b ? -1 : a > b ? 1 : 0
}
function elapsed(value) { var ms = number(value); return ms < 1000 ? ms + " ms" : (ms / 1000).toFixed(3) + " s" }
function millis(value) { return Date.parse(text(value).replace(/(\.\d{3})\d+(Z|[+-]\d\d:\d\d)$/, "$1$2")) || 0 }
function endpoint(value) {
    value = text(value)
    var match = /^(?:\[([^\]]+)\]|(.*)):(\d+)$/.exec(value)
    return match ? {host: match[1] || match[2], port: match[3]} : {host: value, port: ""}
}
function socketName(id) { var n = text(id).split("/").pop(); return "S" + (n.length < 2 ? "0" : "") + n }
function packet(event) { return !!event.direction && !!event.packet }
function datagram(event) { return ["stun_packet", "stun_error", "probe_sent", "probe_received", "ack_sent", "ack_received", "packet_rejected", "source_rejected"].indexOf(event.kind) >= 0 }
function source(value) { return {socket: "socket 绑定", selected_network: "所选网络", selected_route: "实际路由", stun: "STUN 返回", peer_observed: "对端观测", peer_reported: "对端上报", ice_candidate: "ICE 候选"}[value] || "待观测" }
function role(value) { return {business: "打洞业务", calibration: "STUN 校准", nat_probe: "NAT 采样", nat_base: "基础映射", nat_validation: "跨 socket 验证", nat_filtering: "过滤探测", ice: "ICE 候选"}[value] || text(value) }
function mode(value) { return {single: "单侧对称 · 多 socket", fixed: "双侧对称 · 固定目标", next: "双侧对称 · 下一端口", pool: "地址池 · 有限扫描", cone: "双端锥形 · ICE", ice: "普通 ICE", ordinary: "普通 ICE", discovery: "NAT 采样"}[value] || "连接准备" }
function phase(value) { return {ice: "ICE", direct: "ICE", punch: "预测打洞", nat: "NAT 采样", hole: "Hole 中继", relay: "UDP 中继", relay_udp: "UDP 中继", relay_tcp: "TCP 中继"}[value] || text(value) }
function state(value) { return {running: "进行中", connected: "已连接", complete: "采样完成", failed: "已结束", cancelled: "已取消", insufficient: "信息不足"}[value] || "准备中" }
function stage(value) {
    return {preparing: "准备连接", waiting_nat: "等待 NAT 采样", strategy: "选择打洞策略", reservation: "预约打洞时段", model: "交换 NAT 模型", model_refresh: "刷新分配模型",
        dns: "解析 STUN", binding: "预绑定业务 socket", calibration_wait: "同步校准准备", calibration: "校准公网映射", coordination: "校验双方计划", udp: "UDP 端口探测",
        quic: "QUIC 握手", mux: "身份与通道协商", connected: "业务通道就绪", ice: "ICE 候选检查", nat_sampling: "映射与过滤采样", nat_base: "同 socket 映射采样",
        nat_extra: "新 socket 分配验证", nat_classify: "汇总分类证据", nat_filter_base: "建立过滤基准", nat_filter_ip_port: "变更响应 IP 与端口", nat_filter_port: "变更响应端口",
        nat_filter_alternate: "确认备用 IP", nat_validate: "跨 socket 验证步长", nat_reuse: "确认同目标映射稳定"}[value] || "等待实际收发"
}
function validation(value) { return {authenticated: "认证通过", transaction_matched: "STUN 事务匹配", echo_verified: "挑战回显匹配", source_rejected: "来源不在范围内", packet_rejected: "认证未通过", sent: "发送成功", queued: "信令已提交", send_failed: "提交未完成"}[value] || text(value) }
function result(value) { return {ok: "完成", retry: "重新校准", refresh: "刷新模型", exhausted: "机会耗尽"}[value] || state(value) }
function eventTitle(event) {
    if (text(event.kind).indexOf("signal_") !== 0) return text(event.title)
    var name = {offer: "打洞参数提议", reserve: "时段预约", reserved: "预约确认", model: "NAT 模型", calibrate: "校准准备", calibrated: "校准结果", ready: "端口准备", go: "同步开始", failed: "本轮结束", abort: "停止打洞", ice_start: "ICE 开始"}[event.packet] || event.packet
    return (event.kind === "signal_error" ? "提交未完成 · " : event.direction === "in" ? "收到 · " : "提交 · ") + name
}
function line(label, value) { return {label: label, text: text(value) || "待观测"} }
function planLines(plan, label) {
    if (!plan) return []
    return [line(label + "校准映射", plan.ip + ":" + plan.port), line(label + "预测窗口", plan.target + "–" + (number(plan.target) + number(plan.width) - 1)), line(label + "宽度 / socket", plan.width + " / " + plan.sockets)]
}
function modelLines(model) {
    model = model || {}
    var behavior = {endpoint_independent: "端点无关", endpoint_dependent: "端点相关", address_dependent: "地址相关", address_port_dependent: "地址与端口相关", address_and_port_dependent: "地址与端口相关"}
    var kind = {open: "公网直连", firewall: "防火墙过滤", full_cone: "完全锥型", restricted_cone: "地址受限锥型", port_restricted_cone: "端口受限锥型", symmetric: "对称型", endpoint_independent: "端点无关映射", address_pool: "地址池"}
    var trend = {stable: "映射稳定", predictable_incremental: "可预测递增", predictable_decremental: "可预测递减", incremental: "可能递增", decremental: "可能递减", irregular: "未观察到规律", unverified: "待验证"}
    var rows = [line("NAT 类型", kind[model.type] || "信息不足"), line("端口趋势", trend[model.predictability] || "待验证"),
        line("映射 / 过滤", (behavior[model.mapping_behavior] || "未知") + " / " + (behavior[model.filtering_behavior] || "未知")),
        line("分配范围", {same_socket: "同 socket", cross_socket: "跨 socket"}[model.allocation_scope] || "未知"),
        line("保留 / No-PAT", ({preserved: "保留端口", translated: "端口转换", mixed: "部分保留"}[model.port_preservation] || "待观测") + " / " + ({suspected: "疑似", not_detected: "未观测到", not_applicable: "不适用"}[model.no_pat] || "待观测"))]
    if (model.public_ip) rows.push(line("公网 IP", model.public_ip))
    if (model.port_step) rows.push(line("端口步长", model.port_step))
    if (model.last_mapping_port) rows.push(line("基准端口", model.last_mapping_port))
    rows.push(line("有效基础样本", model.samples === undefined ? "待观测" : model.samples))
    ;(model.ip_evidence || []).forEach(function(ip) { rows.push(line("地址池 " + ip.ip, "目标 " + ip.targets + " · 端口 " + ip.ports + " · " + (trend[ip.trend] || ip.trend) + " · 步长 " + (ip.step || "—"))) })
    return rows
}
function eventLines(event) {
    var rows = [line("记录编号", event.sequence), line("代次 / 轮次 / 校准", event.generation + " / " + event.round + " / " + event.trial), line("绝对时间", event.time), line("相对时间", "+" + elapsed(event.elapsed_ms))]
    if (event.direction) rows.push(line("方向", event.direction === "in" ? "接收" : "发送"))
    if (event.socket_id) rows.push(line("socket / 用途", socketName(event.socket_id) + " · " + role(event.socket_role)))
    if (event.local_address) rows.push(line("本机端点", event.local_address))
    if (event.local_address_source) rows.push(line("本机 IP 来源", source(event.local_address_source)))
    if (event.bound_address) rows.push(line("绑定端点", event.bound_address))
    if (event.remote_address) rows.push(line(event.direction === "in" ? "实际来源" : "发送目标", event.remote_address))
    if (event.mapped_address) rows.push(line("观测映射", event.mapped_address))
    if (event.address_source) rows.push(line("地址来源", source(event.address_source)))
    if (event.packet) rows.push(line("报文类型", event.packet))
    if (event.packet_sequence) rows.push(line("报文序号", event.packet_sequence))
    if (event.bytes) rows.push(line("长度", event.bytes + " B"))
    if (event.transaction_id) {
        rows.push(line("STUN 事务", event.transaction_id))
        rows.push(line("CHANGE-REQUEST", number(event.change_request) === 6 ? "6 · 变更 IP 与端口" : number(event.change_request) === 2 ? "2 · 变更端口" : "0 · 默认来源"))
    }
    if (event.server_address) rows.push(line("请求的 STUN 服务", event.server_address))
    if (event.other_address) rows.push(line("OTHER-ADDRESS", event.other_address))
    if (event.peer_socket) rows.push(line("对端 socket", event.peer_socket))
    if (event.kind === "probe_sent") rows.push(line("重传间隔 / 探测上限", "至少 " + event.retry_after_ms + " ms / " + event.probe_limit))
    if (event.validation) rows.push(line("验证状态", validation(event.validation)))
    if (event.mode) rows.push(line("策略", mode(event.mode)))
    if (event.result) rows.push(line("结果", result(event.result)))
    if (event.detail) rows.push(line("记录内容", event.detail))
    rows = rows.concat(planLines(event.plan, "信令"))
    if (event.round_snapshot) rows = rows.concat(planLines(event.round_snapshot.local_plan, "本机"), planLines(event.round_snapshot.peer_plan, "对端"))
    if (event.kind === "targets" && event.round_snapshot && event.round_snapshot.targets) rows.push(line("本轮实际目标", event.round_snapshot.targets.join("\n")))
    if (event.model) rows = rows.concat(modelLines(event.model))
    return rows
}
function eventRow(event, prefix) {
    var key = prefix + "/" + event.sequence
    var rejected = /rejected$/.test(text(event.validation)) || /error$/.test(text(event.kind))
    return {key: key, kind: "event", title: eventTitle(event), subtitle: "+" + elapsed(event.elapsed_ms) + " · #" + event.sequence,
        tone: rejected ? "red" : event.direction === "in" ? "green" : packet(event) ? "blue" : "neutral",
        packet: text(event.packet), socket: event.socket_id ? socketName(event.socket_id) : "", bytes: event.bytes ? event.bytes + " B" : "",
        validation: validation(event.validation), direction: text(event.direction), datagram: datagram(event),
        endpointFlow: datagram(event) || event.kind === "ice_selected", socketId: text(event.socket_id),
        local: endpoint(event.local_address || event.bound_address), remote: endpoint(event.remote_address),
        transaction: text(event.transaction_id), expanded: options.detail === key,
        lines: options.detail === key ? eventLines(event) : []}
}
function socketAt(socket, events) {
    var copy = JSON.parse(JSON.stringify(socket)), related = events.filter(function(e) { return e.socket_id === socket.id })
    copy.state = "bound"; copy.mapped_address = ""; copy.mapping_source = ""; copy.target = ""; copy.local_address = ""; copy.local_address_source = ""
    copy.probes_sent = 0; copy.probes_received = 0; copy.acks_sent = 0; copy.acks_received = 0
    related.forEach(function(e) {
        copy.last_activity_ms = e.elapsed_ms
        if (e.remote_address) copy.target = e.remote_address
        if (e.kind === "socket_bound" || e.kind === "socket_role") copy.role = e.detail
        if ((e.kind === "socket_bound" || e.kind === "socket_selected") && e.local_address) { copy.local_address = e.local_address; copy.local_address_source = e.local_address_source || e.address_source }
        if (e.kind === "mapped") { copy.mapped_address = e.mapped_address; copy.mapping_source = e.address_source }
        if (e.kind === "probe_sent") { copy.probes_sent++; if (copy.state === "bound" || copy.state === "probing") copy.state = "probing" }
        if (e.kind === "probe_received") copy.probes_received++
        if (e.kind === "ack_sent") copy.acks_sent++
        if (e.kind === "ack_received") copy.acks_received++
        var states = {ack_verified: "verified", quic_started: "quic", quic_established: "quic", socket_selected: "selected", socket_retained: "retained", socket_closed: "closed"}
        if (states[e.kind]) copy.state = states[e.kind]
    })
    return copy
}
function socketRow(socket, prefix, events) {
    var key = prefix + "/socket/" + socket.id
    var status = {bound: "已绑定", probing: "探测中", verified: "ACK 已验证", quic: "QUIC 握手", selected: "已选中", retained: "保留占用", closed: "已关闭"}[socket.state] || socket.state
    var rows = []
    if (options.detail === key) {
        rows = [line("绑定端点", socket.bound_address), line("本机端点", socket.local_address), line("本机 IP 来源", source(socket.local_address_source)), line("公网端点", socket.mapped_address), line("映射来源", source(socket.mapping_source)),
            line("最近目标", socket.target), line("创建 / 最近活动", "+" + elapsed(socket.created_ms) + " / +" + elapsed(socket.last_activity_ms)),
            line("PROBE 发送 / 接收", socket.probes_sent + " / " + socket.probes_received), line("ACK 发送 / 接收", socket.acks_sent + " / " + socket.acks_received)]
        var sends = {}
        events.forEach(function(e) { if (e.socket_id === socket.id && e.kind === "probe_sent") sends[e.remote_address] = e })
        Object.keys(sends).forEach(function(target) {
            var e = sends[target]
            rows.push(line("探测 " + target, "序号 " + e.packet_sequence + " / " + e.probe_limit + " · 至少 " + e.retry_after_ms + " ms"))
        })
    }
    return {key: key, kind: "socket", title: socketName(socket.id), subtitle: role(socket.role) + " · " + status,
        tone: socket.state === "selected" || socket.state === "verified" ? "green" : socket.state === "closed" ? "neutral" : "blue",
        local: endpoint(socket.local_address || socket.bound_address), remote: endpoint(socket.mapped_address),
        validation: "PROBE ↑" + socket.probes_sent + " ↓" + socket.probes_received + " · ACK ↓" + socket.acks_received,
        expanded: options.detail === key, lines: rows, socketId: socket.id}
}
function merge(reply) {
    var generation = text(reply.selected_generation), stream = reply.stream || "connection", data = reply.nat_punch_process || {}, key = generation + "/" + stream
    var old = records[key], runs = data.attempts || [], run = runs[runs.length - 1]
    var reset = !!old && !!run && (old.run.started_at !== run.started_at || compare(data.sequence, old.sequence) < 0)
    if (Array.isArray(reply.attempts)) {
        attempts = reply.attempts
        var allowed = {}; attempts.forEach(function(a) { allowed[text(a.generation)] = true })
        Object.keys(records).forEach(function(k) { if (!allowed[k.split("/")[0]]) delete records[k] })
    }
    if (run) {
        var combined = (old && !reset ? old.events : []).concat(data.events || []), seen = {}
        combined = combined.filter(function(e) { var id = text(e.sequence); if (seen[id]) return false; seen[id] = true; return true })
        combined.sort(function(a, b) { return compare(a.sequence, b.sequence) })
        var steps = combined.filter(function(e) { return !packet(e) }).slice(-512), packets = combined.filter(packet).slice(-4096)
        records[key] = {run: run, events: steps.concat(packets).sort(function(a, b) { return compare(a.sequence, b.sequence) }), sequence: text(data.sequence || "0"),
            dropped: text(data.events_dropped || "0"), evicted: text(data.events_evicted || "0"), hasDiscovery: !!reply.has_discovery, more: !!reply.more}
    }
    return {generation: generation, stream: stream, sequence: text(data.sequence || "0"), more: !!reply.more, reset: reset, hasDiscovery: !!reply.has_discovery}
}
function selection() { return text(options.generation) || (attempts.length ? text(attempts[attempts.length - 1].generation) : "") }
function clone(value) { return value ? JSON.parse(JSON.stringify(value)) : null }
function freeze() {
    var key = selection(), record = records[key + "/connection"]
    var discovery = records[key + "/discovery"]
    if (!options.freezeReady || !record || record.more || record.hasDiscovery && (!discovery || discovery.more)) return false
    frozen = {key: key, token: options.pauseToken, main: clone(record), discovery: clone(discovery)}
    replayMs = Math.max(number(record.run.elapsed_ms), record.events.length ? number(record.events[record.events.length - 1].elapsed_ms) : 0)
    replayIndex = record.events.length - 1
    return true
}
function command(message) {
    var key = selection()
    if (key !== selectedKey) { frozen = null; selectedKey = key }
    if (frozen && frozen.token !== options.pauseToken) frozen = null
    if (!options.paused) { frozen = null; return }
    if (!frozen && !freeze()) return
    if (!frozen) return
    var events = frozen.main.events, duration = Math.max(number(frozen.main.run.elapsed_ms), events.length ? number(events[events.length - 1].elapsed_ms) : 0, 1)
    if (message.command === "seek" || message.command === "tick") {
        replayMs = message.command === "seek" ? duration * number(message.value) : Math.min(duration, replayMs + 100)
        replayIndex = -1
        events.forEach(function(e, i) { if (number(e.elapsed_ms) <= replayMs) replayIndex = i })
    } else if (message.command === "step") {
        replayIndex = Math.max(-1, Math.min(events.length - 1, replayIndex + number(message.value)))
        replayMs = replayIndex < 0 ? 0 : number(events[replayIndex].elapsed_ms)
    }
}
function projection() {
    var generation = selection(), live = records[generation + "/connection"], record = options.paused && frozen && frozen.key === generation ? frozen.main : live
    var frame = {generation: generation, attempts: attempts.map(function(a) { return {value: text(a.generation), label: phase(a.phase) + " · " + a.generation} }), rows: [], rounds: [], trials: [],
        waiting: !record, hasDiscovery: !!live && live.hasDiscovery, paused: !!options.paused, frozen: !!options.paused && !!frozen, duration: 1, progress: 1, time: "0 ms", markers: [], packetCount: 0, pageCount: 1, pageIndex: 0,
        plans: [], formula: "", summary: [], localModel: [], peerModel: [], network: [], sampleRows: [], samplePoints: [], sampleRange: "", sampleSteps: [], sampleIps: [], matrix: {sockets: [], targets: [], cells: []}, loss: ""}
    if (!record) return frame
    var run = record.run, full = record.events, history = options.paused && frozen ? full.slice(0, replayIndex + 1) : full
    frame.duration = Math.max(number(run.elapsed_ms), full.length ? number(full[full.length - 1].elapsed_ms) : 0, 1)
    var cursor = options.paused && frozen ? replayMs : frame.duration
    frame.progress = cursor / frame.duration; frame.time = elapsed(cursor); frame.atEnd = cursor >= frame.duration
    frame.durationText = elapsed(frame.duration)
    frame.markers = full.filter(function(e) { return e.kind === "stage" }).map(function(e) { return number(e.elapsed_ms) / frame.duration })
    var rounds = run.rounds || [], currentStage = run.stage, currentState = run.state
    if (options.paused && frozen) {
        var historic = {}, finished = null; currentStage = "preparing"
        history.forEach(function(e) { if (e.round_snapshot) historic[e.round_snapshot.round] = e.round_snapshot; if (e.kind === "stage") currentStage = e.stage; if (e.kind === "finished") finished = e })
        rounds = Object.keys(historic).map(function(k) { return historic[k] }).sort(function(a, b) { return a.round - b.round })
        currentState = finished ? finished.result || run.state : "running"
    }
    frame.rounds = [{value: "-1", label: "全过程"}].concat(rounds.filter(function(r) { return r.round > 0 }).map(function(r) { return {value: text(r.round), label: "第 " + r.round + " 轮"} }))
    var round = null
    rounds.forEach(function(r) { if (number(options.round) < 0 || r.round === number(options.round)) round = r })
    var trialSet = {}
    history.forEach(function(e) { if ((number(options.round) < 0 || e.round === number(options.round)) && e.trial > 0) trialSet[e.trial] = true })
    frame.trials = [{value: "-1", label: "全部校准"}].concat(Object.keys(trialSet).sort(compare).map(function(t) { return {value: t, label: "校准 " + t} }))
    if (number(options.trial) > 0) {
        var selectedRound = round ? round.round : -1; round = null
        history.forEach(function(e) { if (e.round === selectedRound && e.trial === number(options.trial) && e.round_snapshot) round = e.round_snapshot })
    }
    var filtered = history.filter(function(e) { return (number(options.round) < 0 || e.round === number(options.round)) && (number(options.trial) < 0 || e.trial === number(options.trial)) &&
        (!options.socket || e.socket_id === options.socket || e.kind === "stage") && (!options.target || e.remote_address === options.target || e.kind === "stage") })
    var last = null
    filtered.forEach(function(e) { if (datagram(e) || e.kind === "socket_selected" || e.kind === "ice_selected") last = e })
    var local = last ? last.local_address || last.bound_address : "", remote = last ? last.remote_address : round && round.targets && round.targets[0] || ""
    frame.hero = {title: stage(number(options.round) > 0 && round ? round.stage : currentStage), subtitle: round && round.mode ? mode(round.mode) : phase(run.phase), state: state(currentState),
        tone: currentState === "connected" ? "green" : currentState === "failed" ? "red" : "blue", time: frame.time,
        local: endpoint(local), remote: endpoint(remote), localLabel: last && last.kind === "ice_selected" ? "本机 ICE" : last && last.transaction_id ? "本机 STUN" : "本机 UDP",
        remoteLabel: last && last.direction === "in" ? "实际来源" : "发送目标",
        localSource: source(last && (last.kind === "ice_selected" ? "ice_candidate" : last.local_address_source || (last.bound_address ? "socket" : ""))),
        remoteSource: last ? last.direction === "in" ? "实际回包来源" : datagram(last) ? "实际发送目标" : "选中线路" : "预测目标",
        direction: last ? text(last.direction) : "", activity: last ? eventTitle(last) + " · +" + elapsed(last.elapsed_ms) : "等待实际收发", sequence: last ? text(last.sequence) : ""}
    var sockets = (run.sockets || []).filter(function(s) { return (number(options.round) < 0 || s.round === number(options.round)) && (!options.paused || number(s.created_ms) <= cursor) })
    if (options.paused && frozen) sockets = sockets.map(function(s) { return socketAt(s, history) })
    frame.metrics = [line("PROBE ↑", sockets.reduce(function(n, s) { return n + number(s.probes_sent) }, 0)), line("ACK ↓", sockets.reduce(function(n, s) { return n + number(s.acks_received) }, 0)), line("socket", sockets.length), line("目标端口", round && round.targets ? round.targets.length : "—")]
    frame.localModel = modelLines(round && round.local_model); frame.peerModel = modelLines(round && round.peer_model)
    var network = run.network || {}
    frame.network = [line("网络 / 网卡", (network.transport || "系统选择") + " / " + (network.interface || "待观测"))]
    if (network.handle) frame.network.push(line("网络标识", network.handle))
    if (network.gateway) frame.network.push(line("网关", network.gateway))
    ;(network.addresses || []).forEach(function(ip) { frame.network.push(line(ip.indexOf(":") >= 0 ? "IPv6" : "IPv4", ip)) })
    if (network.dns && network.dns.length) frame.network.push(line("DNS", network.dns.join(" · ")))
    if (round && round.peer_plan) {
        var plan = round.peer_plan, step = number(round.peer_model && round.peer_model.port_step), offset = round.mode === "fixed" ? Math.max(1, Math.floor(plan.sockets / 2)) : 1
        frame.formula = (round.mode === "fixed" || round.mode === "next") && step > 0 ? plan.port + " + " + step + " × " + offset + " = " + plan.target : plan.port + (plan.target >= plan.port ? " + " : " − ") + Math.abs(plan.target - plan.port) + " = " + plan.target
        frame.plans = planLines(round.local_plan, "本机").concat(planLines(round.peer_plan, "对端"))
        frame.plans.push(line("QUIC 角色", round.dialer ? "发起握手" : "接收握手"))
        if (round.selected_socket) frame.plans.push(line("选中 socket", socketName(round.selected_socket)))
        if (round.targets && round.targets.length) frame.plans.push(line("候选端口顺序", endpoint(round.targets[0]).port + " → " + endpoint(round.targets[round.targets.length - 1]).port))
    }
    if (round && round.diagnostic) {
        var stats = round.diagnostic
        frame.summary = [line("PROBE 发送 / 接收", stats.probes_sent + " / " + stats.probes_received), line("ACK 发送 / 接收", stats.acks_sent + " / " + stats.acks_received),
            line("挑战回显匹配", stats.acks_verified), line("来源 / 认证未通过", stats.source_rejected + " / " + stats.packet_rejected), line("接收错误", stats.read_errors), line("QUIC", stats.quic_established ? "已建立" : "本轮已结束")]
        if (stats.error) frame.summary.push(line("结束状态", stats.error))
    }
    var discovery = options.paused && frozen ? frozen.discovery : records[generation + "/discovery"], sampleEvents = discovery ? discovery.events : []
    if (options.paused && frozen) sampleEvents = sampleEvents.filter(function(e) { return millis(e.time) <= millis(run.started_at) + cursor })
    var mapped = sampleEvents.filter(function(e) { return e.kind === "mapped" && e.mapped_address && (!options.sampleRole || options.sampleRole === "all" || e.socket_role === options.sampleRole) })
    var sampleIps = {}
    mapped.forEach(function(e) { sampleIps[endpoint(e.mapped_address).host] = true })
    frame.sampleIps = Object.keys(sampleIps).map(function(ip) { return {value: ip, label: ip} })
    var sampleIp = options.sampleIp && sampleIps[options.sampleIp] ? options.sampleIp : frame.sampleIps.length ? frame.sampleIps[0].value : ""
    mapped = mapped.filter(function(e) { return endpoint(e.mapped_address).host === sampleIp })
    var samplePorts = mapped.map(function(e) { return number(endpoint(e.mapped_address).port) }), low = samplePorts.length ? Math.min.apply(Math, samplePorts) : 0, high = samplePorts.length ? Math.max.apply(Math, samplePorts) : 0
    frame.samplePoints = samplePorts.map(function(port, i) {
        var event = mapped[i], previous = i > 0 ? samplePorts[i - 1] : null
        return {x: i / Math.max(1, samplePorts.length - 1), y: (port - low) / Math.max(1, high - low), sequence: text(event.sequence),
            port: text(port), delta: previous === null ? "首个样本" : (port - previous >= 0 ? "+" : "") + (port - previous),
            socket: socketName(event.socket_id), mapped: event.mapped_address, server: event.server_address, time: "+" + elapsed(event.elapsed_ms), transaction: text(event.transaction_id)}
    })
    frame.sampleRange = samplePorts.length ? low + "–" + high + " · " + samplePorts.length + " 个映射响应" : "暂无对应采样"
    frame.sampleRows = mapped.slice(-24).map(function(e) { return eventRow(e, generation + "/discovery") })
    frame.sampleSteps = sampleEvents.filter(function(e) { return e.kind === "stage" }).map(function(e) { return eventRow(e, generation + "/discovery") })
    if (!round && discovery) {
        var sampledModel = null
        if (options.paused && frozen) sampleEvents.forEach(function(e) { if (e.round_snapshot) sampledModel = e.round_snapshot.local_model })
        else if (discovery.run.rounds && discovery.run.rounds.length) sampledModel = discovery.run.rounds[discovery.run.rounds.length - 1].local_model
        frame.localModel = modelLines(sampledModel)
    }
    frame.loss = (record.evicted !== "0" || record.dropped !== "0") ? "已移出 " + record.evicted + " 条 · 未记录 " + record.dropped + " 条" : ""
    if (options.tab === "ports") {
        frame.rows = sockets.map(function(s) { return socketRow(s, generation, history) })
        var allTargets = round && round.targets || [], business = sockets.filter(function(s) { return s.role === "business" && round && s.round === round.round })
        var portPage = Math.min(Math.max(0, number(options.matrixPortPage)), Math.max(0, Math.ceil(allTargets.length / 16) - 1)), socketPage = Math.min(Math.max(0, number(options.matrixSocketPage)), Math.max(0, Math.ceil(business.length / 8) - 1))
        var targetWindow = allTargets.slice(portPage * 16, portPage * 16 + 16), socketWindow = business.slice(socketPage * 8, socketPage * 8 + 8), indexed = {}
        history.forEach(function(e) { var key = e.socket_id + "\n" + e.remote_address; if (!indexed[key]) indexed[key] = []; indexed[key].push(e) })
        frame.matrix = {portPage: portPage, socketPage: socketPage, portPages: Math.max(1, Math.ceil(allTargets.length / 16)), socketPages: Math.max(1, Math.ceil(business.length / 8)),
            targetCount: allTargets.length, socketCount: business.length, sockets: socketWindow.map(function(s) { return {id: s.id, label: socketName(s.id), port: endpoint(s.bound_address).port} }),
            targets: targetWindow.map(function(t) { return {value: t, label: endpoint(t).port, host: endpoint(t).host} }), cells: []}
        targetWindow.forEach(function(target) {
            var cells = socketWindow.map(function(s) {
                var related = indexed[s.id + "\n" + target] || [], kinds = {}, sent = 0, received = 0
                related.forEach(function(e) { kinds[e.kind] = true; if (datagram(e)) { if (e.direction === "out") sent++; else if (e.direction === "in") received++ } })
                var status = kinds.socket_selected ? "已命中" : kinds.ack_verified ? "已验证" : kinds.ack_received || kinds.probe_received ? "已回应" : kinds.packet_rejected || kinds.source_rejected ? "未通过" : kinds.probe_sent ? "已探测" : "未发送"
                return {socket: s.id, target: target, label: status, count: "↑" + sent + " ↓" + received, tone: status === "已命中" || status === "已验证" ? "green" : status === "已回应" ? "yellow" : status === "未通过" ? "red" : status === "已探测" ? "blue" : "neutral"}
            })
            frame.matrix.cells.push(cells)
        })
    } else if (options.tab === "packets") {
        var useSamples = options.packetStream === "discovery", pool = useSamples ? sampleEvents : filtered, query = text(options.query).toLowerCase(), stageEnd = ""
        if (!useSamples && options.stageSequence) {
            history.some(function(e) { if (e.kind === "stage" && compare(e.sequence, options.stageSequence) > 0) { stageEnd = text(e.sequence); return true } return false })
        }
        pool = pool.filter(function(e) {
            if (!packet(e) || options.direction && options.direction !== "all" && e.direction !== options.direction) return false
            if (options.socket && e.socket_id !== options.socket || options.target && e.remote_address !== options.target) return false
            if (!useSamples && options.stageSequence && (compare(e.sequence, options.stageSequence) < 0 || stageEnd && compare(e.sequence, stageEnd) >= 0)) return false
            if (options.packetKind === "PROBE" || options.packetKind === "ACK") { if (e.packet !== options.packetKind) return false }
            else if (options.packetKind === "STUN" && !e.transaction_id && e.kind !== "stun_packet" && e.kind !== "stun_error") return false
            else if (options.packetKind === "signal" && datagram(e)) return false
            else if (options.packetKind === "rejected" && !/rejected$/.test(text(e.validation)) && !/error$/.test(text(e.kind))) return false
            return !query || [e.title, e.packet, e.local_address, e.bound_address, e.remote_address, e.mapped_address, e.transaction_id, e.sequence].map(text).join(" ").toLowerCase().indexOf(query) >= 0
        })
        frame.packetCount = pool.length; frame.pageCount = Math.max(1, Math.ceil(pool.length / pageSize)); frame.pageIndex = options.follow ? frame.pageCount - 1 : Math.min(Math.max(0, number(options.packetPage)), frame.pageCount - 1)
        frame.rows = pool.slice(frame.pageIndex * pageSize, frame.pageIndex * pageSize + pageSize).map(function(e) { return eventRow(e, generation + "/" + (useSamples ? "discovery" : "connection")) })
    } else {
        var groups = []
        filtered.forEach(function(e) { if (!groups.length || e.kind === "stage") groups.push([]); groups[groups.length - 1].push(e) })
        frame.rows = groups.map(function(group, index) {
            var first = group[0], key = generation + "/stage/" + first.sequence, open = !!(options.expanded && options.expanded[key]), packets = group.filter(packet)
            var steps = group.filter(function(e) { return !packet(e) }), limit = Math.max(24, number(options.stageLimits && options.stageLimits[key]))
            var children = open ? steps.slice(0, limit).concat(packets.slice(-3)).sort(function(a, b) { return compare(a.sequence, b.sequence) }) : []
            var failed = group.some(function(e) { return e.kind === "round_failed" }), complete = !failed && (index < groups.length - 1 || currentState === "connected")
            return {key: key, kind: "stage", step: index + 1, title: first.title, subtitle: "+" + elapsed(first.elapsed_ms) + " → " + elapsed(group[group.length - 1].elapsed_ms) + " · " + group.length + " 条",
                tone: failed ? "red" : complete ? "green" : index === groups.length - 1 && currentState === "running" ? "blue" : "neutral", complete: complete,
                expanded: open, remainingSteps: Math.max(0, steps.length - limit), packetCount: packets.length, stageSequence: text(first.sequence), round: first.round,
                sent: packets.filter(function(e) { return e.direction === "out" }).length, received: packets.filter(function(e) { return e.direction === "in" }).length,
                children: children.map(function(e) { return eventRow(e, generation + "/connection") })}
        })
    }
    return frame
}

WorkerScript.onMessage = function(message) {
    var readResult = null, error = ""
    try {
        if (message.epoch !== currentEpoch) {
            records = {}; attempts = []; frozen = null; replayMs = 0; replayIndex = -1; selectedKey = ""; currentEpoch = message.epoch
        }
        options = message.options || options
        if (message.json) readResult = merge(JSON.parse(message.json))
        command(message)
        WorkerScript.sendMessage({epoch: message.epoch, revision: message.revision, read: message.read, view: projection(), readResult: readResult, error: error})
    } catch (failure) {
        WorkerScript.sendMessage({epoch: message.epoch, revision: message.revision, read: message.read, view: {waiting: true, rows: [], attempts: [], rounds: [], trials: []}, readResult: readResult, error: "过程记录解析失败，请刷新重试"})
    }
}
