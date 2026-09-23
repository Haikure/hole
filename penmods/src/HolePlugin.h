#pragma once

#include <QJsonObject>
#include <QProcess>
#include <QObject>
#include <QString>

class QQmlEngine;

class HolePlugin final : public QObject {
    Q_OBJECT
    Q_PROPERTY(QString state READ state NOTIFY stateChanged)
    Q_PROPERTY(bool running READ running NOTIFY runningChanged)
    Q_PROPERTY(QString serverUrl READ serverUrl WRITE setServerUrl NOTIFY configChanged)
    Q_PROPERTY(QString configJson READ configJson WRITE setConfigJson NOTIFY configChanged)
    Q_PROPERTY(QString mappingStateJson READ mappingStateJson WRITE setMappingStateJson NOTIFY configChanged)
    Q_PROPERTY(QString lastError READ lastError NOTIFY lastErrorChanged)
    Q_PROPERTY(QString snapshotJson READ snapshotJson NOTIFY snapshotChanged)
    Q_PROPERTY(QString pluginDirectory READ pluginDirectory CONSTANT)

public:
    explicit HolePlugin(QObject* parent = nullptr);
    ~HolePlugin() override;

    QString state() const { return m_state; }
    bool running() const { return m_running; }
    QString serverUrl() const { return m_serverUrl; }
    QString configJson() const { return m_configJson; }
    QString mappingStateJson() const { return m_mappingStateJson; }
    QString lastError() const { return m_lastError; }
    QString snapshotJson() const { return m_snapshotJson; }
    QString pluginDirectory() const { return m_pluginDirectory; }

    void setServerUrl(const QString& value);
    void setConfigJson(const QString& value);
    void setMappingStateJson(const QString& value);

    Q_INVOKABLE void start();
    Q_INVOKABLE void stop();
    Q_INVOKABLE void applyConfig();
    Q_INVOKABLE void refresh();
    Q_INVOKABLE void networkChanged();
    Q_INVOKABLE void renominateTransports();
    Q_INVOKABLE bool saveConfig();
    Q_INVOKABLE bool loadConfig();
    Q_INVOKABLE void clearError();

signals:
    void stateChanged();
    void runningChanged();
    void configChanged();
    void lastErrorChanged();
    void snapshotChanged();
    void eventReceived(const QString& eventJson);

private slots:
    void onProcessStarted();
    void onProcessFinished(int exitCode, QProcess::ExitStatus status);
    void readStandardOutput();
    void readStandardError();

private:
    void ensureProcess();
    void sendHello();
    void sendStart(const QString& method);
    void sendRequest(const QString& method, const QJsonObject& params = {});
    void handleLine(const QByteArray& line);
    void setState(const QString& value);
    void setError(const QString& value);
    QJsonObject requestObject() const;
    QString configPath() const;

    QProcess* m_process = nullptr;
    QString m_pluginDirectory;
    QString m_state = QStringLiteral("stopped");
    QString m_serverUrl;
    QString m_configJson = QStringLiteral("{\"room\":\"\",\"password\":\"\",\"token\":\"\",\"device_name\":\"pen\",\"session_timeout\":\"10m\",\"transport\":{\"preferred\":\"ice\",\"allow_legacy\":true},\"provide\":[],\"consume\":[]}");
    QString m_mappingStateJson;
    QString m_lastError;
    QString m_snapshotJson;
    QByteArray m_outputBuffer;
    quint64 m_requestId = 0;
    bool m_running = false;
    bool m_helloReceived = false;
    QString m_pendingMethod;
};

extern "C" void init_plugin();
extern "C" void attach_engine(QQmlEngine* engine);
extern "C" void destroy_plugin();
