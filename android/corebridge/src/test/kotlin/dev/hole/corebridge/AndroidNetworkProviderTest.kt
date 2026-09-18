package dev.hole.corebridge

import android.net.ConnectivityManager
import android.net.LinkAddress
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkInfo
import java.net.InetAddress
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import org.json.JSONArray
import org.json.JSONObject
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.shadows.ShadowNetwork
import org.robolectric.shadows.ShadowNetworkInfo
import org.robolectric.util.ReflectionHelpers
import org.robolectric.util.ReflectionHelpers.ClassParameter

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26, 36])
@Suppress("DEPRECATION")
class AndroidNetworkProviderTest {
    private val context get() = RuntimeEnvironment.getApplication()
    private val manager get() = context.getSystemService(ConnectivityManager::class.java)
    private val shadow get() = shadowOf(manager)

    @Before
    fun clearNetworks() { shadow.clearAllNetworks() }

    private fun addNetwork(id: Int, name: String, transport: Int, vararg addresses: String): Network {
        val network = ShadowNetwork.newInstance(id)
        val info = ShadowNetworkInfo.newInstance(
            NetworkInfo.DetailedState.CONNECTED, ConnectivityManager.TYPE_WIFI, 0, true, NetworkInfo.State.CONNECTED,
        )
        shadow.addNetwork(network, info)
        shadow.setNetworkCapabilities(network, NetworkCapabilities().apply {
            shadowOf(this).addTransportType(transport)
            ReflectionHelpers.callInstanceMethod<NetworkCapabilities>(this, "addCapability",
                ClassParameter.from(Int::class.javaPrimitiveType, NetworkCapabilities.NET_CAPABILITY_INTERNET))
        })
        shadow.setLinkProperties(network, properties(name, *addresses))
        return network
    }

    private fun properties(name: String, vararg addresses: String) = LinkProperties().apply {
        interfaceName = name
        setLinkAddresses(addresses.map {
            ReflectionHelpers.callConstructor(
                LinkAddress::class.java,
                ClassParameter.from(InetAddress::class.java, InetAddress.getByName(it)),
                ClassParameter.from(Int::class.javaPrimitiveType, if (it.contains(':')) 64 else 24),
            )
        })
    }

    @Test
    fun publicPlatformSnapshotIncludesWifiAndCellularButNotVpn() {
        addNetwork(101, "wlan0", NetworkCapabilities.TRANSPORT_WIFI, "2001:db8::1", "192.168.1.2")
        addNetwork(102, "rmnet_data0", NetworkCapabilities.TRANSPORT_CELLULAR, "2001:db8::2")
        addNetwork(103, "tun0", NetworkCapabilities.TRANSPORT_VPN, "fd7a:115c:a1e0::1")
        val snapshot = JSONArray(AndroidNetworkProvider(context).interfacesJSON())
        val names = (0 until snapshot.length()).map { snapshot.getJSONObject(it).getString("name") }.toSet()
        assertEquals(setOf("wlan0", "rmnet_data0"), names)
        for (index in 0 until snapshot.length()) {
            val item = snapshot.getJSONObject(index)
            assertTrue(item.getBoolean("up"))
            assertEquals(false, item.getBoolean("loopback"))
            assertTrue(item.getJSONArray("addresses").length() > 0)
        }
    }

    @Test
    fun eachReadSeesNewAddressesAndNetworkLoss() {
        val network = addNetwork(101, "wlan0", NetworkCapabilities.TRANSPORT_WIFI, "2001:db8::1")
        val provider = AndroidNetworkProvider(context)
        val first = provider.interfacesJSON()
        shadow.setLinkProperties(network, properties("wlan0", "2001:db8::9"))
        val updated = JSONArray(provider.interfacesJSON()).getJSONObject(0).getJSONArray("addresses")
        assertEquals(InetAddress.getByName("2001:db8::9").hostAddress, updated.getString(0))
        assertTrue(first != provider.interfacesJSON())
        shadow.clearAllNetworks()
        assertEquals(0, JSONArray(provider.interfacesJSON()).length())
    }

    @Test
    fun missingLinkPropertiesAreSkippedWithoutInventingAddresses() {
        val network = addNetwork(101, "wlan0", NetworkCapabilities.TRANSPORT_WIFI, "2001:db8::1")
        shadow.setLinkProperties(network, null)
        assertEquals(0, JSONArray(AndroidNetworkProvider(context).interfacesJSON()).length())
    }

    @Test
    fun monitoringUsesCallbackDnsAndAddressesAndIgnoresOldNetworkLoss() {
        val old = addNetwork(101, "wlan0", NetworkCapabilities.TRANSPORT_WIFI, "2001:db8::1")
        val next = addNetwork(102, "rmnet0", NetworkCapabilities.TRANSPORT_CELLULAR, "2001:db8::2")
        val provider = AndroidNetworkProvider(context)
        val events = mutableListOf<String>()
        assertTrue(provider.startMonitoring { events += it })
        assertFalse(provider.startMonitoring { error("duplicate observer") })
        val physical = ReflectionHelpers.getField<ConnectivityManager.NetworkCallback>(provider, "callback")
        val primary = ReflectionHelpers.getField<ConnectivityManager.NetworkCallback>(provider, "defaultCallback")
        primary.onAvailable(next)
        val updated = properties("rmnet0", "2001:db8::9").apply {
            ReflectionHelpers.callInstanceMethod<Boolean>(this, "addDnsServer", ClassParameter.from(InetAddress::class.java, InetAddress.getByName("2001:db8::53")))
        }
        physical.onLinkPropertiesChanged(next, updated)
        val selected = JSONObject(provider.networkJSON())
        assertEquals(next.networkHandle.toString(), selected.getString("handle"))
        assertEquals(InetAddress.getByName("2001:db8::9").hostAddress, selected.getJSONArray("addresses").getString(0))
        assertEquals(InetAddress.getByName("2001:db8::53").hostAddress, selected.getJSONArray("dns").getString(0))
        val beforeLoss = events.size
        physical.onLost(old)
        assertEquals(beforeLoss, events.size)
        assertEquals(selected.toString(), provider.networkJSON())
        provider.close()
    }

    @Test
    fun stoppedAndPreviousRegistrationCallbacksNeverReviveNetwork() {
        val network = addNetwork(101, "wlan0", NetworkCapabilities.TRANSPORT_WIFI, "2001:db8::1")
        val provider = AndroidNetworkProvider(context)
        val events = mutableListOf<String>()
        provider.startMonitoring { events += it }
        val oldCallback = ReflectionHelpers.getField<ConnectivityManager.NetworkCallback>(provider, "callback")
        provider.close()
        oldCallback.onAvailable(network)
        assertFalse(JSONObject(provider.networkJSON()).getBoolean("available"))
        assertTrue(events.isEmpty())
        provider.startMonitoring { events += it }
        val snapshot = provider.networkJSON()
        oldCallback.onLost(network)
        assertEquals(snapshot, provider.networkJSON())
        assertTrue(events.isEmpty())
        assertFailsWith<Exception> { provider.bindSocket(-1, network.networkHandle.toString()) }
        assertFailsWith<Exception> { provider.bindSocket(0, "obsolete-handle") }
        provider.close()
        assertFailsWith<Exception> { provider.lookupIP("localhost", network.networkHandle.toString()) }
    }
}
