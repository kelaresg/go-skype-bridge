// +build !cgo

package main

import (
	"image"
	"io"

	"golang.org/x/image/webp"
)

func NewCryptoHelper(bridge *Bridge) Crypto {
	if !bridge.Config.Bridge.Encryption.Allow {
		bridge.Log.Warnln("Bridge built without end-to-bridge encryption, but encryption is enabled in config")
	}
	bridge.Log.Debugln("Bridge built without end-to-bridge encryption")
	return nil
}

func decodeWebp(r io.Reader) (image.Image, error) {
	return webp.Decode(r)
}
