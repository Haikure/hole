// JSON decoding has its own QML worker engine. Only the latest revision is
// committed by Main.qml; formatting hidden views stays out of the poll path.
WorkerScript.onMessage = function(message) {
    var snapshot = {}
    var error = ""
    try {
        var value = JSON.parse(message.json)
        if (!value || typeof value !== "object" || Array.isArray(value))
            error = "快照数据格式不是对象"
        else
            snapshot = value
    } catch (e) {
        error = "快照解析失败，请刷新重试"
    }
    WorkerScript.sendMessage({revision: message.revision, snapshot: snapshot,
        natKey: JSON.stringify(snapshot.nat_diagnostic || null), error: error})
}
