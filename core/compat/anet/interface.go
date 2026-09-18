//go:build !android

// Package anet is the narrow public-net compatibility surface used by Pion.
package anet

import "net"

func Interfaces() ([]net.Interface, error)                           { return net.Interfaces() }
func InterfaceAddrs() ([]net.Addr, error)                            { return net.InterfaceAddrs() }
func InterfaceAddrsByInterface(i *net.Interface) ([]net.Addr, error) { return i.Addrs() }
func InterfaceByIndex(index int) (*net.Interface, error)             { return net.InterfaceByIndex(index) }
func InterfaceByName(name string) (*net.Interface, error)            { return net.InterfaceByName(name) }
func SetAndroidVersion(uint)                                         {}
