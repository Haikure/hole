#include "HolePlugin.h"

#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QJsonDocument>
#include <QJsonArray>
#include <QJsonParseError>
#include <QSaveFile>

#include <dlfcn.h>

namespace {
QString pluginDirectoryFromAddress(void* address) {
    Dl_info info{};
    if (dladdr(address, &info) == 0 || info.dli_fname == nullptr) return {};
    return QFileInfo(QString::fromLocal8Bit(info.dli_fname)).absolutePath();
}
}

HolePlugin::HolePlugin(QObject* parent) : QObject(parent) {
    m_pluginDirectory = pluginDirectoryFromAddress(reinterpret_cast<void*>(&init_plugin));
    m_reloadTimer.setSingleShot(true);
    connect(&m_reloadTimer, &QTimer::timeout, this, [this] {
        if (m_reloadStopping) {
            if (m_process) m_process->kill();
        } else if (m_reloading) {
            failReload(QStringLiteral("新核心握手超时，请检查核心文件后重试"));
            if (m_process) m_process->kill();
        }
    });
    m_natPunchProcessTimer.setSingleShot(true);
    m_natPunchProcessTimer.setInterval(5000);
    connect(&m_natPunchProcessTimer, &QTimer::timeout, this, [this] {
        const QString id = m_natPunchProcessRequestId;
        m_natPunchProcessRequestId.clear();
        emit natPunchProcessFailed(id, QStringLiteral("过程记录读取超时"));
    });
    loadConfig();
}

HolePlugin::~HolePlugin() {
    m_reloadTimer.stop();
    m_natPunchProcessTimer.stop();
    m_reloading = m_reloadStopping = m_resumeAfterReload = false;
    if (!m_process) return;
    // Teardown must never dispatch finished() into the reload path.
    disconnect(m_process, nullptr, this, nullptr);
    if (m_process->state() != QProcess::NotRunning) {
        sendRequest(QStringLiteral("shutdown"));
        if (!m_process->waitForFinished(2000)) {
            m_process->kill();
            m_process->waitForFinished(2000);
        }
    }
    delete m_process;
}

void HolePlugin::setServerUrl(const QString& value) {
    if (m_serverUrl == value) return;
    m_serverUrl = value.trimmed();
    emit configChanged();
}

void HolePlugin::setConfigJson(const QString& value) {
    if (m_configJson == value) return;
    m_configJson = value;
    emit configChanged();
}

void HolePlugin::setMappingStateJson(const QString& value) {
    if (m_mappingStateJson == value) return;
    m_mappingStateJson = value;
    emit configChanged();
}

void HolePlugin::setTurnStateJson(const QString& value) {
    if (m_turnStateJson == value) return;
    m_turnStateJson = value;
    emit configChanged();
}

void HolePlugin::setState(const QString& value) {
    if (m_state == value) return;
    m_state = value;
    emit stateChanged();
}

void HolePlugin::setError(const QString& value) {
    if (m_lastError == value) return;
    m_lastError = value;
    emit lastErrorChanged();
}

QString HolePlugin::configPath() const {
    return QDir(m_pluginDirectory).filePath(QStringLiteral("config.json"));
}

