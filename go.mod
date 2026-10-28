module hole

go 1.27

toolchain go1.27.3

require (
	github.com/gorilla/websocket v1.5.3
	github.com/pion/ice/v4 v4.4.2
	github.com/pion/logging v0.2.4
	github.com/pion/opus v0.1.1-0.20261001050837-cfb9e7a35aad
	github.com/pion/stun/v4 v4.0.0
	github.com/pion/transport/v4 v4.1.0
	github.com/pion/turn/v5 v5.1.0
	github.com/quic-go/quic-go v0.63.0
	golang.org/x/net v0.56.0
	golang.org/x/sys v0.47.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/pion/dtls/v3 v3.1.8 // indirect
	github.com/pion/mdns/v2 v2.2.0 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/time v0.14.0 // indirect
)

replace github.com/wlynxg/anet => ./core/compat/anet
