package dev.hole.corebridge

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Network
import android.net.NetworkRequest
import android.net.LinkProperties
import android.os.ParcelFileDescriptor
import dev.hole.core.mobile.NetworkBinding
import org.json.JSONArray
import org.json.JSONObject

/**
 * Android 限制应用直接通过 NETLINK_ROUTE 枚举网卡；使用系统服务提供的
 * LinkProperties。只持有 application context 的服务，不创建 VPN、不请求提权。
 * 每次读取最新地址，既用于启动，也用于 Go 核心已有的候选地址刷新。
 */
class AndroidNetworkProvider(context: Context) : NetworkBinding, AutoCloseable {
    private val connectivity = requireNotNull(context.applicationContext.getSystemService(ConnectivityManager::class.java))
    private val lock = Any()
    private var observer: ((String) -> Unit)? = null
    private var sequence = 0L
    private var monitoring = false
    private var monitorGeneration = 0L
    private var selected: Network? = null
    private var preferred: Network? = null
    private var snapshot = JSONObject().put("available", false).put("handle", "0").toString()
    private val lost = mutableSetOf<Network>()
    private data class LinkFacts(val interfaceName: String?, val addresses: List<String>, val dns: List<String>)
    private fun linkFacts(value: LinkProperties) = LinkFacts(
        value.interfaceName,
        value.linkAddresses.mapNotNull { it.address.hostAddress?.substringBefore('%') },
        value.dnsServers.mapNotNull { it.hostAddress },
    )
    private val properties = mutableMapOf<Network, LinkFacts>()
    private val capabilities = mutableMapOf<Network, NetworkCapabilities>()
    private var callback: ConnectivityManager.NetworkCallback? = null
    private var defaultCallback: ConnectivityManager.NetworkCallback? = null

    private fun callback(generation: Long, default: Boolean) = object : ConnectivityManager.NetworkCallback() {
        private fun changed(update: () -> Unit) = synchronized(lock) {
            if (!monitoring || generation != monitorGeneration) return@synchronized
            update()
            refresh()
        }
        override fun onAvailable(network: Network) = changed {
            lost.remove(network)
            if (default) preferred = network
        }
        override fun onLost(network: Network) = changed {
            if (preferred == network) preferred = null
            if (!default) {
                lost.add(network)
                properties.remove(network)
                capabilities.remove(network)
            }
        }
        override fun onCapabilitiesChanged(network: Network, value: NetworkCapabilities) = changed {
            capabilities[network] = NetworkCapabilities(value)
        }
        override fun onLinkPropertiesChanged(network: Network, value: LinkProperties) = changed {
            properties[network] = linkFacts(value)
        }
    }