bool HolePlugin::saveConfig() {
    QJsonParseError error{};
    const QJsonDocument config = QJsonDocument::fromJson(m_configJson.toUtf8(), &error);
    if (error.error != QJsonParseError::NoError || !config.isObject() || m_serverUrl.isEmpty()) {
        setError(QStringLiteral("配置或信令地址无效，无法保存"));
        return false;
    }

    QJsonObject root;
    root.insert(QStringLiteral("server_url"), m_serverUrl);
    root.insert(QStringLiteral("config"), config.object());
    if (!m_mappingStateJson.trimmed().isEmpty()) {
        QJsonParseError mappingError{};
        const QJsonDocument mappingState = QJsonDocument::fromJson(m_mappingStateJson.toUtf8(), &mappingError);
        if (mappingError.error != QJsonParseError::NoError || !mappingState.isObject()) {
            setError(QStringLiteral("映射编辑状态无效，无法保存"));
            return false;
        }
        root.insert(QStringLiteral("mapping_state"), mappingState.object());
    }
    if (!m_turnStateJson.trimmed().isEmpty()) {
        QJsonParseError turnError{};
        const QJsonDocument turnState = QJsonDocument::fromJson(m_turnStateJson.toUtf8(), &turnError);
        if (turnError.error != QJsonParseError::NoError || !turnState.isObject()) {
            setError(QStringLiteral("TURN 编辑状态无效，无法保存"));
            return false;
        }
        root.insert(QStringLiteral("turn_state"), turnState.object());
    }
    QSaveFile file(configPath());
    if (!file.open(QIODevice::WriteOnly) || file.write(QJsonDocument(root).toJson(QJsonDocument::Indented)) < 0 || !file.commit()) {
        setError(QStringLiteral("无法保存插件配置"));
        return false;
    }
    return true;
}

bool HolePlugin::loadConfig() {
    QFile file(configPath());
    if (!file.exists()) return true;
    if (!file.open(QIODevice::ReadOnly)) {
        setError(QStringLiteral("无法读取插件配置"));
        return false;
    }

    QJsonParseError error{};
    const QJsonDocument document = QJsonDocument::fromJson(file.readAll(), &error);
    if (error.error != QJsonParseError::NoError || !document.isObject()) {
        setError(QStringLiteral("插件配置格式无效"));
        return false;
    }
    const QJsonObject root = document.object();
    const QJsonValue serverUrl = root.value(QStringLiteral("server_url"));
    const QJsonValue config = root.value(QStringLiteral("config"));
    if (!serverUrl.isString() || !config.isObject()) {
        setError(QStringLiteral("插件配置缺少必要字段"));
        return false;
    }
    m_serverUrl = serverUrl.toString();
    m_configJson = QString::fromUtf8(QJsonDocument(config.toObject()).toJson(QJsonDocument::Compact));
    const QJsonValue mappingState = root.value(QStringLiteral("mapping_state"));
    m_mappingStateJson = mappingState.isObject()
        ? QString::fromUtf8(QJsonDocument(mappingState.toObject()).toJson(QJsonDocument::Compact))
        : QString();
    const QJsonValue turnState = root.value(QStringLiteral("turn_state"));
    m_turnStateJson = turnState.isObject()
        ? QString::fromUtf8(QJsonDocument(turnState.toObject()).toJson(QJsonDocument::Compact))
        : QString();
    emit configChanged();
    return true;
}

QJsonObject HolePlugin::requestObject() const {
    QJsonParseError error{};
    const QJsonDocument config = QJsonDocument::fromJson(m_configJson.toUtf8(), &error);
    if (error.error != QJsonParseError::NoError || !config.isObject()) return {};

    QJsonObject request;
    request.insert(QStringLiteral("api_version"), 1);
    request.insert(QStringLiteral("server_url"), m_serverUrl);
    request.insert(QStringLiteral("config"), config.object());
    return request;
}

void HolePlugin::ensureProcess() {
    if (m_reloadStopping) return;
    if (m_process && m_process->state() != QProcess::NotRunning) return;
    if (m_pluginDirectory.isEmpty()) {
        setError(QStringLiteral("无法确定插件目录"));
        return;
    }

    if (m_process) {
        disconnect(m_process, nullptr, this, nullptr);
        m_process->deleteLater();
    }
    m_process = new QProcess(this);
    m_process->setProgram(QDir(m_pluginDirectory).filePath(QStringLiteral("hole-core")));
    m_process->setWorkingDirectory(m_pluginDirectory);
    m_process->setProcessChannelMode(QProcess::SeparateChannels);
    connect(m_process, &QProcess::started, this, &HolePlugin::onProcessStarted);
    connect(m_process, &QProcess::readyReadStandardOutput, this, &HolePlugin::readStandardOutput);
    connect(m_process, &QProcess::readyReadStandardError, this, &HolePlugin::readStandardError);
    connect(m_process, qOverload<int, QProcess::ExitStatus>(&QProcess::finished), this, &HolePlugin::onProcessFinished);
    connect(m_process, &QProcess::errorOccurred, this, [this](QProcess::ProcessError error) {
        if (error != QProcess::FailedToStart) return;
        failReload(QStringLiteral("无法启动 Hole 核心程序，请检查文件和执行权限"));
    });
    m_helloReceived = false;
    m_outputBuffer.clear();
    m_process->start();
}

