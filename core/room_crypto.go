package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
)

const maxRoomFrame = 128 * 1024
const maxRoomHops = 16

// Hops is deliberately outside the signature: each authenticated forwarder
// decrements it. IDs, identities, destinations and the exact body are immutable.
type roomFrame struct {
	ID            string `json:"id"`
	Room          string `json:"room"`
	Origin        string `json:"origin"`
	Runtime       string `json:"runtime"`
	Target        string `json:"target,omitempty"`
	TargetRuntime string `json:"target_runtime,omitempty"`
	Kind          string `json:"kind"`
	Body          string `json:"body"`
	Certificate   string `json:"certificate,omitempty"`
	Signature     string `json:"signature"`
	Hops          int    `json:"hops"`
}

func roomFrameSigningBytes(f roomFrame) []byte {
	data := []byte("HRC1")
	for _, value := range []string{f.Room, f.ID, f.Origin, f.Runtime, f.Target, f.TargetRuntime, f.Kind, f.Body, f.Certificate} {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		data = append(data, size[:]...)
		data = append(data, value...)
	}
	return data
}

func (f roomFrame) validSize() bool {
	if _, err := parseSessionID(f.ID); err != nil {
		return false
	}
	if f.Hops < 0 || f.Hops > maxRoomHops || len(f.Room) > 128 || len(f.Origin) > 128 || len(f.Runtime) > 128 || len(f.Target) > 128 || len(f.TargetRuntime) > 128 || len(f.Certificate) > 4096 || len(f.Signature) > 128 || len(f.Body) > maxRoomFrame {
		return false
	}
	data, err := json.Marshal(f)
	return err == nil && len(data) <= maxRoomFrame
}

func signRoomFrame(key *ecdsa.PrivateKey, f *roomFrame) error {
	if key == nil || key.Curve.Params().BitSize != 256 {
		return errors.New("unsupported room signing key")
	}
	sum := sha256.Sum256(roomFrameSigningBytes(*f))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		return err
	}
	var signature [64]byte
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	f.Signature = base64.StdEncoding.EncodeToString(signature[:])
	return nil
}

func verifyRoomFrame(key *ecdsa.PublicKey, f roomFrame) bool {
	if key == nil || key.Curve.Params().BitSize != 256 || !f.validSize() {
		return false
	}
	signature, err := base64.StdEncoding.DecodeString(f.Signature)
	if err != nil || len(signature) != 64 {
		return false
	}
	sum := sha256.Sum256(roomFrameSigningBytes(f))
	return ecdsa.Verify(key, sum[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:]))
}

func roomCertificate(value, fingerprint string) (*x509.Certificate, *ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(der) > 3072 {
		return nil, nil, errors.New("invalid room certificate")
	}
	sum := sha256.Sum256(der)
	if hex.EncodeToString(sum[:]) != fingerprint {
		return nil, nil, errors.New("room certificate fingerprint mismatch")
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	key, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve.Params().BitSize != 256 {
		return nil, nil, errors.New("unsupported room certificate")
	}
	return certificate, key, nil
}

type roomEncryptedBody struct {
	Key        string `json:"key"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func decryptRoomNotice(private *ecdsa.PrivateKey, f roomFrame) ([]byte, error) {
	encoded, err := base64.StdEncoding.DecodeString(f.Body)
	if err != nil || len(encoded) > maxRoomFrame {
		return nil, errors.New("invalid encrypted room notice")
	}
	var envelope roomEncryptedBody
	if json.Unmarshal(encoded, &envelope) != nil || private == nil {
		return nil, errors.New("invalid encrypted room notice")
	}
	der, err := base64.StdEncoding.DecodeString(envelope.Key)
	if err != nil || len(der) > 256 {
		return nil, errors.New("invalid notice key")
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	public, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("invalid notice key")
	}
	local, err := private.ECDH()
	if err != nil {
		return nil, err
	}
	remote, err := public.ECDH()
	if err != nil {
		return nil, err
	}
	shared, err := local.ECDH(remote)
	if err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, shared, []byte(f.Room), "hole-room-notice:"+f.ID, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid notice nonce")
	}
	data, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, data, []byte(f.ID))
}
