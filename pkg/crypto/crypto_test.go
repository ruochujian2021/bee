// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package crypto_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethersphere/bee/pkg/crypto"
)

func TestGenerateSecp256k1Key(t *testing.T) {
	t.Parallel()

	k1, err := crypto.GenerateSecp256k1Key()
	if err != nil {
		t.Fatal(err)
	}
	if k1 == nil {
		t.Fatal("nil key")
	}
	k2, err := crypto.GenerateSecp256k1Key()
	if err != nil {
		t.Fatal(err)
	}
	if k2 == nil {
		t.Fatal("nil key")
	}

	if bytes.Equal(k1.D.Bytes(), k2.D.Bytes()) {
		t.Fatal("two generated keys are equal")
	}
}

func TestGenerateSecp256k1EDG(t *testing.T) {
	t.Parallel()

	k1, err := crypto.EDGSecp256_K1.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if k1 == nil {
		t.Fatal("nil key")
	}
	k2, err := crypto.EDGSecp256_K1.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if k2 == nil {
		t.Fatal("nil key")
	}

	if bytes.Equal(k1.D.Bytes(), k2.D.Bytes()) {
		t.Fatal("two generated keys are equal")
	}
}

func TestNewAddress(t *testing.T) {
	t.Parallel()

	k, err := crypto.GenerateSecp256k1Key()
	if err != nil {
		t.Fatal(err)
	}
	a, err := crypto.NewOverlayAddress(k.PublicKey, 1, common.HexToHash("0x1").Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if l := len(a.Bytes()); l != 32 {
		t.Errorf("got address length %v, want %v", l, 32)
	}

	_, err = crypto.NewOverlayAddress(k.PublicKey, 1, nil)
	if !errors.Is(err, crypto.ErrBadHashLength) {
		t.Fatalf("expected %v, got %v", crypto.ErrBadHashLength, err)
	}
}

func TestEncodeSecp256k1PrivateKey(t *testing.T) {
	t.Parallel()

	k1, err := crypto.GenerateSecp256k1Key()
	if err != nil {
		t.Fatal(err)
	}
	d, err := crypto.EncodeSecp256k1PrivateKey(k1)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := crypto.DecodeSecp256k1PrivateKey(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1.D.Bytes(), k2.D.Bytes()) {
		t.Fatal("encoded and decoded keys are not equal")
	}
}

func TestEncodeSecp256k1EDG(t *testing.T) {
	t.Parallel()

	k1, err := crypto.EDGSecp256_K1.Generate()
	if err != nil {
		t.Fatal(err)
	}
	d, err := crypto.EDGSecp256_K1.Encode(k1)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := crypto.EDGSecp256_K1.Decode(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1.D.Bytes(), k2.D.Bytes()) {
		t.Fatal("encoded and decoded keys are not equal")
	}
}

func TestSecp256k1PrivateKeyFromBytes(t *testing.T) {
	t.Parallel()

	data := []byte("data")

	k1 := crypto.Secp256k1PrivateKeyFromBytes(data)
	if k1 == nil {
		t.Fatal("nil key")
	}

	k2 := crypto.Secp256k1PrivateKeyFromBytes(data)
	if k2 == nil {
		t.Fatal("nil key")
	}

	if !bytes.Equal(k1.D.Bytes(), k2.D.Bytes()) {
		t.Fatal("two generated keys are not equal")
	}
}

func TestGenerateSecp256r1Key(t *testing.T) {
	t.Parallel()

	k1, err := crypto.GenerateSecp256r1Key()
	if err != nil {
		t.Fatal(err)
	}
	if k1 == nil {
		t.Fatal("nil key")
	}
	k2, err := crypto.GenerateSecp256r1Key()
	if err != nil {
		t.Fatal(err)
	}
	if k2 == nil {
		t.Fatal("nil key")
	}

	if bytes.Equal(k1.D.Bytes(), k2.D.Bytes()) {
		t.Fatal("two generated keys are equal")
	}
}

func TestGenerateSecp256r1EDG(t *testing.T) {
	t.Parallel()

	r1, err := crypto.EDGSecp256_R1.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if r1 == nil {
		t.Fatal("nil key")
	}
	r2, err := crypto.EDGSecp256_R1.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if r2 == nil {
		t.Fatal("nil key")
	}

	if bytes.Equal(r1.D.Bytes(), r2.D.Bytes()) {
		t.Fatal("two generated keys are equal")
	}
}

func TestEncodeSecp256r1PrivateKey(t *testing.T) {
	t.Parallel()

	r1, err := crypto.GenerateSecp256r1Key()
	if err != nil {
		t.Fatal(err)
	}
	d, err := crypto.EncodeSecp256r1PrivateKey(r1)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := crypto.DecodeSecp256r1PrivateKey(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r1.D.Bytes(), r2.D.Bytes()) {
		t.Fatal("encoded and decoded keys are not equal")
	}
}

func TestEncodeSecp256r1EDG(t *testing.T) {
	t.Parallel()

	r1, err := crypto.EDGSecp256_R1.Generate()
	if err != nil {
		t.Fatal(err)
	}
	d, err := crypto.EDGSecp256_R1.Encode(r1)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := crypto.EDGSecp256_R1.Decode(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r1.D.Bytes(), r2.D.Bytes()) {
		t.Fatal("encoded and decoded keys are not equal")
	}
}

func TestNewEthereumAddress(t *testing.T) {
	t.Parallel()

	privKeyHex := "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"
	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	privKey, err := crypto.DecodeSecp256k1PrivateKey(privKeyBytes)
	if err != nil {
		t.Fatal(err)
	}
	expectAddressHex := "2f63cbeb054ce76050827e42dd75268f6b9d87c5"
	expectAddress, err := hex.DecodeString(expectAddressHex)
	if err != nil {
		t.Fatal(err)
	}
	address, err := crypto.NewEthereumAddress(privKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(address, expectAddress) {
		t.Fatalf("address mismatch %x %x", address, expectAddress)
	}
}
