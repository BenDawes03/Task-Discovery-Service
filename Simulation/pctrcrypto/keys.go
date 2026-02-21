package pctrcrypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

func LoadRSAPublicKeyFromPEMFile(path string) (*rsa.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("invalid pem")
	}

	// Try PKIX first.
	ifc, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err == nil {
		pk, ok := ifc.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("not rsa public key")
		}
		return pk, nil
	}

	// Fallback to PKCS1.
	pk, err2 := x509.ParsePKCS1PublicKey(block.Bytes)
	if err2 != nil {
		return nil, fmt.Errorf("parse public key: %v / %v", err, err2)
	}
	return pk, nil
}

func LoadRSAPrivateKeyFromPEMFile(path string) (*rsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("invalid pem")
	}

	// Try PKCS8.
	ifc, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		pk, ok := ifc.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("not rsa private key")
		}
		return pk, nil
	}

	// Fallback to PKCS1.
	pk, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err2 != nil {
		return nil, fmt.Errorf("parse private key: %v / %v", err, err2)
	}
	return pk, nil
}

func EncryptCardData(pub *rsa.PublicKey, plaintext []byte) ([]byte, error) {
	h := sha256.New()
	return rsa.EncryptOAEP(h, rand.Reader, pub, plaintext, nil)
}

func DecryptCardData(priv *rsa.PrivateKey, ciphertext []byte) ([]byte, error) {
	h := sha256.New()
	return rsa.DecryptOAEP(h, rand.Reader, priv, ciphertext, nil)
}