void HolePlugin::onProcessStarted() {
    if (m_reloadStopping) sendRequest(QStringLiteral("shutdown"));
    else sendHello();
}

void HolePlugin::sendHello() {
    sendRequest(QStringLiteral("hello"));
}

void HolePlugin::sendStart(const QString& method) {
    const QJsonObject request = requestObject();
    if (request.isEmpty()) {
        setError(QStringLiteral("配置 JSON 无效"));
        return;
    }
    sendRequest(method, request);
}

QString HolePlugin::sendRequest(const QString& method, const QJsonObject& params) {
    if (m_reloading && method != QStringLiteral("hello") && method != QStringLiteral("shutdown")) return {};
    if (!m_process || m_process->state() == QProcess::NotRunning) return {};
    QJsonObject request;
    const QString id = QStringLiteral("p%1").arg(++m_requestId);
    if (method == QStringLiteral("start") || method == QStringLiteral("apply_config")) m_pendingRequestId = id;
    if (method == QStringLiteral("start") || method == QStringLiteral("apply_config") || method == QStringLiteral("stop")) {
        m_stateRequestId = m_requestId;
    }
    request.insert(QStringLiteral("jsonrpc"), QStringLiteral("2.0"));
    request.insert(QStringLiteral("id"), id);
    request.insert(QStringLiteral("method"), method);
    if (!params.isEmpty()) request.insert(QStringLiteral("params"), params);
    m_process->write(QJsonDocument(request).toJson(QJsonDocument::Compact));
    m_process->write("\n");
    return id;
}

void HolePlugin::start() {
    if (m_running || m_reloading) return;
    if (!saveConfig()) return;
    m_pendingMethod = QStringLiteral("start");
    ensureProcess();
    if (m_process && m_helloReceived) {
        sendStart(m_pendingMethod);
    }
    setState(QStringLiteral("starting"));
}

void HolePlugin::stop() {
    if (m_reloading) {
        m_resumeAfterReload = false;
        m_pendingMethod.clear();
        if (m_running) { m_running = false; emit runningChanged(); }
        return;
    }
    if (!m_process || m_process->state() == QProcess::NotRunning) {
        setState(QStringLiteral("stopped"));
        return;
    }
    sendRequest(QStringLiteral("stop"));
    m_pendingMethod.clear();
    setState(QStringLiteral("stopping"));
}

bool HolePlugin::applyConfig() {
    if (!saveConfig()) return false;
    if (m_reloading) return true; // The new hello will use the latest config.
    if (!m_process || m_process->state() == QProcess::NotRunning) {
        start();
        return true;
    }

    // A config edit can land while the initial start is waiting for the
    // bridge hello. Keep the start request pending; sendStart() will use the
    // newest in-memory config once hello arrives. If start was already sent,
    // queue the reconfiguration as a separate request without replacing its
    // pending response tracking.
    if (m_pendingMethod == QStringLiteral("start")) {
        if (m_helloReceived) sendStart(QStringLiteral("apply_config"));
        return true;
    }

    m_pendingMethod = QStringLiteral("apply_config");
    if (m_helloReceived) sendStart(m_pendingMethod);
    return true;
}

void HolePlugin::refresh() {
    if (m_reloading) return;
    if (!m_process || m_process->state() == QProcess::NotRunning) {
        ensureProcess();
        return;
    }
    sendRequest(QStringLiteral("snapshot"));
}

void HolePlugin::networkChanged() {
    if (m_process && m_process->state() != QProcess::NotRunning) sendRequest(QStringLiteral("network_changed"));
}

void HolePlugin::renominateTransports() {
    if (m_process && m_process->state() != QProcess::NotRunning) sendRequest(QStringLiteral("renominate_transports"));
}

