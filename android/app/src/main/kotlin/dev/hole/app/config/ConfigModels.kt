package dev.hole.app.config

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// Android 宿主配置模型。entry_id / enabled / 主题等宿主字段只存在于这一层；
// 交给 Go 核心的有效配置由 ConfigRules.toStartRequest 转换，不包含这些字段。

@Serializable
data class ProvideEntry(
    val entryId: String,
    val id: String = "",
    // "tcp" 或 "udp"
    val protocol: String = "tcp",
    // 服务地址：允许域名、IPv4、IPv6；IPv6 存储时去掉方括号。
    val host: String = "",
    val port: Int = 0,
    val enabled: Boolean = true,
)

@Serializable
data class ConsumeEntry(
    val entryId: String,
    val id: String = "",
    // 本地监听地址：必须是字面 IP（IPv4 或 IPv6）；IPv6 存储时去掉方括号。
    val host: String = "127.0.0.1",
    val port: Int = 0,
    val enabled: Boolean = true,
)

@Serializable
data class ConnectionSettings(
    val serverUrl: String = "",
    val room: String = "",
    val deviceName: String = "",
    // Go 时长字符串，例如 "10m"；经 ConfigRules.isValidDuration 校验。
    val sessionTimeout: String = "10m",
    val candidateInterfaces: List<String> = emptyList(),
    val candidateAddresses: List<String> = emptyList(),
    // 凭据密文 = Base64(iv || AES-GCM 密文)，密钥在 Android Keystore。
    // 明文仅在运行内存中出现，不落盘、不进快照。
    val passwordCipher: String = "",
    val tokenCipher: String = "",
    val connectionMode: String = "auto",
    val allowInsecureSignal: Boolean = false,
    val ice: IceSettings = IceSettings(),
    val turn: TurnSettings = TurnSettings(),
    val turnCredentialCipher: String = "",
)

@Serializable
data class StoredConfig(
    val schemaVersion: Int = 2,
    // "material" | "miuix"；旧配置缺少此字段时保留原有 Material 3 外观。
    val themeStyle: String = ThemeStyle.MATERIAL.value,
    // "system" | "light" | "dark"
    val themeMode: String = "system",
    val dynamicColor: Boolean = true,
    // Miuix 默认使用官方 HyperOS 蓝色；壁纸配色独立保存，不继承 Material 的 Monet 开关。
    val miuixDynamicColor: Boolean = false,
    val connection: ConnectionSettings = ConnectionSettings(),
    val provide: List<ProvideEntry> = emptyList(),
    val consume: List<ConsumeEntry> = emptyList(),
)

// ---- 交给 Go 核心的有效配置（mobile facade API 1 请求格式，字段名与 mobile/README.md 一致）----

@Serializable
data class CoreProvide(
    @SerialName("id") val id: String,
    @SerialName("service") val service: String,
)

@Serializable
data class CoreConsume(
    @SerialName("id") val id: String,
    @SerialName("expose") val expose: String,
)

@Serializable
data class CoreConfig(
    @SerialName("room") val room: String,
    @SerialName("password") val password: String,
    @SerialName("token") val token: String,
    @SerialName("device_name") val deviceName: String,
    @SerialName("session_timeout") val sessionTimeout: String,
    @SerialName("candidate_interfaces") val candidateInterfaces: List<String>,
    @SerialName("candidate_addresses") val candidateAddresses: List<String>,
    // 空集合必须序列化为 []，核心要求 provide/consume 以数组出现。
    @SerialName("provide") val provide: List<CoreProvide> = emptyList(),
    @SerialName("consume") val consume: List<CoreConsume> = emptyList(),
    val transport: CoreTransport = CoreTransport(),
    val ice: IceSettings = IceSettings(),
    val turn: CoreTurn = CoreTurn(),
)

@Serializable
data class StartRequest(
    @SerialName("api_version") val apiVersion: Int = 1,
    @SerialName("server_url") val serverUrl: String,
    @SerialName("config") val config: CoreConfig,
)
