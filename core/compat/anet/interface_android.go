//go:build android

package anet

import (
	"errors"
	"net"
)

// Android always uses the application's pinned ICE Platform. A library default
// is an explicit configuration error, never a private-Go-symbol/netlink fallback.
var errProvider = errors.New("Android ICE requires the injected Network provider")

func Interfaces() ([]net.Interface, error)                         { return nil, errProvider }
func InterfaceAddrs() ([]net.Addr, error)                          { return nil, errProvider }
func InterfaceAddrsByInterface(*net.Interface) ([]net.Addr, error) { return nil, errProvider }
func InterfaceByIndex(int) (*net.Interface, error)                 { return nil, errProvider }
func InterfaceByName(string) (*net.Interface, error)               { return nil, errProvider }
func SetAndroidVersion(uint)                                       {}