void HolePlugin::startNATProbe(const QString& optionsJson) {
    if (m_reloading) return;
    QJsonParseError error{};
    const auto document = QJsonDocument::fromJson(optionsJson.toUtf8(), &error);
    if (optionsJson.size() > 16384 || error.error != QJsonParseError::NoError || !document.isObject()) {
        setError(QStringLiteral("NAT 检测参数无效"));
        return;
    }
    setError({});
    m_probeOptions = document.object();
    m_probePending = true;
    ensureProcess();
    if (m_helloReceived) {
        m_probePending = false;
        sendRequest(QStringLiteral("start_nat_probe"), m_probeOptions);
    }
}

void HolePlugin::cancelNATProbe() {
    m_probePending = false;
    if (m_helloReceived) sendRequest(QStringLiteral("cancel_nat_probe"));
}

QString HolePlugin::requestNATPunchProcess(const QString& transportId, const QString& afterSequence,
                                         const QString& generation, const QString& stream) {
    if (m_reloading || !natPunchProcessAvailable() || !m_natPunchProcessRequestId.isEmpty()) return {};
    if (transportId.isEmpty() || transportId.size() > 256 || afterSequence.size() > 20 || generation.size() > 20) return {};
    QJsonObject params;
    params.insert(QStringLiteral("transport_id"), transportId);
    params.insert(QStringLiteral("after_sequence"), afterSequence);
    params.insert(QStringLiteral("generation"), generation);
    params.insert(QStringLiteral("stream"), stream);
    params.insert(QStringLiteral("limit"), 256);
    m_natPunchProcessRequestId = sendRequest(QStringLiteral("nat_punch_process"), params);
    if (!m_natPunchProcessRequestId.isEmpty()) {
        m_natPunchProcessRequests.append(m_natPunchProcessRequestId);
        while (m_natPunchProcessRequests.size() > 8) m_natPunchProcessRequests.removeFirst();
        m_natPunchProcessTimer.start();
    }
    return m_natPunchProcessRequestId;
}

void HolePlugin::clearError() {
    setError({});
}

void HolePlugin::readStandardOutput() {
    m_outputBuffer.append(m_process->readAllStandardOutput());
    while (true) {
        const qsizetype newline = m_outputBuffer.indexOf('\n');
        if (newline < 0) break;
        const QByteArray line = m_outputBuffer.left(newline).trimmed();
        m_outputBuffer.remove(0, newline + 1);
        if (!line.isEmpty()) handleLine(line);
    }
}

void HolePlugin::readStandardError() {
    if (m_process) m_process->readAllStandardError();
}

