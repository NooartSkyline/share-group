// Package qr renders QR codes as PNG images.
package qr

import qrcode "github.com/skip2/go-qrcode"

// Generator implements port.QRGenerator.
type Generator struct{}

func New() Generator { return Generator{} }

func (Generator) PNG(text string, size int) ([]byte, error) {
	return qrcode.Encode(text, qrcode.Medium, size)
}

// Terminal renders text as a compact QR code made of block characters.
func Terminal(text string) (string, error) {
	q, err := qrcode.New(text, qrcode.Low)
	if err != nil {
		return "", err
	}
	return q.ToSmallString(false), nil
}
