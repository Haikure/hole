package core

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MinecraftMetadata is a value so config/directory snapshots never alias it.
// Zero means the provider did not publish world details (older mod versions).
type MinecraftMetadata struct {
	Description string `json:"description" yaml:"description"`
	Version     string `json:"version" yaml:"version"`
	Online      int    `json:"online" yaml:"online"`
	Max         int    `json:"max" yaml:"max"`
}

func validMinecraftMetadata(m MinecraftMetadata, kind string) bool {
	if m == (MinecraftMetadata{}) {
		return true
	}
	clean := func(s string, limit int) bool {
		return len(s) <= limit && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
	}
	return kind == "minecraft" && m.Version != "" && clean(m.Version, 32) && clean(m.Description, 192) && m.Online >= 0 && m.Online <= 1000000 && m.Max >= 0 && m.Max <= 1000000
}

// ServiceAdvertisement deliberately omits the provider's local address.
// Discovery is room-scoped; a consume declaration still authorizes a mapping.
type ServiceAdvertisement struct {
	ID        string            `json:"id"`
	Protocol  string            `json:"protocol"`
	Kind      string            `json:"kind,omitempty"`
	Minecraft MinecraftMetadata `json:"minecraft,omitzero"`
}

type DiscoveredService struct {
	ServiceAdvertisement
	DeviceName string `json:"device_name"`
	RuntimeID  string `json:"runtime_id"`
}

func validServiceAdvertisements(items []ServiceAdvertisement) bool {
	if len(items) > 64 {
		return false
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if !validMinecraftMetadata(item.Minecraft, item.Kind) {
			return false
		}
		if item.Kind != "" && (item.Kind != "minecraft" || item.Protocol != "tcp") {
			return false
		}
		if item.ID == "" || len(item.ID) > 256 || strings.IndexFunc(item.ID, func(r rune) bool { return unicode.IsSpace(r) || r < 32 }) >= 0 || seen[item.ID] || (item.Protocol != "tcp" && item.Protocol != "udp") {
			return false
		}
		seen[item.ID] = true
	}
	return true
}

// Called under roomControl.mu. Includes all authenticated room members, even
// before a data connection exists; Members remains the reachable graph view.
func (r *roomControl) servicesLocked() []DiscoveredService {
	items := []DiscoveredService{}
	for _, member := range r.directory {
		for _, service := range member.Services {
			// Earlier experimental Workers advertised unmarked provide entries.
			// Preserve room compatibility without exposing those as discoveries.
			if service.Kind == "" {
				continue
			}
			items = append(items, DiscoveredService{service, member.DeviceName, member.RuntimeID})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DeviceName != items[j].DeviceName {
			return items[i].DeviceName < items[j].DeviceName
		}
		return items[i].ID < items[j].ID
	})
	return items
}