void HolePlugin::handleLine(const QByteArray& line) {
    if (m_reloadStopping) return; // Discard the retiring process's late replies.
    QJsonParseError error{};
    const QJsonDocument document = QJsonDocument::fromJson(line, &error);
    if (error.error != QJsonParseError::NoError || !document.isObject()) return;
    const QJsonObject object = document.object();

    if (object.value(QStringLiteral("method")).toString() == QStringLiteral("event")) {
        if (m_reloading) return; // Wait for the replacement core's hello first.
        const QJsonObject event = object.value(QStringLiteral("params")).toObject();
        const QString kind = event.value(QStringLiteral("kind")).toString();
        const QString state = event.value(QStringLiteral("state")).toString();
        if (kind == QStringLiteral("engine") && !state.isEmpty()) {
            m_stateRequestId = m_requestId;
            setState(state);
            const bool active = state == QStringLiteral("starting") || state == QStringLiteral("running")
                || state == QStringLiteral("recovering") || state == QStringLiteral("reconfiguring");
            if (m_running != active) {
                m_running = active;
                emit runningChanged();
            }
            if (event.contains(QStringLiteral("error"))) {
                const QJsonObject fault = event.value(QStringLiteral("error")).toObject();
                setError(fault.value(QStringLiteral("message")).toString(QStringLiteral("核心连接失败")));
            } else if (state == QStringLiteral("running")) {
                // A later successful engine event supersedes a stale error
                // from an earlier reconnect attempt.
                setError({});
            }
        }
        if (kind == QStringLiteral("config") && state == QStringLiteral("applying")) {
            // Completion may reuse the current signal/QUIC connection. A
            // subsequent snapshot determines readiness even if events drop.
            m_stateRequestId = m_requestId;
            setState(QStringLiteral("reconfiguring"));
        }
        if (kind == QStringLiteral("session") && event.contains(QStringLiteral("error"))) setError(QStringLiteral("映射会话失败"));
        emit eventReceived(QString::fromUtf8(line));
        if (kind == QStringLiteral("nat_diagnostic") || kind == QStringLiteral("engine") || kind == QStringLiteral("config")) refresh();
        return;
    }

    const QJsonObject errorObject = object.value(QStringLiteral("error")).toObject();
    const QString responseId = object.value(QStringLiteral("id")).toString();
    if (m_natPunchProcessRequests.contains(responseId)) {
        m_natPunchProcessRequests.removeOne(responseId);
        if (responseId != m_natPunchProcessRequestId) return;
        m_natPunchProcessRequestId.clear();
        m_natPunchProcessTimer.stop();
        if (!errorObject.isEmpty()) {
            const QJsonObject data = errorObject.value(QStringLiteral("data")).toObject();
            emit natPunchProcessFailed(responseId, data.value(QStringLiteral("message")).toString(QStringLiteral("过程记录读取失败")));
        } else {
            const QJsonObject result = object.value(QStringLiteral("result")).toObject();
            emit natPunchProcessReady(responseId, QString::fromUtf8(QJsonDocument(result).toJson(QJsonDocument::Compact)));
        }
        return;
    }
    if (!errorObject.isEmpty()) {
        const QJsonObject data = errorObject.value(QStringLiteral("data")).toObject();
        if (m_reloading) {
            failReload(data.value(QStringLiteral("message")).toString(QStringLiteral("新核心握手失败")));
            if (m_process) m_process->kill();
            return;
        }
        setError(data.value(QStringLiteral("message")).toString(QStringLiteral("核心请求失败")));
        if (object.value(QStringLiteral("id")).toString() == m_pendingRequestId) {
            emit configApplyFailed(m_lastError);
            m_pendingMethod.clear();
            m_pendingRequestId.clear();
            if (!m_running) setState(QStringLiteral("error"));
            refresh();
        }
        return;
    }

    const QJsonObject result = object.value(QStringLiteral("result")).toObject();
    if (result.contains(QStringLiteral("bridge_version"))) {
        // The hello response is not the start response. Keep the pending
        // operation alive until its own reply arrives; otherwise the first
        // start request is lost and the UI gets a false running state.
        m_helloReceived = true;
        m_natPunchProcessAvailable = result.value(QStringLiteral("methods")).toArray().contains(QJsonValue(QStringLiteral("nat_punch_process")));
        emit capabilitiesChanged();
        if (m_reloading) {
            m_reloadTimer.stop();
            m_reloading = false;
            const bool resume = m_resumeAfterReload;
            m_resumeAfterReload = false;
            // A new start failure must not inherit the old process's running flag.
            if (m_running) { m_running = false; emit runningChanged(); }
            setState(resume ? QStringLiteral("starting") : QStringLiteral("stopped"));
        }
        if (!m_pendingMethod.isEmpty()) sendStart(m_pendingMethod);
        if (m_probePending) { m_probePending = false; sendRequest(QStringLiteral("start_nat_probe"), m_probeOptions); }
        refresh();
        return;
    }

    const QString method = m_pendingMethod;
    if ((method == QStringLiteral("start") || method == QStringLiteral("apply_config")) && object.value(QStringLiteral("id")).toString() == m_pendingRequestId) {
        if (method == QStringLiteral("start") && !m_running) {
            m_running = true;
            emit runningChanged();
        }
        m_pendingMethod.clear();
        m_pendingRequestId.clear();
        refresh();
    }
    if (result.contains(QStringLiteral("snapshot"))) {
        // Ignore snapshots requested before a command or newer state event;
        // their replies can still be queued in the bridge output.
        const quint64 requestId = object.value(QStringLiteral("id")).toString().mid(1).toULongLong();
        if (requestId <= m_stateRequestId) return;
        m_stateRequestId = requestId;
        const QJsonObject snapshot = result.value(QStringLiteral("snapshot")).toObject();
        const QString state = snapshot.value(QStringLiteral("engine_state")).toString();
        if (!state.isEmpty()) setState(state);
        if (snapshot.contains(QStringLiteral("run_requested"))) {
            const bool active = snapshot.value(QStringLiteral("run_requested")).toBool();
            if (m_running != active) {
                m_running = active;
                emit runningChanged();
            }
        }
        const QJsonObject fault = snapshot.value(QStringLiteral("error")).toObject();
        if (!fault.isEmpty()) setError(fault.value(QStringLiteral("message")).toString());
        m_snapshotJson = QString::fromUtf8(QJsonDocument(snapshot).toJson(QJsonDocument::Compact));
        emit snapshotChanged();
    }
}