    /** Registration begins only for a requested run; constructing/binding UI stays offline. */
    fun startMonitoring(onChanged: (String) -> Unit): Boolean = synchronized(lock) {
        if (monitoring) return@synchronized false
        observer = onChanged
        monitoring = true
        monitorGeneration++
        lost.clear()
        val physical = callback(monitorGeneration, default = false)
        val primary = callback(monitorGeneration, default = true)
        callback = physical
        defaultCallback = primary
        try {
            connectivity.registerNetworkCallback(
                NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN).build(), physical,
            )
            connectivity.registerDefaultNetworkCallback(primary)
            refresh(notify = false)
        } catch (failure: Exception) {
            close()
            throw failure
        }
        true
    }

    /** Re-read platform facts, not the callback's old Network, so late onLost is harmless. */
    @Suppress("DEPRECATION")
    fun refresh(force: Boolean = false, notify: Boolean = true) = synchronized(lock) {
        if (!monitoring) return@synchronized
        val active = preferred ?: connectivity.activeNetwork
        fun caps(network: Network) = capabilities[network] ?: connectivity.getNetworkCapabilities(network)
        fun links(network: Network) = properties[network] ?: connectivity.getLinkProperties(network)?.let(::linkFacts)
        val networks = (listOfNotNull(active) + connectivity.allNetworks + properties.keys + capabilities.keys).distinct().filter { network ->
            val caps = caps(network)
            network !in lost && caps != null && !caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN) &&
                caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) && links(network)?.interfaceName != null
        }
        val network = networks.firstOrNull { it == active } ?: networks.firstOrNull {
            caps(it)?.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED) == true
        } ?: networks.firstOrNull()
        val properties = network?.let(::links)
        val capabilities = network?.let(::caps)
        val next = JSONObject().apply {
            put("handle", network?.networkHandle?.toString() ?: "0")
            put("available", network != null)
            put("interface", properties?.interfaceName ?: "")
            put("addresses", JSONArray(properties?.addresses?.sorted().orEmpty()))
            put("dns", JSONArray(properties?.dns?.sorted().orEmpty()))
            put("validated", capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED) == true)
            put("metered", capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) != true)
            put("transport", when {
                capabilities == null -> "none"
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "Wi-Fi"
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "移动网络"
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "以太网"
                else -> "其他网络"
            })
        }.toString()
        selected = network
        if (snapshot != next || force) {
            snapshot = next
            sequence++
            if (notify) observer?.invoke(JSONObject().put("sequence", sequence.toString())
                .put("network_handle", network?.networkHandle?.toString() ?: "0").toString())
        }
    }

    override fun networkJSON(): String = synchronized(lock) { snapshot }

    private fun requireNetwork(handle: String): Network = synchronized(lock) {
        check(monitoring) { "网络监测已停止" }
        requireNotNull(selected?.takeIf { it.networkHandle.toString() == handle && it !in lost }) { "所选网络已失效" }
    }

    override fun bindSocket(fd: Long, networkHandle: String) {
        val network = requireNetwork(networkHandle)
        require(fd in 0..Int.MAX_VALUE.toLong()) { "无效的 socket fd" }
        // fromFd duplicates the borrowed fd. bindSocket changes the same socket;
        // closing the duplicate never closes Go's original owned descriptor.
        ParcelFileDescriptor.fromFd(fd.toInt()).use { network.bindSocket(it.fileDescriptor) }
        requireNetwork(networkHandle)
    }

    override fun lookupIP(host: String, networkHandle: String): String {
        val network = requireNetwork(networkHandle)
        val addresses = network.getAllByName(host).mapNotNull { it.hostAddress?.substringBefore('%') }
        requireNetwork(networkHandle)
        return JSONArray(addresses).toString()
    }

    override fun close() = synchronized(lock) {
        monitoring = false
        monitorGeneration++
        observer = null
        callback?.let { runCatching { connectivity.unregisterNetworkCallback(it) } }
        defaultCallback?.let { runCatching { connectivity.unregisterNetworkCallback(it) } }
        callback = null
        defaultCallback = null
        selected = null
        preferred = null
        lost.clear()
        properties.clear()
        capabilities.clear()
        snapshot = JSONObject().put("available", false).put("handle", "0").toString()
    }

    // Legacy API 1 constructor compatibility; bound runs use networkJSON instead.
    @Suppress("DEPRECATION")
    override fun interfacesJSON(): String {
        val networks = buildList {
            connectivity.activeNetwork?.let { add(it) }
            addAll(connectivity.allNetworks)
        }.distinct()
        val addressesByInterface = linkedMapOf<String, MutableSet<String>>()
        for (network in networks) {
            val capabilities = connectivity.getNetworkCapabilities(network) ?: continue
            // 仅发布物理网络地址，不把 Tailscale 等 VPN 隧道地址当作公网打洞地址。
            if (capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) continue
            val properties = connectivity.getLinkProperties(network) ?: continue
            val name = properties.interfaceName?.takeIf { it.isNotBlank() } ?: continue
            val addresses = addressesByInterface.getOrPut(name) { linkedSetOf() }
            for (link in properties.linkAddresses) {
                // 区域标识只用于链路本地地址；Core 仍统一执行公网 IPv6 过滤和网卡筛选。
                link.address.hostAddress?.substringBefore('%')?.let { addresses.add(it) }
            }
        }
        return JSONArray().apply {
            addressesByInterface.forEach { (name, addresses) ->
                put(JSONObject().apply {
                    put("name", name)
                    put("up", true)
                    put("loopback", false)
                    put("addresses", JSONArray(addresses.toList()))
                })
            }
        }.toString()
    }
}