void HolePlugin::clearProcessState() {
    m_helloReceived = false;
    m_natPunchProcessAvailable = false;
    m_natPunchProcessTimer.stop();
    const QString processRequest = m_natPunchProcessRequestId;
    m_natPunchProcessRequestId.clear();
    m_natPunchProcessRequests.clear();
    if (!processRequest.isEmpty()) emit natPunchProcessFailed(processRequest, QStringLiteral("核心进程已结束"));
    emit capabilitiesChanged();
    m_outputBuffer.clear();
    m_pendingMethod.clear();
    m_pendingRequestId.clear();
    m_stateRequestId = m_requestId;
    m_probePending = false;
    m_probeOptions = {};
    m_snapshotJson.clear();
    emit snapshotChanged();
}

void HolePlugin::failReload(const QString& message) {
    m_reloadTimer.stop();
    m_reloading = m_reloadStopping = m_resumeAfterReload = false;
    if (m_running) {
        m_running = false;
        emit runningChanged();
    }
    setError(message);
    setState(QStringLiteral("error"));
    clearProcessState();
}

void HolePlugin::reloadCore() {
    if (m_reloading) return;
    if (m_pluginDirectory.isEmpty()) {
        setError(QStringLiteral("无法确定插件目录"));
        return;
    }
    const QFileInfo core(QDir(m_pluginDirectory).filePath(QStringLiteral("hole-core")));
    if (!core.isFile() || !core.isExecutable()) {
        setError(QStringLiteral("核心文件不存在或没有执行权限，无法重载"));
        return;
    }
    m_resumeAfterReload = m_state != QStringLiteral("stopping")
        && (m_running || m_pendingMethod == QStringLiteral("start") || m_pendingMethod == QStringLiteral("apply_config"));
    m_reloading = true;
    m_reloadStopping = m_process && m_process->state() != QProcess::NotRunning;
    setError({});
    setState(QStringLiteral("reloading"));
    clearProcessState();
    if (m_reloadStopping) {
        m_reloadTimer.start(2000);
        if (m_process->state() == QProcess::Running) sendRequest(QStringLiteral("shutdown"));
    } else {
        m_pendingMethod = m_resumeAfterReload ? QStringLiteral("start") : QString();
        m_reloadTimer.start(10000);
        ensureProcess();
    }
}

void HolePlugin::onProcessFinished(int, QProcess::ExitStatus) {
    clearProcessState();
    if (m_reloadStopping) {
        m_reloadStopping = false;
        m_pendingMethod = m_resumeAfterReload ? QStringLiteral("start") : QString();
        m_reloadTimer.start(10000);
        ensureProcess();
        return;
    }
    if (m_reloading) {
        failReload(QStringLiteral("新核心在握手完成前退出，请检查核心文件后重试"));
        return;
    }
    if (m_running) {
        m_running = false;
        emit runningChanged();
    }
    if (m_state != QStringLiteral("error")) setState(QStringLiteral("stopped"));
}
